package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// probeHealth backs `contact -healthcheck`: 200 -> healthy (nil), anything else or
// an unreachable endpoint -> error (non-zero exit). (issue #43 — finding #7)
func TestProbeHealth(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ok.Close()
	if err := probeHealth(ok.URL+"/healthz", 2*time.Second); err != nil {
		t.Errorf("probeHealth(200) = %v, want nil", err)
	}

	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer down.Close()
	if err := probeHealth(down.URL+"/healthz", 2*time.Second); err == nil {
		t.Error("probeHealth(503) = nil, want error")
	}

	if err := probeHealth("http://127.0.0.1:0/healthz", 500*time.Millisecond); err == nil {
		t.Error("probeHealth(unreachable) = nil, want error")
	}
}
