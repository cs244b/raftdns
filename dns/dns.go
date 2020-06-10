package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

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

		// handle CNAME record
		if !hasPreciseMatch && qType != dns.TypeCNAME {
			cnameList := typeMap[dns.TypeCNAME]
			if cnameList != nil && len(cnameList) != 0 {
				// should only have one CNAME RR
				cnameRR, err := dns.NewRR(cnameList[0])
				if err == nil && cnameRR != nil {
					r.Answer = append(r.Answer, cnameRR)
					// get the canonical name for this request domain name
					realCNameRR, ok := cnameRR.(*dns.CNAME)
					if ok {
						cnameData := realCNameRR.Target
						// retry with canonical name
						return HandleSingleQuestion(cnameData, qType, r, s)
					}
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
	server := &dns.Server{Addr: "0.0.0.0:53", Net: "udp"}
	go server.ListenAndServe()
	dns.HandleFunc(".", func(w dns.ResponseWriter, r *dns.Msg) {
		res := ProcessDNSQuery(r, store)
		w.WriteMsg(res)
	})
}

type jsonNsInfo struct {
	NsName     string `json:"name"`
	GlueRecord string `json:"glue"`
}
type jsonClusterInfo struct {
	ClusterToken string       `json:"cluster"`
	Members      []jsonNsInfo `json:"members"`
}

func getClusterInfo(hashServer string) []jsonClusterInfo {
	// send HTTP request to hash server to retrieve cluster info
	addr := "http://" + hashServer + ":9121/clusterinfo"
	req, err := http.NewRequest("GET", addr, strings.NewReader(""))
	if err != nil {
		log.Fatal("Cannot form get cluster info request")
	}
	req.ContentLength = 0

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	if resp.StatusCode == http.StatusInternalServerError {
		log.Fatal("Hash server internal server error")
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Fatal("Failed to read cluster info response", err)
	}

	// fmt.Printf("Received: %s\n", body)
	jsonClusters := make([]jsonClusterInfo, 0)
	err = json.Unmarshal(body, &jsonClusters)
	if err != nil {
		log.Fatal(err)
	}
	// return cluster
	return jsonClusters
}

func updateConfig(clusters []jsonClusterInfo, configPath *string) []jsonClusterInfo {
	file, err := os.Open(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	fileContent, err := ioutil.ReadAll(file)
	if err != nil {
		log.Fatal("Update config read failed: ", err)
	}
	var newCluster jsonClusterInfo
	err = json.Unmarshal(fileContent, &newCluster)
	if err != nil {
		log.Fatal("Update config unmarshal:", err)
	}
	// fmt.Println(newCluster.ClusterToken)
	return append(clusters, newCluster)
}

// print out []jsonClusterInfo
func PrintClusterConfig(clusters []jsonClusterInfo) {
	for _, cluster := range clusters {
		fmt.Println(cluster.ClusterToken)
		for _, member := range cluster.Members {
			fmt.Println("\t", member.NsName)
			fmt.Println("\t", member.GlueRecord)
		}
	}
}

// send the updated cluster info to other Raft clusters
// the last entry in clusters is the new cluster
func sendClusterInfo(clusters []jsonClusterInfo, destIP string) {
	// pick the first member ot send the cluster config to
	addr := "http://" + destIP + ":9121/addcluster"
	clusterJSON, err := json.Marshal(clusters)
	log.Println("Sending cluster info", string(clusterJSON), "to", addr)
	if err != nil {
		log.Fatal("sendClusterInfo: ", err)
	}
	req, err := http.NewRequest("PUT", addr, strings.NewReader(string(clusterJSON)))
	if err != nil {
		log.Fatal("sendClusterInfo: ", err)
	}
	req.ContentLength = int64(len(string(clusterJSON)))
	resp, err := http.DefaultClient.Do(req)
	log.Println("Received cluster info response", destIP)
	if err != nil {
		log.Fatal("sendClusterInfo: ", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		log.Fatal("sendClusterInfo request failed at server side")
	}
}

func retrieveRR(clusters []jsonClusterInfo, store *dnsStore) {
	var wg sync.WaitGroup
	// excluding our own cluster
	errorC := make(chan error)
	for _, cluster := range clusters[:len(clusters)-1] {
		member := cluster.Members[0]
		t := strings.Split(member.GlueRecord, " ")
		memberIP := t[len(t)-1]
		addr := "http://" + memberIP + ":9121/getrecord"
		wg.Add(1)
		go func(addr string, store *dnsStore, errorC chan<- error, wg *sync.WaitGroup) {
			defer wg.Done()
			log.Println("Start retrieving RR from", addr)
			counter := 0
			for {
				req, err := http.NewRequest("GET", addr, strings.NewReader(strconv.Itoa(counter)))
				log.Println(strconv.Itoa(counter))
				if err != nil {
					errorC <- err
					return
				}
				req.ContentLength = int64(len(strconv.Itoa(counter)))
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					errorC <- err
					return
				}

				body, err := ioutil.ReadAll(resp.Body)
				if err != nil {
					errorC <- err
					return
				}
				// parse body
				var records []string
				err = json.Unmarshal(body, &records)
				if err != nil {
					errorC <- err
					return
				}
				log.Println("Received", records)
				if len(records) == 1 && records[0] == "Done" {
					// finished reading
					log.Println("Finished reading RRs")
					return
				}
				// go through Raft to add RRs
				for _, rrString := range records {
					if !checkValidRRString(rrString) {
						// log.Fatal("Received bad RR")
						errorC <- errors.New("Recevied bad RR")
						return
					}
					// this is async, might cause too much traffic
					// no need to acquire locks here since it is proposed through channel
					store.ProposeAddRR(rrString)
					log.Println("Adding RR:", rrString)
				}
				counter++
			}
		}(addr, store, errorC, &wg)
	}

	// check if a go thread failed
	c := make(chan struct{})
	go func() {
		// waiting for retrieveRR go routines to finish
		wg.Wait()
		c <- struct{}{}
	}()
	select {
	case <-c:
		// success
		return
	case err := <-errorC:
		// some go thread failed
		log.Fatal("retrieveRR", err)
	}
}

// TODO: notify every other cluster to garbage collect
// the records that they no longer need to keep b/c of
// the new cluster
func startGarbageCollect(clusters []jsonClusterInfo) {
	log.Println("Garbage Collection started")
}

// notify the hash sverver to stop processing writes
func disableWrites(hashServer string) {
	// send write disable request to hash server and
	// wait for ack before return
	addr := "http://" + hashServer + ":9121/disablewrite"
	req, err := http.NewRequest("PUT", addr, strings.NewReader(""))
	if err != nil {
		log.Fatal("DisableWrites failure: ", err)
	}
	req.ContentLength = 0
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatal("DisableWrites failure: ", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		log.Fatal("DisableWrites request failed at server side")
	}
	log.Println("Hash Server write disabled")
}

// notify the hash server to start processing writes
func enableWrites(hashServer string) {
	addr := "http://" + hashServer + ":9121/enablewrite"
	req, err := http.NewRequest("PUT", addr, strings.NewReader(""))
	if err != nil {
		log.Fatal("EnableWrites failure: ", err)
	}
	req.ContentLength = 0
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatal("EnableWrites failure: ", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		log.Fatal("EnableWrites request failed at server side")
	}
	log.Println("Enable writes at the hash server at", hashServer)
}

func migrateDNS(store *dnsStore, hashServer *string, config *string) {
	// retrieve cluster info
	clusters := getClusterInfo(*hashServer)
	// disable writes
	disableWrites(*hashServer)
	// update hash configuration
	clusters = updateConfig(clusters, config)
	PrintClusterConfig(clusters)
	// send new config to other Raft clusters
	for _, cluster := range clusters[:len(clusters)-1] {
		t := strings.Split(cluster.Members[0].GlueRecord, " ")
		destIP := t[len(t)-1]
		sendClusterInfo(clusters, destIP)
	}
	// retrieve RR
	retrieveRR(clusters, store)
	// update cluster info at hash servers
	sendClusterInfo(clusters, *hashServer)
	// kick off garbage collection at other clusters
	startGarbageCollect(clusters)
	// enable write
	enableWrites(*hashServer)
}
