package main

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/coreos/etcd/snap"

	"github.com/miekg/dns"
)

/**
 * Structure for dnsStore.store:
 * [name] => {
 *	 A_TYPE: [...array of entries]
 *   ...
 * }
 *
 * TODO: explore if we can handle wildcard more efficiently
 */

type rrType = uint16 // From RR_Header::RrType
// XXX: this is made mapping to strings for easy snapshotting for first pass.
// Consider making this more efficient
type dnsRRTypeMap map[rrType][]string

type dnsStore struct {
	proposeC    chan<- string // New entry proposals is sent to here
	mu          sync.RWMutex
	store       map[string]dnsRRTypeMap // ...Where actual entrys are stored
	snapshotter *snap.Snapshotter
	// XXX: optionally need a cache for glue records
}

func newDNSStore(snapshotter *snap.Snapshotter, proposeC chan<- string, commitC <-chan *string, errorC <-chan error) *dnsStore {
	s := &dnsStore{
		proposeC:    proposeC,
		store:       make(map[string]dnsRRTypeMap),
		snapshotter: snapshotter,
	}
	// Replay historical commits to in-memory store
	s.readCommits(commitC, errorC)
	// Read more commits for incoming (commits that are caused by proposals)
	go s.readCommits(commitC, errorC)
	return s
}

// a.com => (a, com), empty string means cannot pop
func popLeftmostLabel(domain string) (string, string) {
	chunks := strings.SplitN(domain, ".", 2)
	switch len(chunks) {
	case 0:
		return "", ""
	case 1:
		return "", domain
	default:
		return chunks[0], chunks[1]
	}
}

// Convert a list of strings into RRs.
// Silently consume errors
func stringsToRRs(sList []string) []dns.RR {
	rrs := []dns.RR{}
	for _, s := range sList {
		rr, err := dns.NewRR(s)
		if err == nil {
			rrs = append(rrs, rr)
		}
	}
	return rrs
}

func (s *dnsStore) HandleRecursiveQuery(req *dns.Msg, r *dns.Msg) {
	c := new(dns.Client)
	in, _, err := c.Exchange(req, "8.8.8.8:53")
	if err != nil {
		log.Fatal("Recursive failure")
	}
	*r = *in
	return
}

// Handle query from outside.
// Supporting iterative queries only ATM
func (s *dnsStore) ProcessDNSQuery(req *dns.Msg) *dns.Msg {
	res := new(dns.Msg)
	res.SetReply(req)

	// RECURSIVE
	res.RecursionAvailable = true
	if req.RecursionDesired {
		s.HandleRecursiveQuery(req, res)
		return res
	}
	// RECURSIVE

	s.mu.RLock() // Lock!

	// We ignore Qclass for now, since we only care about IN.
	for _, q := range req.Question {
		s.HandleSingleQuestion(q.Name, q.Qtype, res)
	}

	s.mu.RUnlock()
	return res
}

func (s *dnsStore) getGlueRecords(nsName string) []*dns.RR {
	glueRecords := []*dns.RR{}

	hasPreciseMatch := false
	if typeMap, ok := s.store[nsName]; ok {
		if glueARecords, ok := typeMap[dns.TypeA]; ok {
			for _, glueRecordString := range glueARecords {
				rr, err := dns.NewRR(glueRecordString)
				if err == nil {
					hasPreciseMatch = true
					glueRecords = append(glueRecords, &rr)
				}
			}
		}
	}
	if hasPreciseMatch {
		return glueRecords
	}
	// Try star matching
	var leftLabel, rest string // dummy init s.t. avoids local scope
	rest = nsName
	for {
		leftLabel, rest = popLeftmostLabel(rest)
		if leftLabel == "" {
			break
		}
		hasMatch := false
		// Try with wildcard prefix
		if wildcardTypeMap, ok := s.store["*."+rest]; ok {
			wildcardGlueRecordList := wildcardTypeMap[dns.TypeA]
			for _, wildCardGlueRecordString := range wildcardGlueRecordList {
				rr, err := dns.NewRR(wildCardGlueRecordString)
				if err == nil {
					hasMatch = true
					glueRecords = append(glueRecords, &rr)
				}
			}
		}
		if hasMatch {
			break
		}
	}
	return glueRecords
}

// See rfc1034 4.3.2
// TODO: ensure SOA
func (s *dnsStore) HandleSingleQuestion(name string, qType uint16, r *dns.Msg) {
	domainName := strings.ToLower(name)
	hasPreciseMatch := false

	// XXX: handle CNAME if we have time
	typeMap, hasTypeMap := s.store[domainName]
	if hasTypeMap {
		rrStringList := typeMap[qType]
		if rrStringList != nil && len(rrStringList) != 0 {
			for _, rrString := range rrStringList {
				rr, err := dns.NewRR(rrString)
				if err == nil {
					hasPreciseMatch = true // We have a precise match if we push entry
					r.Answer = append(r.Answer, rr)
				}
			}
		}
	}

	// Has precise match, no need to scan further
	if hasPreciseMatch {
		return
	}

	// 4.3.2.3.b, border of zone
	if hasTypeMap {
		nsList := typeMap[dns.TypeNS]
		if len(nsList) > 0 { // has a new zone
			rrs := stringsToRRs(nsList)
			// If we actually delegate to a new zone
			if len(rrs) > 0 {
				// Try append glue record if exist
				glueRRPtrs := []*dns.RR{}
				for _, nsRR := range rrs {
					glueRRPtrs = append(glueRRPtrs, s.getGlueRecords(nsRR.Header().Name)...)
				}

				glueRRs := []dns.RR{}
				for _, glueRRPtr := range glueRRPtrs {
					glueRRs = append(glueRRs, *glueRRPtr)
				}

				r.Ns = append(r.Ns, rrs...)
				r.Extra = append(r.Extra, glueRRs...)
				return // We have delegated to another server. Done.
			}
		}
	}

	// Otherwise, try repeating the process for star
	// 4.3.2.3.c
	var leftLabel, rest string // dummy init s.t. avoids local scope
	rest = domainName
	for {
		leftLabel, rest = popLeftmostLabel(rest)
		if leftLabel == "" {
			break
		}
		hasMatch := false
		// Try with wildcard prefix
		if wildcardTypeMap, ok := s.store["*."+rest]; ok {
			wildcardRRList := wildcardTypeMap[qType]
			for _, wildCardRRString := range wildcardRRList {
				rr, err := dns.NewRR(wildCardRRString)
				if err == nil {
					hasMatch = true
					// Spec asks us to change the owner to be w/o star
					rr.Header().Name = domainName
					r.Answer = append(r.Answer, rr)
				}
			}
		}
		if hasMatch {
			break
		}
	}
	// Don't worry about *.somedomain.com for NS records, they are not supported
}

