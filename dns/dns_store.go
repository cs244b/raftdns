package main

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

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
type cacheRRInfo struct {
	ttl        uint32
	createTime time.Time
}

// rrType -> rrstring (with ttl = 0) -> (ttl, createTime)
type cacheRRTypeMap map[rrType]map[string]cacheRRInfo

type dnsStore struct {
	proposeC    chan<- string // New entry proposals is sent to here
	mu          sync.RWMutex
	store       map[string]dnsRRTypeMap // ...Where actual entrys are stored
	snapshotter *snap.Snapshotter
	cache       map[string]cacheRRTypeMap // cache for non-authoritative records
	// XXX: optionally need a cache for glue records
	// for sending http Cache requests
	cluster []string
	id      int
}

func newDNSStore(snapshotter *snap.Snapshotter, proposeC chan<- string, commitC <-chan *string, errorC <-chan error, cluster []string, id int) *dnsStore {
	s := &dnsStore{
		proposeC:    proposeC,
		store:       make(map[string]dnsRRTypeMap),
		snapshotter: snapshotter,
		cache:       make(map[string]cacheRRTypeMap),
		cluster:     cluster,
		id:          id,
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
		if err == nil && rr != nil {
			rrs = append(rrs, rr)
		}
	}
	return rrs
}

// Forward a new cache record to other machiens in the cluster
func (s *dnsStore) whisperAddCacheRecord(rr dns.RR) {

	for i, peerAddr := range s.cluster {
		// skip this machine
		if i == s.id-1 {
			continue
		}
		log.Printf("Whisper to %v\n", peerAddr)

		// send add cache request to other threads
		go func(peerAddr string) {
			addr := peerAddr + "/addcache"
			req, err := http.NewRequest("PUT", addr, strings.NewReader(rr.String()))
			if err != nil {
				log.Println("Cannot form cache forward request")
			}
			req.ContentLength = int64(len(rr.String()))
			// do not care about response
			http.DefaultClient.Do(req)
		}(peerAddr)
	}
}

func (s *dnsStore) addCacheRecord(rr dns.RR, whisper bool) {
	if rr.Header().Name == "." {
		// ignore dummy entry from 8.8.8.8 DNS
		return
	}

	if whisper {
		// broadcast the cache record to other machines in the cluster
		s.whisperAddCacheRecord(rr)
	}

	// check if domain name is in cache
	domainName := strings.ToLower(rr.Header().Name)
	cacheMap, hasCacheMap := s.cache[domainName]
	if !hasCacheMap {
		cacheMap = make(cacheRRTypeMap)
		s.cache[domainName] = cacheMap
	}

	// check if rr type is in cache
	rType := rr.Header().Rrtype
	cacheRRs, hasCR := cacheMap[rType]
	if !hasCR {
		cacheMap[rType] = make(map[string]cacheRRInfo)
		cacheRRs, _ = cacheMap[rType]
	}

	// use 0 tll for all cache records to generate same key for diff ttl
	ttl := rr.Header().Ttl
	rr.Header().Ttl = 0
	crInfo := cacheRRInfo{ttl, time.Now()}
	cacheRRs[rr.String()] = crInfo
	// restore ttl
	rr.Header().Ttl = ttl
	log.Printf("Added cache %v\n", rr.String())
}

func (s *dnsStore) addCacheRecords(records []dns.RR) {
	for _, rr := range records {
		s.addCacheRecord(rr, true)

	}
}

// Minimal version right now: replay resurive query
func (s *dnsStore) HandleRecursiveQuery(req *dns.Msg, r *dns.Msg) {
	c := new(dns.Client)
	in, _, err := c.Exchange(req, "8.8.8.8:53")
	if err != nil {
		log.Println("HandleRecursiveQuery Failed")
		return
	}
	r.Answer = in.Answer
	r.Ns = in.Ns
	r.Extra = in.Extra
	// cache the results
	s.addCacheRecords(r.Answer)
	s.addCacheRecords(r.Ns)
	s.addCacheRecords(r.Extra)
	return
}

