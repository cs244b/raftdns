package main

import (
	"encoding/json"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"

	"github.com/coreos/etcd/raft/raftpb"
	"github.com/miekg/dns"

	"github.com/gorilla/mux"
)

func checkValidRRString(s string) bool {
	_, err := dns.NewRR(s)
	return err == nil
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

		store.ProposeAddRR(rrString)
		// Optimistic-- no waiting for ack from raft. Value is not yet
		// committed so a subsequent GET on the key may return old value
		w.WriteHeader(http.StatusNoContent)
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
			log.Printf("Cannot read /add body: %v\n", err)
			http.Error(w, "Bad PUT body", http.StatusBadRequest)
			return
		}
		rrString := string(body)

		rr, err := dns.NewRR(rrString)
		if err != nil {
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
