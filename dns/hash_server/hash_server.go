package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/buraksezer/consistent"
	"github.com/cespare/xxhash"
	"github.com/gorilla/mux"
	lru "github.com/hashicorp/golang-lru"
	"github.com/miekg/dns"
)

type clusterToken string

func (t clusterToken) String() string {
	return string(t)
}

type hasher struct{}

func (h hasher) Sum64(data []byte) uint64 {
	// Use a proper hash function for uniformity.
	return xxhash.Sum64(data)
}

type nsInfo struct {
	nsName     string
	glueRecord dns.RR
	aliveSince time.Time    // Mark since when the server is considered alive (will be a timestamp in the future if server is found to be down during polling)
	aliveMutex sync.RWMutex // Mutex protecting aliveSince
}

type clusterInfo struct {
	token   clusterToken // Each participant cluster has a unique token for consistent hashing choice
	members []*nsInfo    // Each participant cluster has >= 3 members in a normal Raft design. Any one of them can handle r/w request
}

type rrType = uint16

type cacheRRInfo struct {
	ttl        uint32
	createTime time.Time
}

type cacheRRTypeMap map[rrType]map[string]cacheRRInfo

type hashServerStore struct {
	clusters map[clusterToken]clusterInfo
	lookup   *consistent.Consistent
	mu       sync.RWMutex
	cache    *lru.ARCCache
}

/**
 * config format:
 * [{ cluster: "clusterName", members: [{ name: "ns.example.com.", glue: "ns.example.com. 3600 IN A 3.3.3.3" }, ...] }, ...]
 * The following structs are for serialization only
 */
type jsonNsInfo struct {
	NsName     string `json:"name"`
	GlueRecord string `json:"glue"`
}

type jsonClusterInfo struct {
	ClusterToken string       `json:"cluster"`
	Members      []jsonNsInfo `json:"members"`
}

func getIPFromARecord(rr dns.RR) net.IP {
	switch rr.(type) {
	case *dns.A:
		return rr.(*dns.A).A
	default:
		return nil
	}
}

func (c *jsonClusterInfo) intoClusterInfo() clusterInfo {
	ci := clusterInfo{
		token:   clusterToken(c.ClusterToken),
		members: make([]*nsInfo, 0),
	}
	now := time.Now()
	for _, m := range c.Members {
		glueRecord, err := dns.NewRR(m.GlueRecord)
		if err != nil || glueRecord == nil {
			continue
		}
		mi := nsInfo{
			nsName:     m.NsName,
			glueRecord: glueRecord,
			aliveSince: now,
		}
		ci.members = append(ci.members, &mi)
	}
	return ci
}

func intoClusterMap(clusters []jsonClusterInfo) map[clusterToken]clusterInfo {
	m := make(map[clusterToken]clusterInfo)
	for _, c := range clusters {
		cInfo := c.intoClusterInfo()
		m[cInfo.token] = cInfo
	}
	return m
}

func loadConfig(store *hashServerStore, filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	fileContent, err := ioutil.ReadAll(file)
	if err != nil {
		return err
	}
	jsonClusters := make([]jsonClusterInfo, 0)
	err = json.Unmarshal(fileContent, &jsonClusters)
	if err != nil {
		return err
	}
	store.clusters = intoClusterMap(jsonClusters)
	return nil
}

type deleteRequestPayload struct {
	Name         string `json:"name"`
	RRTypeString string `json:"rrType"`
}

