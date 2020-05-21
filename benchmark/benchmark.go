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
	addr := fmt.Sprintf("http://%s:9121/add", *dnsIP)
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

// wg *sync.WaitGroup
func throughputClient(done chan interface{}, counter chan int, readRatio float64) {
	// defer wg.Done()
	count := 1
	c := new(dns.Client)
	for {
		// finish when done is closed
		select {
		case <-done:
			//end the loop
			counter <- count
			return
		default:
			r := rand.Float64()
			// determine read or write request
			if r < readRatio {
				// send dns query
				req := new(dns.Msg)
				// randomize query
				index := rand.Intn(100)
				// set up through populate.sh
				req.SetQuestion(fmt.Sprintf("example-%d.com.", index), dns.TypeA)
				req.RecursionDesired = false
				// make a request
				_, _, err := c.Exchange(req, *dnsIP+":53")
				if err != nil {
					log.Printf("Req failed %v\n", err)
				} else {
					count++
				}
			} else {
				// write request
				index := rand.Intn(256)
				writeRR := fmt.Sprintf("write-%d.com IN A 100.100.101.%d", index, index)
				if writeRequest(writeRR) {
					count++
				}
			}
		}
	}
}

func measureThroughput(duration int, numClient int, readRatio float64) int {
	done := make(chan interface{})
	counter := make(chan int)
	// wait for client threads
	// wg := new(sync.WaitGroup)
	for i := 0; i < numClient; i++ {
		// wg.Add(1)
		go throughputClient(done, counter, readRatio)
	}
	// run each client for duration seconds
	time.Sleep(time.Duration(duration) * time.Second)
	close(done)
	// collect count of number of finished DNS queries
	// wg.Wait()
	numQueries := 0
	for i := 0; i < numClient; i++ {
		t := <-counter
		log.Println(t)
		numQueries += t
	}
	log.Printf("%.2f queries per second\n", float64(numQueries)/float64(duration))
	// return number of queries done
	return numQueries
}

func latencyClient(c chan []int, numQuery int) {
	var latencies []int
	sumLatencies := 0
	for i := 0; i < numQuery; i++ {
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
		_, _, err := c.Exchange(req, *dnsIP+":53")
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

var dnsIP *string

func main() {
	rand.Seed(time.Now().Unix())
	dnsIP = flag.String("ip", "127.0.0.1", "the ip address to send dns query to")
	// t for throughput
	benchmarkType := flag.String("type", "l", "what to benchmark")
	numClient := flag.Int("client", 1, "number of client threads")
	numQuery := flag.Int("query", 100, "[latency] number of queries to send")
	duration := flag.Int("duration", 30, "[throughput] number of seconds to run")
	readRatio := flag.Float64("readratio", 1, "[throughput] read request ratio in [0,1]")

	flag.Parse()
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

	default:
		log.Println("Wrong benchmark type")
	}
}
