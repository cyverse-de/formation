// Package terraintest provides a fake terrain server for tests. Every request
// is recorded (including its Authorization header) so tests can assert that
// formation forwards the caller's bearer token, and responses come from a
// per-test respond function.
package terraintest

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// Call records one request received by the fake terrain.
type Call struct {
	Method        string
	Path          string
	Query         url.Values
	Body          map[string]any
	Authorization string
}

// BearerToken returns the bearer token from the recorded Authorization header.
func (c Call) BearerToken() string {
	return strings.TrimPrefix(c.Authorization, "Bearer ")
}

// Server is a fake terrain instance backed by a respond function.
type Server struct {
	server *httptest.Server

	mu    sync.Mutex
	calls []Call
}

// New starts a fake terrain whose responses come from respond; a nil respond
// answers every request with a 500. It is shut down via t.Cleanup.
func New(t *testing.T, respond func(r *http.Request) (int, any)) *Server {
	t.Helper()

	if respond == nil {
		respond = func(*http.Request) (int, any) { return http.StatusInternalServerError, nil }
	}

	s := &Server{}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := Call{
			Method:        r.Method,
			Path:          r.URL.Path,
			Query:         r.URL.Query(),
			Authorization: r.Header.Get("Authorization"),
		}
		// Buffer the body so the respond function can re-read it (recording
		// the JSON decode must not consume multipart uploads).
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &call.Body)
		r.Body = io.NopCloser(bytes.NewReader(raw))

		s.mu.Lock()
		s.calls = append(s.calls, call)
		s.mu.Unlock()

		status, payload := respond(r)
		// A []byte payload is served raw, like terrain's file downloads.
		if data, ok := payload.([]byte); ok {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.WriteHeader(status)
			_, _ = w.Write(data)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if payload != nil {
			_ = json.NewEncoder(w).Encode(payload)
		}
	}))
	t.Cleanup(s.server.Close)
	return s
}

// URL returns the fake terrain's base URL.
func (s *Server) URL() string {
	return s.server.URL
}

// Calls returns a snapshot of the recorded requests.
func (s *Server) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	calls := make([]Call, len(s.calls))
	copy(calls, s.calls)
	return calls
}
