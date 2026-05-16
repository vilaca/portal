// alertmanager-receiver is a minimal HTTP server that records every POST to
// /api/v2/alerts and exposes the captured bodies at /captured. It is used by
// the Portal e2e suite to verify that the AlertManager sink delivers the
// expected JSON shape inside a real kind cluster.
//
// Intentionally has zero dependencies beyond the Go standard library so the
// container image stays small (~6 MB) and the build is fast.
package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
)

func main() {
	addr := ":9093"
	if a := os.Getenv("LISTEN"); a != "" {
		addr = a
	}
	srv := &server{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/alerts", srv.handleAlerts)
	mux.HandleFunc("/captured", srv.handleCaptured)
	mux.HandleFunc("/reset", srv.handleReset)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	log.Printf("alertmanager-receiver listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

type server struct {
	mu       sync.Mutex
	captured []json.RawMessage
}

// handleAlerts mimics AlertManager's POST /api/v2/alerts. Records the body,
// returns 202 Accepted. Bad JSON is still recorded — the test asserts on
// shape and the e2e suite needs to see exactly what Portal sent.
func (s *server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(r.Body)
	defer r.Body.Close()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.captured = append(s.captured, json.RawMessage(body))
	s.mu.Unlock()
	w.WriteHeader(http.StatusAccepted)
}

// handleCaptured returns the recorded bodies as a JSON array.
func (s *server) handleCaptured(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	out := make([]json.RawMessage, len(s.captured))
	copy(out, s.captured)
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// handleReset clears the capture buffer (used between test cases).
func (s *server) handleReset(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	s.captured = nil
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}
