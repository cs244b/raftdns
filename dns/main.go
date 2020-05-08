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
	httpAPIPort := flag.Int("port", 9121, "dns HTTP API server port")
	join := flag.Bool("join", false, "join an existing cluster")
	// zoneFile := flag.String("zonefile", "", "Zone file provided during init")
	flag.Parse()

	proposeC := make(chan string)
	defer close(proposeC)
	confChangeC := make(chan raftpb.ConfChange)
	defer close(confChangeC)

	// raft provides a commit stream for the proposals from the http api
	var dnsStore *dnsStore
	getSnapshot := func() ([]byte, error) { return dnsStore.getSnapshot() }
	commitC, errorC, snapshotterReady := newRaftNode(*id, strings.Split(*cluster, ","), *join, getSnapshot, proposeC, confChangeC)

	clusterIP := []string{}
	for i, ip := range strings.Split(*cluster, ",") {
		// !Careful, hardcoded length
		clusterIP = append(clusterIP, ip[:len(ip)-5]+strconv.Itoa(*httpAPIPort))
		log.Printf("cluster ip %v\n", clusterIP[i])
	}

	dnsStore = newDNSStore(<-snapshotterReady, proposeC, commitC, errorC, clusterIP, *id)

	// For dig queries
	serveUDPAPI(dnsStore)
	// For write requests
	serveHTTPAPI(dnsStore, *httpAPIPort, confChangeC, errorC)
}