// 2 Propose methods.
// The proposed messages would arrive as commits if committed

// DNS modify request proposal types
const (
	AddRR = iota
	DeleteRR
)

type dnsProposal struct {
	ProposalType int // Proposal type, ADD_RR or DELETE_RR
	// Only used by ADD_RR
	RRString string

	// Only used by DELETE_RR
	Name   string
	RRType uint16
}

func (s *dnsStore) ProposeAddRR(rrString string) {
	proposal := dnsProposal{
		ProposalType: AddRR,
		RRString:     rrString,
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(proposal); err != nil {
		log.Fatal(err)
	}
	s.proposeC <- buf.String()
}

// XXX: This delete is not really granular, consider improve its API
func (s *dnsStore) ProposeDeleteRRs(name string, rrType uint16) {
	proposal := dnsProposal{
		ProposalType: DeleteRR,
		Name:         name,
		RRType:       rrType,
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(proposal); err != nil {
		log.Fatal(err)
	}
	s.proposeC <- buf.String()
}

// Not protected with lock
func (s *dnsStore) insertRR(rr dns.RR) {
	domainName := strings.ToLower(rr.Header().Name)
	typeMap, hasTypeMap := s.store[domainName]
	if !hasTypeMap {
		typeMap = make(dnsRRTypeMap)
		s.store[domainName] = typeMap
	}

	_, hasRRs := typeMap[rr.Header().Rrtype]
	if !hasRRs {
		typeMap[rr.Header().Rrtype] = []string{}
	}
	typeMap[rr.Header().Rrtype] = append(typeMap[rr.Header().Rrtype], rr.String())
}

// Not protected with lock
func (s *dnsStore) loadFromZoneFile(filename string) error {
	zoneFile, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer zoneFile.Close()
	zp := dns.NewZoneParser(zoneFile, "", "")
	for rr, ok := zp.Next(); ok; rr, ok = zp.Next() {
		if ok {
			s.insertRR(rr)
		}
	}
	return nil
}

func (s *dnsStore) readCommits(commitC <-chan *string, errorC <-chan error) {
	for data := range commitC {
		if data == nil {
			// done replaying log; new data incoming
			// OR signaled to load snapshot
			snapshot, err := s.snapshotter.Load()
			if err == snap.ErrNoSnapshot {
				return
			}
			if err != nil && err != snap.ErrNoSnapshot {
				log.Panic(err)
			}
			log.Printf("loading snapshot at term %d and index %d", snapshot.Metadata.Term, snapshot.Metadata.Index)
			if err := s.recoverFromSnapshot(snapshot.Data); err != nil {
				log.Panic(err)
			}
			continue
		}

		var proposal dnsProposal
		dec := gob.NewDecoder(bytes.NewBufferString(*data))
		if err := dec.Decode(&proposal); err != nil {
			log.Fatalf("dnsStore: could not decode message (%v)", err)
		}

		switch proposal.ProposalType {
		case AddRR:
			rr, err := dns.NewRR(proposal.RRString)
			if err == nil {
				s.mu.Lock()
				s.insertRR(rr)
				s.mu.Unlock()
			}
		case DeleteRR:
			s.mu.Lock()
			if typeMap, ok := s.store[proposal.Name]; ok {
				if _, ok := typeMap[proposal.RRType]; ok {
					delete(s.store[proposal.Name], proposal.RRType)
				}
				// put this if in the above then clause?
				if len(typeMap) == 0 {
					delete(s.store, proposal.Name)
				}
			}
			s.mu.Unlock()
		default:
			log.Fatalf("dnsStore: bad proposal type (%v)", proposal.ProposalType)
		}
	}
	if err, ok := <-errorC; ok {
		log.Fatal(err)
	}
}

func (s *dnsStore) getSnapshot() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return json.Marshal(s.store)
}

func (s *dnsStore) recoverFromSnapshot(snapshot []byte) error {
	var store map[string]dnsRRTypeMap
	if err := json.Unmarshal(snapshot, &store); err != nil {
		return err
	}
	s.mu.Lock()
	s.store = store
	s.mu.Unlock()
	return nil
}

// Implicitly at port 53
func serveUDPAPI(store *dnsStore) {
	server := &dns.Server{Addr: ":53", Net: "udp"}
	go server.ListenAndServe()
	dns.HandleFunc(".", func(w dns.ResponseWriter, r *dns.Msg) {
		res := store.ProcessDNSQuery(r)
		w.WriteMsg(res)
	})
}
