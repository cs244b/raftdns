package main

import (
	"log"
	"strings"

	"github.com/miekg/dns"
)

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

// Minimal version right now: replay resurive query
func HandleRecursiveQuery(req *dns.Msg, r *dns.Msg, s *dnsStore) {
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
func ProcessDNSQuery(req *dns.Msg, s *dnsStore) *dns.Msg {
	res := new(dns.Msg)
	res.SetReply(req)

	s.rlockStore() // Lock!

	unhandledQuestions := []dns.Question{}
	// We ignore Qclass for now, since we only care about IN.
	for _, q := range req.Question {
		canHandleCurrentQuestion := HandleSingleQuestion(q.Name, q.Qtype, res, s)
		if !canHandleCurrentQuestion {
			unhandledQuestions = append(unhandledQuestions, q)
		}
	}

	s.runlockStore()

	// Recursive Query
	res.RecursionAvailable = true
	if req.RecursionDesired && len(unhandledQuestions) > 0 {
		// Create req for recursive with only the subset of unhandled questions
		reqRec := req.Copy()
		reqRec.Question = unhandledQuestions
		// Create a new recursive query for unhandled questions
		resRec := new(dns.Msg)
		HandleRecursiveQuery(reqRec, resRec, s)
		// Merge results of local and recursive
		res.Answer = append(res.Answer, resRec.Answer...)
		res.Extra = append(res.Extra, resRec.Extra...)
		res.Ns = append(res.Ns, resRec.Ns...)
	}

	return res
}

// See rfc1034 4.3.2
// Returns whether this single question can be handled locally
func HandleSingleQuestion(name string, qType uint16, r *dns.Msg, s *dnsStore) bool {
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
				for _, rr := range rrs {
					nsRR, ok := rr.(*dns.NS)
					if ok {
						glueRRPtrs = append(glueRRPtrs, s.getGlueRecords(nsRR.Ns)...)
					}
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

// Implicitly at port 53
func serveUDPAPI(store *dnsStore) {
	server := &dns.Server{Addr: ":53", Net: "udp"}
	go server.ListenAndServe()
	dns.HandleFunc(".", func(w dns.ResponseWriter, r *dns.Msg) {
		res := ProcessDNSQuery(r, store)
		w.WriteMsg(res)
	})
}
