package main

import (
	"log"
	"time"

	"github.com/miekg/dns"
)

// Not robust yet
func getPercentile(vals []int, percentile int) int {
	a := vals[len(vals)*percentile/100-1]
	return a
}

func measureLatency(numQuery int) []int {
	var latencies []int
	sumLatencies := 0
	for i := 0; i < numQuery; i++ {
		// take current time
		c := new(dns.Client)
		req := new(dns.Msg)
		req.SetQuestion("google.com.", dns.TypeA)
		req.RecursionDesired = false
		startTime := time.Now()
		// make a request
		_, _, err := c.Exchange(req, "204.236.145.236:53")
		if err != nil {
			log.Printf("Req failed %v\n", err)
		} else {
			endTime := time.Now()
			latency := endTime.Sub(startTime).Microseconds()
			latencies = append(latencies, int(latency))
			sumLatencies += int(latency)
			// log.Printf("Time elapsed: %d ms\n", latency/1000)
		}
	}
	log.Println(latencies)
	var percentiles = []int{50, 90, 99}
	if len(latencies) > 0 {
		log.Printf("Avg latency: %f ms\n", float64(sumLatencies)/float64(len(latencies))/1000)
		for _, p := range percentiles {
			log.Printf("P%d latency: %d ms", p, getPercentile(latencies, p)/1000)
		}
	}
	return latencies
}

func main() {
	measureLatency(100)
}
