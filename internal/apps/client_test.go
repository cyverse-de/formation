package apps

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/cyverse-de/formation/internal/apperr"
)

func testClient(t *testing.T, handler http.HandlerFunc) (*AppsClient, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	base, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	return NewAppsClient(srv.Client(), base, nil), srv
}

func TestAppsClientRequests(t *testing.T) {
	tests := []struct {
		name     string
		call     func(c *AppsClient) error
		wantPath string
		wantUser string
		check    func(t *testing.T, q url.Values, body string)
	}{
		{
			name: "get app",
			call: func(c *AppsClient) error {
				_, err := c.GetApp(context.Background(), "de", "app-1", "alice")
				return err
			},
			wantPath: "/apps/de/app-1",
			wantUser: "alice",
		},
		{
			name: "list apps with search and pagination",
			call: func(c *AppsClient) error {
				_, err := c.ListApps(context.Background(), "alice", 50, 10, "blast")
				return err
			},
			wantPath: "/apps",
			wantUser: "alice",
			check: func(t *testing.T, q url.Values, _ string) {
				if q.Get("limit") != "50" || q.Get("offset") != "10" || q.Get("search") != "blast" {
					t.Errorf("query = %v", q)
				}
			},
		},
		{
			name:     "get analysis encodes id filter",
			call:     func(c *AppsClient) error { _, err := c.GetAnalysis(context.Background(), "an-1", "alice"); return err },
			wantPath: "/analyses",
			wantUser: "alice",
			check: func(t *testing.T, q url.Values, _ string) {
				var filters []analysisFilter
				if err := json.Unmarshal([]byte(q.Get("filter")), &filters); err != nil {
					t.Fatalf("filter not JSON: %v", err)
				}
				if len(filters) != 1 || filters[0].Field != "id" || filters[0].Value != "an-1" {
					t.Errorf("filter = %v", filters)
				}
			},
		},
		{
			name: "list analyses encodes status filter",
			call: func(c *AppsClient) error {
				_, err := c.ListAnalyses(context.Background(), "alice", "Running")
				return err
			},
			wantPath: "/analyses",
			wantUser: "alice",
			check: func(t *testing.T, q url.Values, _ string) {
				var filters []analysisFilter
				if err := json.Unmarshal([]byte(q.Get("filter")), &filters); err != nil {
					t.Fatalf("filter not JSON: %v", err)
				}
				if filters[0].Field != "status" || filters[0].Value != "Running" {
					t.Errorf("filter = %v", filters)
				}
			},
		},
		{
			name: "submit analysis posts body with user and email",
			call: func(c *AppsClient) error {
				_, err := c.SubmitAnalysis(context.Background(), map[string]any{"name": "x"}, "alice", "alice@example.org")
				return err
			},
			wantPath: "/analyses",
			wantUser: "alice",
			check: func(t *testing.T, q url.Values, body string) {
				if q.Get("email") != "alice@example.org" {
					t.Errorf("email = %q", q.Get("email"))
				}
				var m map[string]any
				if err := json.Unmarshal([]byte(body), &m); err != nil || m["name"] != "x" {
					t.Errorf("body = %q", body)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath string
			var gotQuery url.Values
			var gotBody string
			c, srv := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotQuery = r.URL.Query()
				b, _ := io.ReadAll(r.Body)
				gotBody = string(b)
				// Return minimally valid payloads for each endpoint.
				switch {
				case r.URL.Path == "/analyses" && r.Method == http.MethodGet:
					_, _ = w.Write([]byte(`{"analyses":[{"id":"an-1","status":"Running"}]}`))
				default:
					_, _ = w.Write([]byte(`{"id":"x"}`))
				}
			})
			defer srv.Close()

			if err := tc.call(c); err != nil {
				t.Fatalf("call error: %v", err)
			}
			if gotPath != tc.wantPath {
				t.Errorf("path = %q, want %q", gotPath, tc.wantPath)
			}
			if gotQuery.Get("user") != tc.wantUser {
				t.Errorf("user = %q, want %q", gotQuery.Get("user"), tc.wantUser)
			}
			if tc.check != nil {
				tc.check(t, gotQuery, gotBody)
			}
		})
	}
}

func TestGetAnalysisNotFound(t *testing.T) {
	c, srv := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"analyses":[]}`))
	})
	defer srv.Close()

	_, err := c.GetAnalysis(context.Background(), "missing", "alice")
	if !apperr.AsNotFound(err) {
		t.Fatalf("expected NotFoundError, got %v", err)
	}
}

func TestStatusError(t *testing.T) {
	c, srv := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream boom"))
	})
	defer srv.Close()

	_, err := c.GetApp(context.Background(), "de", "app-1", "alice")
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("expected StatusError, got %v", err)
	}
	if se.Code != http.StatusBadGateway || se.Service != "apps" {
		t.Errorf("got %+v", se)
	}
}
