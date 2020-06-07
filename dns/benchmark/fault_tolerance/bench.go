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
var quiet *bool
var hashIP *string
var useHash *bool

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
func throughputClient(idx int, done chan string, interval float64, tpC chan float64, serverip string) {
	client := new(dns.Client)
	numResponse := 0
	ticker := time.NewTicker(time.Duration(interval*1000) * time.Millisecond)
	quit := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				if !*quiet {
					fmt.Printf("Read throughput from client %d : %.2f responses/sec\n", idx, float64(numResponse)/interval)
				}
				tpC <- float64(numResponse) / interval
				numResponse = 0
			case <-quit:
				ticker.Stop()
				close(tpC)
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
			_, _, err := client.Exchange(req, serverip+":53")
			if err != nil {
				log.Printf("Req failed %v\n", err)
			} else {
				numResponse++
			}
			// for benchmark
			if !*useHash {
				time.Sleep(700 * time.Microsecond)
			}
			// for benchmark
		}
	}
}

// pure read request
func measureTp(duration int, numClient int, interval float64) {
	done := make(chan string)
	tpC := make(chan float64)
	for i := 0; i < numClient; i++ {
		if *useHash {
			go throughputClient(i, done, interval, tpC, *hashIP)
		} else {
			go throughputClient(i, done, interval, tpC, getDNSServerIP())
		}
	}

	ticker := time.NewTicker(time.Duration(duration) * time.Second)
	go func() {
		<-ticker.C
		close(done)
		fmt.Printf("Test ended.\n")
	}()
	tp := 0.0
	i := 0
	for tpVal := range tpC {
		tp += tpVal
		i++
		if i == numClient {
			fmt.Printf("Read throughput from all clients: %.2f responses/sec\n", tp)
			tp = 0
			i = 0
		}
	}
}

func main() {
	rand.Seed(time.Now().Unix())
	dnsIP := flag.String("ip", "127.0.0.1", "comma separated ip addresses to send dns query to")
	benchmarkType := flag.String("type", "t", "benchmark type: t for throughput, p for populate")
	// client send at its full speed. Change client number to adjust sending rate
	numClient := flag.Int("client", 1, "number of client threads, each sending request at its full speed")
	dbSize = flag.Int("size", 100, "number of RR in dns server")
	duration := flag.Int("duration", 30, "[throughput] number of seconds to run")
	interval := flag.Float64("interval", 1, "measure throughout every x second")
	quiet = flag.Bool("quiet", false, "whether to print throughput of each client thread")

	// consistent hashing test support
	useHash = flag.Bool("useHash", false, "whether to query the hashing server")
	hashIP = flag.String("hashIp", "127.0.0.1", "hashing server ip address")

	flag.Parse()
	dnsIPs = strings.Split(*dnsIP, ",")

	switch *benchmarkType {
	case "p":
		populateDNSStore(*dbSize)
	default:
		log.Println("Wrong benchmark type")

	case "t":
		measureTp(*duration, *numClient, *interval)
	}

}
