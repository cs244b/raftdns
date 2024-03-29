// Copyright 2015 The etcd Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"flag"
	"log"
	"strconv"
	"strings"

	"github.com/coreos/etcd/raft/raftpb"
)

func main() {
	cluster := flag.String("cluster", "http://127.0.0.1:9021", "comma separated cluster peers")
	id := flag.Int("id", 1, "node ID")
	// PLEASE use 9121! hash_server.go assumes this port for forwarding
	httpAPIPort := flag.Int("port", 9121, "dns HTTP API server port")
	join := flag.Bool("join", false, "join an existing cluster")
	migrate := flag.Bool("migrate", false, "need to coordinate migration")
	// include the port in hash server ip addr
	hashServer := flag.String("hash", "", "the ip address of a hash server to help with migration")
	// This config file should contain the JSON representaion of type jsonClusterInfo for the new cluster
	// see new_config.json for example
	config := flag.String("config", "", "the path to the json file containing the config for this cluster")
	usePaged := flag.Bool("page", false, "use paged version of DNS store")
	// zoneFile := flag.String("zonefile", "", "Zone file provided during init")
	flag.Parse()

	proposeC := make(chan string)
	defer close(proposeC)
	confChangeC := make(chan raftpb.ConfChange)
	defer close(confChangeC)

	if *usePaged {
		// Use special paged version

		// raft provides a commit stream for the proposals from the http api
		var dnsStore *pagedDNSStore
		getSnapshot := func() ([]byte, error) { return dnsStore.getSnapshot() }
		commitC, errorC, snapshotterReady := newRaftNode(*id, strings.Split(*cluster, ","), *join, getSnapshot, proposeC, confChangeC)

		clusterIP := []string{}
		for i, ip := range strings.Split(*cluster, ",") {
			// !Careful, hardcoded length
			clusterIP = append(clusterIP, ip[:len(ip)-4]+strconv.Itoa(*httpAPIPort))
			log.Printf("cluster ip %v\n", clusterIP[i])
		}

		dnsStore = newPagedDNSStore(<-snapshotterReady, proposeC, commitC, errorC, clusterIP, *id)

		// For dig queries
		PagedServeUDPAPI(dnsStore)
		// For write requests
		PagedServeHTTPAPI(dnsStore, *httpAPIPort, confChangeC, errorC)
	} else {
		// raft provides a commit stream for the proposals from the http api
		var dnsStore *dnsStore
		getSnapshot := func() ([]byte, error) { return dnsStore.getSnapshot() }
		commitC, errorC, snapshotterReady := newRaftNode(*id, strings.Split(*cluster, ","), *join, getSnapshot, proposeC, confChangeC)
		clusterIP := []string{}
		for i, ip := range strings.Split(*cluster, ",") {
			// !Careful, hardcoded length
			clusterIP = append(clusterIP, ip[:len(ip)-4]+strconv.Itoa(*httpAPIPort))
			log.Printf("cluster ip %v\n", clusterIP[i])
		}

		dnsStore = newDNSStore(<-snapshotterReady, proposeC, commitC, errorC, clusterIP, *id)

		// For dig queries
		serveUDPAPI(dnsStore)
		// For migration
		if *migrate {
			go migrateDNS(dnsStore, hashServer, config)
		}
		// For write requests
		serveHTTPAPI(dnsStore, *httpAPIPort, confChangeC, errorC)
	}
}
