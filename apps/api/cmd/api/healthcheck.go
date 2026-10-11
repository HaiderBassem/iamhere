package main

import (
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// healthcheck probes /readyz on the local listener and returns a process exit code.
func healthcheck() int {
	addr := os.Getenv("IAH_LISTEN")
	if addr == "" {
		addr = ":8080"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 1
	}
	if host == "" || strings.Trim(host, "0:.") == "" { // wildcard listen address
		host = "127.0.0.1"
	}
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + net.JoinHostPort(host, port) + "/readyz")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
