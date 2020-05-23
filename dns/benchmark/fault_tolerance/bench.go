package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/miekg/dns"
)

var dnsIPs []string
var dbSize *int

// choose dns server uniformly at random
func getDNSServerIP() string {
	return dnsIPs[rand.Intn(len(dnsIPs))]
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

func populateDNSStore(numRR int) {
	i := 0
	for a := 0; a < 256; a++ {
		for b := 0; b < 256; b++ {
			for c := 0; c < 256; c++ {
				for d := 0; d < 256; d++ {
					domain := fmt.Sprintf("example-%d.com.", i)
					writeRR := fmt.Sprintf("%s IN A %d.%d.%d.%d", domain, a, b, c, d)
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

// pure read request
func throughputClient(idx int, done chan string, precision float64) {
	client := new(dns.Client)
	numResponse := 0
	ticker := time.NewTicker(time.Duration(precision*1000) * time.Millisecond)
	quit := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				fmt.Printf("Read throughput: %.2f responses/sec\n", float64(numResponse)/precision)
				numResponse = 0
			case <-quit:
				ticker.Stop()
				return
			}
		}
	}()

	for {
		select {
		case <-done:
			close(quit)
			return
		default:
			req := new(dns.Msg)
			domain := fmt.Sprintf("example-%d.com.", rand.Intn(*dbSize))
			req.SetQuestion(domain, dns.TypeA)
			req.RecursionDesired = false
			_, _, err := client.Exchange(req, getDNSServerIP()+":53")
			if err != nil {
				log.Printf("Req failed %v\n", err)
			} else {
				numResponse++
			}
		}
	}
}

// pure read request
func measureTp(duration int, numClient int, precision float64) {
	done := make(chan string)
	for i := 0; i < numClient; i++ {
		go throughputClient(i, done, precision)
	}
	time.Sleep(time.Duration(duration) * time.Second)
	close(done)
}

func main() {

	dnsIP := flag.String("ip", "127.0.0.1", "comma separated ip addresses to send dns query to")
	benchmarkType := flag.String("type", "t", "benchmark type: t for throughput, p for populate")
	// client send at its full speed. Change client number to adjust sending rate
	numClient := flag.Int("client", 1, "number of client threads, each sending request at its full speed")
	// readRatio := flag.Float64("readratio", 1, "[throughput] read request ratio in [0,1]")
	dbSize = flag.Int("size", 100, "number of RR in dns server")
	duration := flag.Int("duration", 30, "[throughput] number of seconds to run")
	precision := flag.Float64("precision", 1, "measure throughout every x second")
	// kill := flag.Bool("kill", false, "whether this program kill or restart the node")
	// killtime1 := flag.Int("killtime1", 10, "when to kill the first server node")
	// killtime2 := flag.Int("killtime2", 20, "when to kill the second server node")
	// restarttime1 := flag.Int("restarttime1", 25, "when to restart the first server node")
	// TODO: when to kill server and when to start
	// TODO: who to kill server

	flag.Parse()
	dnsIPs = strings.Split(*dnsIP, ",")

	switch *benchmarkType {
	case "p":
		populateDNSStore(*dbSize)
	default:
		log.Println("Wrong benchmark type")

	case "t":
		measureTp(*duration, *numClient, *precision)
	}

}
