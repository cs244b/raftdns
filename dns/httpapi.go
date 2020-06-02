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
	log.Println("Computed cluster is", clusterToken)
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

	// PUT /addcluster
	// body: JSON(
	// [
	// 	{
	// 		"cluster": "cluster1",
	// 		"members": [
	// 			{
	// 				"name": "ns1.example.com.",
	// 				"glue": "ns1.example.com. 3600 IN A 172.18.0.10"
	// 			}
	// 		]
	// 	},
	// 	{
	// 		"cluster": "cluster2",
	// 		"members": [
	// 			{
	// 				"name": "ns2.example.com.",
	// 				"glue": "ns2.example.com. 3600 IN A 172.18.0.11"
	// 			}
	// 		]
	// 	}
	// ]
	// )
	// The body can be constructed from json.Marshal([]jsonClusterInfo)
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
		store.config = jsonClusters
		store.mu.Unlock()
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

	var domainNames []string
	type recordJSON struct {
		DomainName string
		RecordsMap dnsRRTypeMap
	}

	// GET /getrecord
	// body: string(index)
	// Responds with an array of rrString that correspond to the index-th domain name:
	// This node keeps an array of domain names, a copy of keys of store.store,
	// that the cluster is responsible for.
	// ** Note that this array of rrString will be empty if the domain name does not
	// belong to the new cluster.
	router.HandleFunc("/getrecord", func(w http.ResponseWriter, r *http.Request) {
		log.Println("HTTP request /getrecord")
		if r.Method != "GET" {
			http.Error(w, "Method has to be GET", http.StatusBadRequest)
			return
		}

		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			log.Printf("Cannot read /getrecord body: %v\n", err)
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
			}

			// check if this domain name should be migrated
			domain := domainNames[index]
			log.Println("Domain is ", domain)
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

	// PUT /member/{nodeID}
	// body: string(url, e.g. http://127.0.0.1:42379)
	// Example: curl -L http://127.0.0.1:12380/member/{replace with NODE_ID} -XPUT -d http://127.0.0.1:42379

	// DELETE /member/{nodeID}
	// body: none
	// Example: curl -L http://127.0.0.1:12380/member/{replace with NODE_ID} -XDELETE
	router.HandleFunc("/member/{nodeID}", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			url, err := ioutil.ReadAll(r.Body)
			if err != nil {
				log.Printf("Failed to read on PUT for /member (%v)\n", err)
				http.Error(w, "Failed on PUT", http.StatusBadRequest)
				return
			}

			vars := mux.Vars(r)
			nodeID, err := strconv.ParseUint(vars["nodeID"], 0, 64)
			if err != nil {
				log.Printf("Failed to convert ID for conf change (%v)\n", err)
				http.Error(w, "Failed on PUT", http.StatusBadRequest)
				return
			}

			cc := raftpb.ConfChange{
				Type:    raftpb.ConfChangeAddNode,
				NodeID:  nodeID,
				Context: url,
			}
			confChangeC <- cc

			// As above, optimistic that raft will apply the conf change
			w.WriteHeader(http.StatusNoContent)
		} else if r.Method == "DELETE" {
			vars := mux.Vars(r)
			nodeID, err := strconv.ParseUint(vars["nodeID"], 0, 64)
			if err != nil {
				log.Printf("Failed to convert ID for conf change (%v)\n", err)
				http.Error(w, "Failed on DELETE", http.StatusBadRequest)
				return
			}

			cc := raftpb.ConfChange{
				Type:   raftpb.ConfChangeRemoveNode,
				NodeID: nodeID,
			}
			confChangeC <- cc

			// As above, optimistic that raft will apply the conf change
			w.WriteHeader(http.StatusNoContent)
		} else {
			http.Error(w, "/member Method has to be PUT or DELELTE", http.StatusBadRequest)
			return
		}
		/* Example:
		./dns_server --id 1 --cluster http://127.0.0.1:10000 --port 10001
		curl -L http://127.0.0.1:10001/member/2 -XPUT -d http://127.0.0.1:20000
		./dns_server --join --id 2 --cluster http://127.0.0.1:10000,http://127.0.0.1:20000 --port 20001
		# Then do some DNS requests
		curl -L http://127.0.0.1:20001/member/1 -XDELETE
		*/
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