// Problems: how to handle star queries?
func serveHashServerHTTPAPI(store *hashServerStore, port int, done chan<- error) {
	router := mux.NewRouter()

	// Below API mirrors that of httpapi.go for dns_server.

	// PUT /add
	// body: string(rrString)
	router.HandleFunc("/add", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			http.Error(w, "Method has to be PUT", http.StatusBadRequest)
			return
		}
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			log.Printf("Cannot read /add body: %v\n", err)
			http.Error(w, "Bad PUT body", http.StatusBadRequest)
			return
		}
		rrString := string(body)

		rr, err := dns.NewRR(rrString)
		if err != nil || rr == nil {
			log.Println("Bad RR request")
			http.Error(w, "Bad RR request", http.StatusBadRequest)
			return
		}
		// Be CAREFUL! Always ensure that the name requested ends with "."
		cToken := clusterToken(store.lookup.LocateKey([]byte(rr.Header().Name)).String())
		cluster := store.clusters[cToken]
		// Randomly pick one from the cluster
		randomMember := cluster.members[rand.Intn(len(cluster.members))]
		randomMemberIP := getIPFromARecord(randomMember.glueRecord)
		u := fmt.Sprintf("http://%s:9121/add", randomMemberIP.String())
		req, err := http.NewRequest("PUT", u, strings.NewReader(rrString))
		if err != nil {
			log.Println("Cannot forward request /add")
			http.Error(w, "Cannot forward request /add", http.StatusInternalServerError)
			return
		}
		req.ContentLength = int64(len(rrString))
		resp, err := http.DefaultClient.Do(req) // In its independent goroutine so blocking is fine
		w.WriteHeader(resp.StatusCode)
	})

	// PUT /delete
	// body: JSON({ name: string, rrType: string("A" | "NS" for now) })
	router.HandleFunc("/delete", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			http.Error(w, "Method has to be PUT", http.StatusBadRequest)
			return
		}

		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			log.Printf("Cannot read /delete body: %v\n", err)
			http.Error(w, "Bad PUT body", http.StatusBadRequest)
			return
		}

		var delReq deleteRequestPayload
		err = json.NewDecoder(bytes.NewReader(body)).Decode(&delReq)

		if err != nil {
			log.Printf("Cannot parse /delete body: %v\n", err)
			http.Error(w, "Bad PUT body", http.StatusBadRequest)
			return
		}
		// Be CAREFUL! Always ensure that the name requested ends with "."
		cToken := clusterToken(store.lookup.LocateKey([]byte(delReq.Name)).String())
		cluster := store.clusters[cToken]
		// Randomly pick one from the cluster
		randomMember := cluster.members[rand.Intn(len(cluster.members))]
		randomMemberIP := getIPFromARecord(randomMember.glueRecord)
		u := fmt.Sprintf("http://%s:9121/delete", randomMemberIP.String())
		req, err := http.NewRequest("PUT", u, bytes.NewReader(body))
		if err != nil {
			log.Println("Cannot forward request /delete")
			http.Error(w, "Cannot forward request /delete", http.StatusInternalServerError)
			return
		}
		req.ContentLength = int64(len(body))
		resp, err := http.DefaultClient.Do(req) // In its independent goroutine so blocking is fine
		w.WriteHeader(resp.StatusCode)
	})

	router.HandleFunc("/clusterinfo", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			http.Error(w, "Method has to be PUT", http.StatusBadRequest)
			return
		}

		// form a list clusters, each cluster is a list of nsInfo of nodes in the cluster

		file, err := os.Open(*configFile)
		if err != nil {
			log.Fatal(err)
		}
		defer file.Close()
		fileContent, err := ioutil.ReadAll(file)
		if err != nil {
			log.Fatal(err)
		}

		io.WriteString(w, string(fileContent))
		return
	})

	// probably updateconfig is a bettre name
	// the current name is consistent with httpapi.go
	router.HandleFunc("/addcluster", func(w http.ResponseWriter, r *http.Request) {
		log.Println("add cluster")
		if r.Method != "PUT" {
			http.Error(w, "Method has to be PUT", http.StatusBadRequest)
			return
		}

		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			log.Printf("Cannot read /addcluster body: %v\n", err)
			http.Error(w, "Bad PUT body", http.StatusBadRequest)
			return
		}

		jsonClusters := make([]jsonClusterInfo, 0)
		err = json.Unmarshal(body, &jsonClusters)
		if err != nil {
			log.Fatal(err)
		}

		// update cluster info in dnsStore
		store.mu.Lock()
		store.clusters = intoClusterMap(jsonClusters)
		store.mu.Unlock()
		// update consistent
		cfg := consistent.Config{
			PartitionCount:    len(store.clusters),
			ReplicationFactor: 2, // We are forced to have number larger than 1
			Load:              3,
			Hasher:            hasher{},
		}
		log.Println("Received cluster update, setting new config")
		store.lookup = consistent.New(nil, cfg)
		for _, cluster := range store.clusters {
			store.lookup.Add(clusterToken(cluster.token))
			log.Println("New cluster:", cluster.token)
		}

		w.WriteHeader(http.StatusNoContent)
	})

	router.HandleFunc("/disablewrite", func(w http.ResponseWriter, r *http.Request) {
		log.Println("disable write operations at hash servers")
		// TODO: fill in the logic for disabling writes
	})

	router.HandleFunc("/enablewrite", func(w http.ResponseWriter, r *http.Request) {
		log.Println("enable write operations at hash servers")
		// TODO: fill in the logic for enable writes
	})

	go func() {
		if err := http.ListenAndServe(":"+strconv.Itoa(port), router); err != nil {
			done <- err
		}
	}()
}

