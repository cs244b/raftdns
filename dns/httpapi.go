package main

import (
	"encoding/json"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"

	"github.com/buraksezer/consistent"
	"github.com/cespare/xxhash"
	"github.com/coreos/etcd/raft/raftpb"
	"github.com/miekg/dns"

	"github.com/gorilla/mux"
)

func checkValidRRString(s string) bool {
	rr, err := dns.NewRR(s)
	return err == nil && rr != nil
}

func shouldMigrate(domain string, store *dnsStore) bool {
	clusterToken := store.lookup.LocateKey([]byte(domain)).String()
	log.Println("Computed cluster ", clusterToken)
	return clusterToken == store.config[len(store.config)-1].ClusterToken
}

type hasher struct{}

func (h hasher) Sum64(data []byte) uint64 {
	// Use a proper hash function for uniformity.
	return xxhash.Sum64(data)
}

type clusterToken string

func (t clusterToken) String() string {
	return string(t)
}

// func (h *dnsHTTPAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
// 	key := r.RequestURI
// 	switch {
// 	case r.Method == "POST":
// 		url, err := ioutil.ReadAll(r.Body)
// 		if err != nil {
// 			log.Printf("Failed to read on POST (%v)\n", err)
// 			http.Error(w, "Failed on POST", http.StatusBadRequest)
// 			return
// 		}

// 		nodeId, err := strconv.ParseUint(key[1:], 0, 64)
// 		if err != nil {
// 			log.Printf("Failed to convert ID for conf change (%v)\n", err)
// 			http.Error(w, "Failed on POST", http.StatusBadRequest)
// 			return
// 		}

// 		cc := raftpb.ConfChange{
// 			Type:    raftpb.ConfChangeAddNode,
// 			NodeID:  nodeId,
// 			Context: url,
// 		}
// 		h.confChangeC <- cc

// 		// As above, optimistic that raft will apply the conf change
// 		w.WriteHeader(http.StatusNoContent)
// 	case r.Method == "DELETE":
// 		nodeId, err := strconv.ParseUint(key[1:], 0, 64)
// 		if err != nil {
// 			log.Printf("Failed to convert ID for conf change (%v)\n", err)
// 			http.Error(w, "Failed on DELETE", http.StatusBadRequest)
// 			return
// 		}

// 		cc := raftpb.ConfChange{
// 			Type:   raftpb.ConfChangeRemoveNode,
// 			NodeID: nodeId,
// 		}
// 		h.confChangeC <- cc

// 		// As above, optimistic that raft will apply the conf change
// 		w.WriteHeader(http.StatusNoContent)
// 	default:
// 		w.Header().Set("Allow", "PUT")
// 		w.Header().Add("Allow", "GET")
// 		w.Header().Add("Allow", "POST")
// 		w.Header().Add("Allow", "DELETE")
// 		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
// 	}
// }

type deleteRequestPayload struct {
	Name         string `json:"name"`
	RRTypeString string `json:"rrType"`
}

