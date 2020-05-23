package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/miekg/dns"
)

// Not robust yet, can be off by 1
func getPercentile(vals []int, percentile int) int {
	sort.Ints(vals)
	a := vals[len(vals)*percentile/100-1]
	return a
}

func writeRequest(request string) bool {
	addr := fmt.Sprintf("http://%s:9121/add", getDNSServerIP())
	req, err := http.NewRequest("PUT", addr, strings.NewReader(request))
	if err != nil {
		log.Printf("writeRequest failed to form request: %v\n", err)
		return false
	}
	req.ContentLength = int64(len(request))
	_, err = http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("writeRequest add failed: %v\n", err)
		return false
	}
	return true
}

func deleteRequest(name string, rrType string) bool {
	// hardcoded JSON formatted string
	jsonRR := fmt.Sprintf("{ \"name\": \"%s\", \"rrType\": \"%s\" }", name, rrType)
	addr := fmt.Sprintf("http://%s:9121/delete", getDNSServerIP())
	req, err := http.NewRequest("PUT", addr, strings.NewReader(jsonRR))
	if err != nil {
		log.Printf("deleteRequest failed to form request: %v\n", err)
		return false
	}
	req.ContentLength = int64(len(jsonRR))
	_, err = http.DefaultClient.Do(req)
	if err != nil {
		log.Println(jsonRR)
		log.Printf("deleteRequest delete failed: %v\n", err)
		return false
	}
	return true
}

func populateDNS(numRR int) {
	i := 0
	for a := 0; a < 256; a++ {
		for b := 0; b < 256; b++ {
			for c := 0; c < 256; c++ {
				for d := 0; d < 256; d++ {
					domain := fmt.Sprintf("example-%d.com.", i)
					writeRR := fmt.Sprintf("%s IN A %d.%d.%d.%d", domain, a, b, c, d)
					// if !writeRequest(writeRR) {
					// 	log.Printf("Populate at %d failed\n", i)
					// }
					if d%2 == 0 {
						go writeRequest(writeRR)
					} else {
						writeRequest(writeRR)
					}
					if i%10000 == 0 {
						time.Sleep(2 * time.Second)
					}

					i++
					if i == numRR {
						return
					}
				}
			}
		}
	}
}

// wg *sync.WaitGroup
func throughputClient(done chan interface{}, readCounter chan int, writeCounter chan int, readRatio float64) {
	// defer wg.Done()
	var readCount, writeCount int
	c := new(dns.Client)
	for {
		// finish when done is closed
		select {
		case <-done:
			//end the loop
			readCounter <- readCount
			writeCounter <- writeCount
			return
		default:
			r := rand.Float64()
			// determine read or write request
			if r < readRatio {
				// send dns query
				req := new(dns.Msg)
				var domain string
				if *localread {
					// set up through populate.sh
					i := rand.Intn(100)
					j := rand.Intn(100)
					domain = fmt.Sprintf("example-%d-%d.com.", i, j)
				} else {
					// randomize query
					index := rand.Intn(*dbSize)
					domain = fmt.Sprintf("example-%d.com.", index)
				}

				req.SetQuestion(domain, dns.TypeA)
				req.RecursionDesired = false
				// make a request
				_, _, err := c.Exchange(req, getDNSServerIP()+":53")
				if err != nil {
					log.Printf("Req failed %v\n", err)
				} else {
					readCount++
				}
			} else {
				// write request
				a := rand.Intn(256)
				b := rand.Intn(256)
				// index := rand.Intn(256 * 256)
				index := a * b
				domain := fmt.Sprintf("write-%d.com.", index)
				// we do not have deduplication in dns store yet
				deleteRequest(domain, "A")
				writeRR := fmt.Sprintf("%s IN A 100.100.%d.%d", domain, a, b)
				if writeRequest(writeRR) {
					// should we add 2 since delete is also a write!
					writeCount++
				}
			}
		}
	}
}

