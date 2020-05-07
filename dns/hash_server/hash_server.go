package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/buraksezer/consistent"
	"github.com/cespare/xxhash"
	"github.com/gorilla/mux"
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
}

type clusterInfo struct {
	token   clusterToken // Each participant cluster has a unique token for consistent hashing choice
	members []nsInfo     // Each participant cluster has >= 3 members in a normal Raft design. Any one of them can handle r/w request
}

type hashServerStore struct {
	clusters map[clusterToken]clusterInfo
	lookup   *consistent.Consistent
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
		members: make([]nsInfo, 0),
	}
	for _, m := range c.Members {
		glueRecord, err := dns.NewRR(m.GlueRecord)
		if err != nil {
			continue
		}
		mi := nsInfo{
			nsName:     m.NsName,
			glueRecord: glueRecord,
		}
		ci.members = append(ci.members, mi)
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
		if err != nil {
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

	go func() {
		if err := http.ListenAndServe(":"+strconv.Itoa(port), router); err != nil {
			done <- err
		}
	}()
}

type batchedDNSQuestions struct {
	token          clusterToken    // token of cluster that should handle these questions
	chosenServerIP net.IP          // IP address of a single member of cluster where the query will be forwarded
	questions      *[]dns.Question // Questions that should go to the same single cluster
}

func keyToClusterToken(m consistent.Member) clusterToken {
	return clusterToken(m.String())
}

// Returns true if through direct query we have all answers. Otherwise return false
func tryDirectQuery(store *hashServerStore, batchList []batchedDNSQuestions, msg *dns.Msg) bool {
	var wg sync.WaitGroup

	var answerLock sync.Mutex // protects hasAllAnswers and msg
	hasAllAnswers := true

	for _, b := range batchList {
		batch := b // Capture loop var
		wg.Add(1)
		go func(wg *sync.WaitGroup) {
			defer wg.Done()

			m := new(dns.Msg)
			m.Question = *batch.questions

			in, err := dns.Exchange(m, batch.chosenServerIP.String()+":53") // XXX: make UDP reliable
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
		randomMember := cluster.members[rand.Intn(len(cluster.members))]
		randomMemberIP := getIPFromARecord(randomMember.glueRecord)
		wg.Add(1)
		go func(wg *sync.WaitGroup) {
			defer wg.Done()

			m := new(dns.Msg)
			m.Question = msg.Question

			in, err := dns.Exchange(m, randomMemberIP.String()+":53") // XXX: make UDP reliable
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

func serveHashServerUDPAPI(store *hashServerStore) {
	server := &dns.Server{Addr: ":53", Net: "udp"}
	go server.ListenAndServe()
	dns.HandleFunc(".", func(w dns.ResponseWriter, r *dns.Msg) {
		// HashServer has no choice but to make the request on behalf of the client
		// (at least to the layer of this single logical DNS nameserver)
		// due to possibility of asterisk records

		// Step 1: for each DNS question, locate which servers to forward the partial questions
		batchMap := make(map[clusterToken]batchedDNSQuestions)
		for _, q := range r.Question {
			token := keyToClusterToken(store.lookup.LocateKey([]byte(q.Name)))
			if _, ok := batchMap[token]; !ok { // not exist
				cluster := store.clusters[token]
				// Select a random member to query
				randomMember := cluster.members[rand.Intn(len(cluster.members))]
				randomMemberIP := getIPFromARecord(randomMember.glueRecord)
				batchMap[token] = batchedDNSQuestions{
					token:          token,
					chosenServerIP: randomMemberIP,
					questions:      &[]dns.Question{},
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
		if tryDirectQuery(store, batchList, mDirect) {
			w.WriteMsg(mDirect)
			return
		}

		// If not all answers available, try broadcast and merge
		// Don't care about conflicts for now
		mBroadcast := new(dns.Msg)
		mBroadcast.Id = r.Id
		mBroadcast.Question = append(mBroadcast.Question, r.Question...)
		tryBroadcastAndMerge(store, mBroadcast)

		w.WriteMsg(mBroadcast)
	})
}

// We currently do not handle adding a new cluster dynamically in code.
// The makeshift design is to have the server stop and does the migration manually.
// However in real-world deployment we need to implement transparent migration without killing servers.

func main() {
	rand.Seed(time.Now().Unix())

	store := hashServerStore{
		clusters: make(map[clusterToken]clusterInfo),
	}

	configFile := flag.String("config", "", "filename to load initial config")
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
	serveHashServerUDPAPI(&store)

	<-httpDone
}
