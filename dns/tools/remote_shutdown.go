package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
)

var cmd *exec.Cmd
var running bool

func start_dnsserver(w http.ResponseWriter, req *http.Request) {
	if running {
		return
	}
	body, err := ioutil.ReadAll(req.Body)
	if err != nil {
		log.Printf("Cannot read /start body: %v\n", err)
		http.Error(w, "Bad start body", http.StatusBadRequest)
		return
	}
	argvstr := string(body)
	argv := strings.Split(argvstr, " ")

	// cmd = exec.Command("../dns_server", "--id", "1", "--cluster", "http://127.0.0.1:12379", "--port", "12380")
	cmd = exec.Command("../dns_server", argv...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		log.Fatal(err)
	}
	running = true
	log.Print("Starting running dns_server with arguement %s\n", argvstr)
	fmt.Fprintf(w, "Starting running dns_server with argument %s\n", argvstr)
}

func kill_dnsserver(w http.ResponseWriter, req *http.Request) {
	if !running {
		return
	}
	if err := cmd.Process.Kill(); err != nil {
		log.Fatal(err)
	}
	// syscall.Kill(cmd.Process.Pid, syscall.SIGINT)
	running = false
	log.Print("Killed the running dns_server\n")
	fmt.Fprintf(w, "Killed the running dns_server\n")
}

func main() {
	running = false
	http.HandleFunc("/start", start_dnsserver)
	http.HandleFunc("/kill", kill_dnsserver)
	http.ListenAndServe(":8090", nil)
}