// return number of finished (read queries, write queries)
func measureThroughput(duration int, numClient int, readRatio float64) (int, int) {
	done := make(chan interface{})
	readCounter := make(chan int)
	writeCounter := make(chan int)
	// wait for client threads
	// wg := new(sync.WaitGroup)
	for i := 0; i < numClient; i++ {
		// wg.Add(1)
		go throughputClient(done, readCounter, writeCounter, readRatio)
	}
	// run each client for duration seconds
	time.Sleep(time.Duration(duration) * time.Second)
	close(done)
	// collect count of number of finished DNS queries
	// wg.Wait()
	var readQueries, writeQueries int
	for i := 0; i < numClient; i++ {
		r := <-readCounter
		w := <-writeCounter
		// log.Printf("Read %d, Written %d\n", r, w)
		readQueries += r
		writeQueries += w
	}
	log.Println()
	log.Printf("%.2f reads per second\n", float64(readQueries)/float64(duration))
	log.Printf("%.2f writes per second\n", float64(writeQueries)/float64(duration))
	log.Printf("Total %.2f operations per second\n\n", float64(readQueries+writeQueries)/float64(duration))
	// return number of queries done
	return readQueries, writeQueries
}

func latencyClient(c chan []int, numQuery int) {
	var latencies []int
	sumLatencies := 0
	for i := 0; i < numQuery; i++ {
		// only reading example-%d.com yet, i.e., local read
		// take current time
		c := new(dns.Client)
		req := new(dns.Msg)
		// randomize query
		index := rand.Intn(100)
		// set up through populate.sh
		req.SetQuestion(fmt.Sprintf("example-%d.com.", index), dns.TypeA)
		req.RecursionDesired = false

		// time the request
		startTime := time.Now()
		// choose a dns server at random to send the query
		_, _, err := c.Exchange(req, getDNSServerIP()+":53")
		if err != nil {
			log.Printf("Req failed %v\n", err)
		} else {
			endTime := time.Now()
			latency := endTime.Sub(startTime).Microseconds()
			latencies = append(latencies, int(latency))
			sumLatencies += int(latency)
		}
	}
	sort.Ints(latencies)
	log.Println(latencies)

	// p50, p90, p99
	var percentiles = []int{50, 90, 99}
	if len(latencies) > 0 {
		log.Printf("Avg latency: %f ms\n", float64(sum(latencies))/float64(len(latencies))/1000)
		for _, p := range percentiles {
			log.Printf("P%d latency: %f ms", p, float64(getPercentile(latencies, p))/1000)
		}
	}
	c <- latencies
}

func sum(array []int) int {
	result := 0
	for _, v := range array {
		result += v
	}
	return result
}

func measureLatency(numQuery int, numClient int) []int {

	// channel to send array of latency
	c := make(chan []int)
	for i := 0; i < numClient; i++ {
		go latencyClient(c, numQuery)
	}

	var latencies []int
	for i := 0; i < numClient; i++ {
		l := <-c
		latencies = append(latencies, l...)
	}

	sort.Ints(latencies)
	var percentiles = []int{50, 90, 99}
	if len(latencies) > 0 {
		log.Println("========== overall latency ==========")
		log.Printf("Avg latency: %f ms\n", float64(sum(latencies))/float64(len(latencies))/1000)
		for _, p := range percentiles {
			log.Printf("P%d latency: %f ms", p, float64(getPercentile(latencies, p))/1000)
		}
	}
	return latencies
}

// choose dns server uniformly at random
func getDNSServerIP() string {
	return dnsIPs[rand.Intn(len(dnsIPs))]
}

var dnsIPs []string
var localread *bool
var dbSize *int

func main() {
	rand.Seed(time.Now().Unix())
	dnsIP := flag.String("ip", "127.0.0.1", "comma separated ip addresses to send dns query to")
	// t for throughput
	benchmarkType := flag.String("type", "l", "what to benchmark")
	numClient := flag.Int("client", 1, "number of client threads")
	numQuery := flag.Int("query", 100, "[latency] number of queries to send")
	duration := flag.Int("duration", 30, "[throughput] number of seconds to run")
	readRatio := flag.Float64("readratio", 1, "[throughput] read request ratio in [0,1]")
	localread = flag.Bool("localread", false, "whether to read from a small set of domain")
	dbSize = flag.Int("size", 100, "number of RR in dns server")

	flag.Parse()
	dnsIPs = strings.Split(*dnsIP, ",")

	switch *benchmarkType {
	case "latency":
		fallthrough
	case "l":
		// measure latency
		measureLatency(*numQuery, *numClient)

	case "throughput":
		fallthrough
	case "t":
		// measure throughput
		measureThroughput(*duration, *numClient, *readRatio)

	case "populate":
		fallthrough
	case "p":
		populateDNS(*dbSize)
	default:
		log.Println("Wrong benchmark type")
	}
}