// Handle query from outside.
// Supporting iterative queries only ATM
func (s *dnsStore) ProcessDNSQuery(req *dns.Msg) *dns.Msg {
	res := new(dns.Msg)
	res.SetReply(req)

	s.mu.RLock() // Lock!

	unhandledQuestions := []dns.Question{}
	// We ignore Qclass for now, since we only care about IN.
	for _, q := range req.Question {
		canHandleCurrentQuestion := s.HandleSingleQuestion(q.Name, q.Qtype, res)
		if !canHandleCurrentQuestion {
			unhandledQuestions = append(unhandledQuestions, q)
		}
	}

	s.mu.RUnlock()

	// Recursive Query
	res.RecursionAvailable = true
	if req.RecursionDesired && len(unhandledQuestions) > 0 {
		// Create req for recursive with only the subset of unhandled questions
		reqRec := req.Copy()
		reqRec.Question = unhandledQuestions
		// Create a new recursive query for unhandled questions
		resRec := new(dns.Msg)
		s.HandleRecursiveQuery(reqRec, resRec)
		// Merge results of local and recursive
		res.Answer = append(res.Answer, resRec.Answer...)
		res.Extra = append(res.Extra, resRec.Extra...)
		res.Ns = append(res.Ns, resRec.Ns...)
	}

	return res
}

func (s *dnsStore) getGlueRecords(nsName string) []*dns.RR {
	glueRecords := []*dns.RR{}

	hasPreciseMatch := false
	if typeMap, ok := s.store[nsName]; ok {
		if glueARecords, ok := typeMap[dns.TypeA]; ok {
			for _, glueRecordString := range glueARecords {
				rr, err := dns.NewRR(glueRecordString)
				if err == nil && rr != nil {
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
				if err == nil && rr != nil {
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

func (s *dnsStore) getCacheRecords(domainName string, qType uint16) []*dns.RR {
	cacheRecords := []*dns.RR{}
	// check cache
	cacheMap, hasCacheMap := s.cache[domainName]
	if hasCacheMap {
		cacheInfoMap := cacheMap[qType]
		if cacheInfoMap != nil && len(cacheInfoMap) != 0 {
			for crString, crInfo := range cacheInfoMap {
				cr, err := dns.NewRR(crString)
				// check cache validity
				timeDiff := uint32(time.Since(crInfo.createTime).Seconds())
				// newTTL := validCache(&crInfo)
				if err == nil && cr != nil && timeDiff < crInfo.ttl {
					// restore ttl
					cr.Header().Ttl = crInfo.ttl - timeDiff
					cacheRecords = append(cacheRecords, &cr)
				} else {
					// remove invalid cache
					delete(cacheInfoMap, crString)
				}
			}
		}
	}

	return cacheRecords
}

// See rfc1034 4.3.2
// Returns whether this single question can be handled locally
func (s *dnsStore) HandleSingleQuestion(name string, qType uint16, r *dns.Msg) bool {
	domainName := strings.ToLower(name)
	hasPreciseMatch := false

	// XXX: handle CNAME if we have time
	typeMap, hasTypeMap := s.store[domainName]
	if hasTypeMap {
		rrStringList := typeMap[qType]
		if rrStringList != nil && len(rrStringList) != 0 {
			for _, rrString := range rrStringList {
				rr, err := dns.NewRR(rrString)
				if err == nil && rr != nil {
					hasPreciseMatch = true // We have a precise match if we push entry
					r.Answer = append(r.Answer, rr)
				}
			}
		}
	}

	// Has precise match, no need to scan further
	if hasPreciseMatch {
		return true
	}

	// for now: query cache if no exact match
	cacheRRPtrs := s.getCacheRecords(domainName, qType)
	// check cache
	for _, cr := range cacheRRPtrs {
		r.Answer = append(r.Answer, *cr)
	}
	if len(cacheRRPtrs) > 0 {
		return true
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
				return false
				// We have delegated to another server. Done.
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
			return false
			// break
		}
		hasMatch := false
		// Try with wildcard prefix
		if wildcardTypeMap, ok := s.store["*."+rest]; ok {
			wildcardRRList := wildcardTypeMap[qType]
			for _, wildCardRRString := range wildcardRRList {
				rr, err := dns.NewRR(wildCardRRString)
				if err == nil && rr != nil {
					hasMatch = true
					// Spec asks us to change the owner to be w/o star
					rr.Header().Name = domainName
					r.Answer = append(r.Answer, rr)
				}
			}
		}
		if hasMatch {
			return true
			// break
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
			if err == nil && rr != nil {
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
