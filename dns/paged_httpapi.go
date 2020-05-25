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

// serveHTTPAPI starts a http server with APIs to write to nameservers
func PagedServeHTTPAPI(store *pagedDNSStore, port int, confChangeC chan<- raftpb.ConfChange, errorC <-chan error) {
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