// serveHTTPAPI starts a http server with APIs to write to nameservers
func serveHTTPAPI(store *dnsStore, port int, confChangeC chan<- raftpb.ConfChange, errorC <-chan error) {
	router := mux.NewRouter()

	// PUT /add
	// body: string(rrString)
	router.HandleFunc("/add", func(w http.ResponseWriter, r *http.Request) {
		log.Println("Start add")
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

		if !checkValidRRString(rrString) {
			log.Println("Bad RR request")
			http.Error(w, "Bad RR request", http.StatusBadRequest)
			return
		}
		log.Println("\t adding", rrString)

		store.ProposeAddRR(rrString)
		// Optimistic-- no waiting for ack from raft. Value is not yet
		// committed so a subsequent GET on the key may return old value
		w.WriteHeader(http.StatusNoContent)
		log.Println("Done")
	})

	// PUT /delete
	// body: JSON({ name: string, rrType: string("A" | "NS" for now) })
	router.HandleFunc("/delete", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			http.Error(w, "Method has to be PUT", http.StatusBadRequest)
			return
		}
		var req deleteRequestPayload
		err := json.NewDecoder(r.Body).Decode(&req)

		if err != nil {
			log.Printf("Cannot parse /delete body: %v\n", err)
			http.Error(w, "Bad PUT body", http.StatusBadRequest)
			return
		}

		var rrType uint16
		switch req.RRTypeString {
		case "A":
			rrType = dns.TypeA
		case "NS":
			rrType = dns.TypeNS
		case "MX":
			rrType = dns.TypeMX
		default:
			log.Printf("Unsupported /delete rrType: %v\n", err)
			http.Error(w, "Unsupported /delete rrType", http.StatusBadRequest)
			return
		}

		store.ProposeDeleteRRs(req.Name, rrType)
		// Optimistic-- no waiting for ack from raft. Value is not yet
		// committed so a subsequent GET on the key may return old value
		w.WriteHeader(http.StatusNoContent)
	})

	router.HandleFunc("/addcache", func(w http.ResponseWriter, r *http.Request) {
		log.Println("addcache request")
		if r.Method != "PUT" {
			http.Error(w, "Method has to be PUT", http.StatusBadRequest)
			return
		}

		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			log.Printf("Cannot read /addcache body: %v\n", err)
			http.Error(w, "Bad PUT body", http.StatusBadRequest)
			return
		}
		rrString := string(body)

		rr, err := dns.NewRR(rrString)
		if err != nil || rr == nil {
			log.Println("Bad cache RR request")
			http.Error(w, "Bad RR request", http.StatusBadRequest)
			return
		}
		// do not whisper again
		log.Printf("add cache record from whisper %v\n", rr.String())
		store.addCacheRecord(rr, false)
		// Optimistic-- no waiting for ack from raft. Value is not yet
		// committed so a subsequent GET on the key may return old value
		w.WriteHeader(http.StatusNoContent)
	})

	// TODO: confChange requests
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
		store.config = jsonClusters
		PrintClusterConfig(store.config)
		// update consistent
		cfg := consistent.Config{
			PartitionCount:    len(store.config),
			ReplicationFactor: 2, // We are forced to have number larger than 1
			Load:              3,
			Hasher:            hasher{},
		}
		log.Println("Received cluster update, setting new config")
		store.lookup = consistent.New(nil, cfg)
		for _, clusterJSON := range store.config {
			store.lookup.Add(clusterToken(clusterJSON.ClusterToken))
		}

		w.WriteHeader(http.StatusNoContent)
	})

	// TODO: confChange requests
	var domainNames []string
	type recordJSON struct {
		DomainName string
		RecordsMap dnsRRTypeMap
	}

	router.HandleFunc("/getrecord", func(w http.ResponseWriter, r *http.Request) {
		log.Println("HTTP request /getrecord")
		if r.Method != "GET" {
			http.Error(w, "Method has to be GET", http.StatusBadRequest)
			return
		}

		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			log.Printf("Cannot read /addcluster body: %v\n", err)
			http.Error(w, "Bad PUT body", http.StatusBadRequest)
			return
		}
		index, err := strconv.Atoi(string(body))
		log.Println("Got index ", index)
		if err != nil {
			log.Printf("Cannot parse getrecord argument: %v\n", err)
			http.Error(w, "Bad index", http.StatusBadRequest)
			return
		}

		var records []string
		if index >= len(store.store) {
			// send a DONE message
			log.Println("Done migrating")
			records = append(records, "Done")
		} else {
			if index == 0 {
				// start migrating resource records
				// make a copy of keys (domain names)
				for k := range store.store {
					domainNames = append(domainNames, k)
				}
				log.Println("key copy: ", domainNames)
			}

			// check if this domain name should be migrated
			domain := domainNames[index]
			// log.Println(store.lookup.LocateKey([]byte(domain)).String())
			if shouldMigrate(domain, store) {
				for _, rrs := range store.store[domainNames[index]] {
					records = append(records, rrs...)
				}
			}
		}

		b, err := json.Marshal(records)
		if err != nil {
			log.Printf("Marshal failed: %v\n", err)
			http.Error(w, "Marshal Failed", http.StatusInternalServerError)
			return
		}

		log.Println("Writing RR", string(b))
		// writes back the json record
		io.WriteString(w, string(b))
	})

	go func() {
		if err := http.ListenAndServe(":"+strconv.Itoa(port), router); err != nil {
			log.Fatal(err)
		}
	}()

	// exit when raft goes down
	if err, ok := <-errorC; ok {
		log.Fatal(err)
	}
}