type batchedDNSQuestions struct {
	token         clusterToken    // token of cluster that should handle these questions
	chosenServers []*nsInfo       // IP address of a single member of cluster where the query will be forwarded
	questions     *[]dns.Question // Questions that should go to the same single cluster
}

func keyToClusterToken(m consistent.Member) clusterToken {
	return clusterToken(m.String())
}

func sendDNSMsgUntilSuccess(m *dns.Msg, servers []*nsInfo) (*dns.Msg, error) {
	c := dns.Client{
		Timeout: time.Millisecond * 500, //
	}
	var in *dns.Msg
	var err error
	for _, s := range servers {
		serverIP := getIPFromARecord(s.glueRecord)
		in, _, err = c.Exchange(m, serverIP.String()+":53")
		if err == nil {
			return in, nil
		}
		// Otherwise server not reachable, mark it to be not alive for the next 0.5 second
		s.aliveMutex.Lock() // Write lock
		s.aliveSince = time.Now().Add(500 * time.Millisecond)
		s.aliveMutex.Unlock()
	}
	return in, err
}

// Returns true if through direct query we have all answers. Otherwise return false
func tryDirectQuery(store *hashServerStore, batchList []batchedDNSQuestions, msg *dns.Msg, rd bool) bool {
	var wg sync.WaitGroup

	var answerLock sync.Mutex // protects hasAllAnswers and msg
	hasAllAnswers := true

	for _, b := range batchList {
		batch := b // Capture loop var
		wg.Add(1)
		go func(wg *sync.WaitGroup) {
			defer wg.Done()

			m := new(dns.Msg)
			m.RecursionDesired = rd // Also seek recursive. See comment below for behavior implications
			m.Question = *batch.questions

			// This will try all servers one by one on preference order until depletion of the list
			in, err := sendDNSMsgUntilSuccess(m, batch.chosenServers)
			if err != nil {
				answerLock.Lock()
				hasAllAnswers = false
				answerLock.Unlock()
			} else {
				hasAnswersForAllQuestionsToThisServer := true

				// We will validate if we get all the questions answered
				for _, q := range *batch.questions {
					// We can make this more efficient, but simple array iter for now (answer is small)
					hasAnswerForThisQuestion := false
					for _, a := range in.Answer {
						if q.Name == a.Header().Name && q.Qtype == a.Header().Rrtype {
							hasAnswerForThisQuestion = true
							break
						}
					}

					// XXX: below is commented out, since it is possible for glue record to store on a
					// different cluster than that of NS record.

					// If no direct answer for this question, check if there is a new subzone has authority
					// if !hasAnswerForThisQuestion {
					// 	for _, ns := range in.Ns {
					// 		if strings.HasSuffix(q.Name, ns.Header().Name) { // XXX: This is not precise enough
					// 			hasAnswerForThisQuestion = true
					// 			break
					// 		}
					// 	}
					// }

					// Otherwise, we definitely has no answer for this question. Short circuit out
					if !hasAnswerForThisQuestion {
						hasAnswersForAllQuestionsToThisServer = false
						break
					}
				}

				answerLock.Lock()
				if hasAnswersForAllQuestionsToThisServer {
					// Merge answer to msg passed in
					msg.Answer = append(msg.Answer, in.Answer...)
					msg.Ns = append(msg.Ns, in.Ns...)
					msg.Extra = append(msg.Extra, in.Extra...)
				} else {
					// Abort and saying
					hasAllAnswers = false
				}
				answerLock.Unlock()
			}
		}(&wg)
	}

	wg.Wait()
	return hasAllAnswers
}

