package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthcheck(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
	}))
	defer srv.Close()
	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	if err := healthcheck(context.Background(), ":"+port); err != nil {
		t.Fatalf("healthy server: %v", err)
	}
	status = http.StatusServiceUnavailable
	if err := healthcheck(context.Background(), ":"+port); err == nil {
		t.Fatal("503: want an error")
	}
	for _, addr := range []string{"8080", ":http", ":0", ":70000"} {
		if err := healthcheck(context.Background(), addr); err == nil {
			t.Errorf("addr %q: want an error", addr)
		}
	}
}
