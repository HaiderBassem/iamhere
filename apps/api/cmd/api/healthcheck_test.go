package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthcheck(t *testing.T) {
	status := http.StatusOK
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/readyz" {
			t.Errorf("probed %s", r.URL.Path)
		}
		w.WriteHeader(status)
	}))
	defer ts.Close()
	_, port, _ := net.SplitHostPort(ts.Listener.Addr().String())

	t.Setenv("IAH_LISTEN", ":"+port) // wildcard listen address -> probes loopback
	if code := healthcheck(); code != 0 {
		t.Fatalf("healthy server: exit %d", code)
	}
	status = http.StatusServiceUnavailable
	if code := healthcheck(); code == 0 {
		t.Fatal("unready server reported healthy")
	}
	ts.Close()
	if code := healthcheck(); code == 0 {
		t.Fatal("unreachable server reported healthy")
	}
	t.Setenv("IAH_LISTEN", "garbage")
	if code := healthcheck(); code == 0 {
		t.Fatal("bad address reported healthy")
	}
}