// Try broadcast to all clusters and merge their responses as answer
func tryBroadcastAndMerge(store *hashServerStore, msg *dns.Msg) {
	var wg sync.WaitGroup

	var answerLock sync.Mutex // protects msg

	for _, cluster := range store.clusters {
		chosenServers := getPreferredServerIPsInOrder(cluster.members)
		wg.Add(1)
		go func(wg *sync.WaitGroup) {
			defer wg.Done()

			m := new(dns.Msg)
			// Recursion is deliberately not done here.
			// With recursion enabled, if record is not present but is a valid DNS query
			// on an existing domain, tryDirectQuery will already have the answer.
			m.Question = msg.Question

			// This will try all servers one by one on preference order until depletion of the list
			in, err := sendDNSMsgUntilSuccess(m, chosenServers)
			if err != nil {
				answerLock.Lock()
				msg.Answer = append(msg.Answer, in.Answer...)
				msg.Ns = append(msg.Ns, in.Ns...)
				msg.Extra = append(msg.Extra, in.Extra...)
				answerLock.Unlock()
			}
		}(&wg)
	}

	wg.Wait()
}

// We prefer alive servers to server with previous unresponsiveness.
func getPreferredServerIPsInOrder(members []*nsInfo) []*nsInfo {
	var aliveServers []*nsInfo
	var deadServers []*nsInfo // servers that are considered not yet alive.
	now := time.Now()
	for _, ni := range members {
		ni.aliveMutex.RLock() // Read Lock
		if ni.aliveSince.Before(now) {
			aliveServers = append(aliveServers, ni)
		} else {
			deadServers = append(deadServers, ni)
		}
		ni.aliveMutex.RUnlock()
	}
	// Randomly shuffle alive servers (to distribute load, since we try servers from the start)
	rand.Shuffle(len(aliveServers), func(i, j int) {
		aliveServers[i], aliveServers[j] = aliveServers[j], aliveServers[i]
	})
	// Sort dead servers by when we treat them as alive again
	sort.Slice(deadServers, func(i, j int) bool {
		deadServers[i].aliveMutex.RLock() // Read Lock
		deadServers[j].aliveMutex.RLock()
		defer deadServers[i].aliveMutex.RUnlock()
		defer deadServers[j].aliveMutex.RUnlock()
		return deadServers[i].aliveSince.Before(deadServers[j].aliveSince)
	})
	// Append deadServers after alive servers, such that they are tried only after all alive are tried
	return append(aliveServers, deadServers...)
}

func (store *hashServerStore) getCacheRecords(domainName string, qType uint16) []*dns.RR {
	cacheRecords := []*dns.RR{}

	// check cache
	tempCacheMap, hasCacheMap := store.cache.Get(domainName)

	if hasCacheMap {
		cacheMap, ok := tempCacheMap.(cacheRRTypeMap)
		if !ok {
			log.Fatal("Cache type cast failed")
		}
		// rlcok to protect reading into map
		store.mu.RLock()
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
		store.mu.RUnlock()
	}

	return cacheRecords
}

func (store *hashServerStore) queryCache(r *dns.Msg) (*dns.Msg, bool) {
	log.Println("Query hashserver cache")
	response := new(dns.Msg)

	// for now, we only serve from cache if all questions can be answered from cache
	for _, q := range r.Question {
		cacheRRPtrs := store.getCacheRecords(q.Name, q.Qtype)
		// no cache records
		if len(cacheRRPtrs) == 0 {
			// fall back to query Raft clusters
			return nil, false
		}
		// use cache records as answers
		for _, cr := range cacheRRPtrs {
			response.Answer = append(response.Answer, *cr)
		}
	}

	log.Println("[success] Query hashserver cache")
	return response, true
}

func (store *hashServerStore) addCacheRecord(rr dns.RR) {
	if rr.Header().Name == "." {
		// ignore dummy entry from 8.8.8.8 DNS
		return
	}

	// check if domain name is in cache
	domainName := strings.ToLower(rr.Header().Name)
	tmpCacheMap, hasCacheMap := store.cache.Get(domainName)
	var cacheMap cacheRRTypeMap
	if !hasCacheMap {
		cacheMap = make(cacheRRTypeMap)
		// domainName -> cacheMap
	} else {
		// cast back to cacheRRTypeMap
		cacheMap = tmpCacheMap.(cacheRRTypeMap)
	}
	store.cache.Add(domainName, cacheMap)

	// check if rr type is in cache
	rType := rr.Header().Rrtype
	// wlock to protect map update
	store.mu.Lock()
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
	// unlock!
	store.mu.Unlock()

	// restore ttl
	rr.Header().Ttl = ttl
	log.Printf("Added cache %v\n", rr.String())
}

func (store *hashServerStore) addCacheRecords(records []dns.RR) {
	for _, rr := range records {
		store.addCacheRecord(rr)
	}
}

