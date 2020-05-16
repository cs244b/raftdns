package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
)

var cmd *exec.Cmd

func start_dnsserver(w http.ResponseWriter, req *http.Request) {
	// TODO(xyyu): read in argv and pass to binary
	// body, err := ioutil.ReadAll(req.Body)
	// if err != nil {
	// 	log.Printf("Cannot read /start body: %v\n", err)
	// 	http.Error(w, "Bad start body", http.StatusBadRequest)
	// 	return
	// }
	// argv := string(body)

	cmd = exec.Command("../dns_server", "--id", "1", "--cluster", "http://127.0.0.1:12379", "--port", "12380")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		log.Fatal(err)
	}
	log.Print("Starting running dns_server")
	fmt.Fprintf(w, "Start running dns_server")
}

func kill_dnsserver(w http.ResponseWriter, req *http.Request) {
	if err := cmd.Process.Kill(); err != nil {
		log.Fatal(err)
	}
	// syscall.Kill(cmd.Process.Pid, syscall.SIGINT)
	log.Print("Killed the running dns_server")
	fmt.Fprintf(w, "Killed the running dns_server")
}

func main() {
	http.HandleFunc("/start", start_dnsserver)
	http.HandleFunc("/kill", kill_dnsserver)
	http.ListenAndServe(":8090", nil)
}