func serveHashServerUDPAPI(store *hashServerStore, useCache bool) {
	server := &dns.Server{Addr: "0.0.0.0:53", Net: "udp"}
	go server.ListenAndServe()
	dns.HandleFunc(".", func(w dns.ResponseWriter, r *dns.Msg) {
		// HashServer has no choice but to make the request on behalf of the client
		// (at least to the layer of this single logical DNS nameserver)
		// due to possibility of asterisk records

		// Step 0: Consult local cache
		if useCache {
			mCache, hasCache := store.queryCache(r)
			if hasCache {
				// can respond from cache directly
				mCache.Id = r.Id
				mCache.Response = true
				w.WriteMsg(mCache)
				return
			}
		}

		// Step 1: for each DNS question, locate which servers to forward the partial questions
		batchMap := make(map[clusterToken]batchedDNSQuestions)
		for _, q := range r.Question {
			token := keyToClusterToken(store.lookup.LocateKey([]byte(q.Name)))
			if _, ok := batchMap[token]; !ok { // not exist
				cluster := store.clusters[token]
				// Select a random member to query
				chosenServers := getPreferredServerIPsInOrder(cluster.members)
				batchMap[token] = batchedDNSQuestions{
					token:         token,
					chosenServers: chosenServers,
					questions:     &[]dns.Question{},
				}
			}
			*(batchMap[token].questions) = append(*(batchMap[token].questions), q)
		}

		batchList := make([]batchedDNSQuestions, 0, len(batchMap)) // XXX: use pointers
		for _, v := range batchMap {
			batchList = append(batchList, v)
		}

		// Try direct queries to target clusters per consistent hashing result first
		mDirect := new(dns.Msg)
		mDirect.Id = r.Id
		mDirect.Question = append(mDirect.Question, r.Question...)
		if tryDirectQuery(store, batchList, mDirect, r.RecursionDesired) {
			mDirect.Response = true
			w.WriteMsg(mDirect)
			// add cache here
			if useCache {
				store.addCacheRecords(mDirect.Answer)
				store.addCacheRecords(mDirect.Ns)
				store.addCacheRecords(mDirect.Extra)
			}
			return
		}

		// If not all answers available, try broadcast and merge
		// Don't care about conflicts for now
		mBroadcast := new(dns.Msg)
		mBroadcast.Id = r.Id
		mBroadcast.Question = append(mBroadcast.Question, r.Question...)
		tryBroadcastAndMerge(store, mBroadcast)
		mBroadcast.Response = true
		w.WriteMsg(mBroadcast)
		// add cache here
		if useCache {
			store.addCacheRecords(mBroadcast.Answer)
			store.addCacheRecords(mBroadcast.Ns)
			store.addCacheRecords(mBroadcast.Extra)
		}
	})
}

// We currently do not handle adding a new cluster dynamically in code.
// The makeshift design is to have the server stop and does the migration manually.
// However in real-world deployment we need to implement transparent migration without killing servers.

var configFile *string

func main() {
	rand.Seed(time.Now().Unix())

	store := hashServerStore{
		clusters: make(map[clusterToken]clusterInfo),
	}

	configFile = flag.String("config", "", "filename to load initial config")
	cacheSize := flag.Int("cache", 0, "cache size at hash server")
	flag.Parse()

	if err := loadConfig(&store, *configFile); err != nil {
		log.Fatalf("Failed to load config: %s\n", err)
	}

	// This is configured statically for now, but if we have time we can experiment with
	// how to transparently move records between clusters on adding a new partition.
	// Adding a new cluster should be a rare operation, so ignore for initial impl.
	cfg := consistent.Config{
		PartitionCount:    len(store.clusters),
		ReplicationFactor: 2, // We are forced to have number larger than 1
		Load:              3,
		Hasher:            hasher{},
	}

	store.lookup = consistent.New(nil, cfg)
	for _, cluster := range store.clusters {
		store.lookup.Add(cluster.token)
	}

	httpDone := make(chan error)
	// Hard coded port number
	serveHashServerHTTPAPI(&store, 9121, httpDone)
	if *cacheSize > 0 {
		// create cache
		var err error
		store.cache, err = lru.NewARC(*cacheSize)
		if err != nil {
			log.Fatal(err)
		}

		serveHashServerUDPAPI(&store, true)
	} else {
		serveHashServerUDPAPI(&store, false)
	}

	<-httpDone
}
