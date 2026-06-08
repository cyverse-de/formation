package apps

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestPrepareSubmission(t *testing.T) {
	// Apps server returns an app name used for auto-naming.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"name":"BLAST Search!"}`))
	}))
	defer srv.Close()
	base, _ := url.Parse(srv.URL)
	c := NewAppsClient(srv.Client(), base, nil)

	fixedTime := time.Date(2026, 6, 8, 14, 30, 5, 0, time.UTC)

	tests := []struct {
		name      string
		in        PrepareInput
		wantEmail string
		check     func(t *testing.T, sub map[string]any)
	}{
		{
			name: "auto name, output dir, and defaults",
			in: PrepareInput{
				Submission: map[string]any{},
				SystemID:   "de", AppID: "app-1", Username: "alice",
				JWTEmail: "alice@jwt.org", OutputZone: "iplant", Now: fixedTime,
			},
			wantEmail: "alice@jwt.org",
			check: func(t *testing.T, sub map[string]any) {
				if sub["name"] != "blast-search-2026-06-08-143005" {
					t.Errorf("name = %v", sub["name"])
				}
				if sub["output_dir"] != "/iplant/home/alice/analyses/blast-search-2026-06-08-143005" {
					t.Errorf("output_dir = %v", sub["output_dir"])
				}
				if sub["debug"] != false || sub["notify"] != true {
					t.Errorf("defaults = %v", sub)
				}
				if _, ok := sub["email"]; ok {
					t.Error("email should be removed from submission")
				}
			},
		},
		{
			name: "body email and explicit name preserved",
			in: PrepareInput{
				Submission: map[string]any{"email": "real@x.org", "name": "myrun", "output_dir": "/iplant/home/alice/out"},
				SystemID:   "de", AppID: "app-1", Username: "alice", JWTEmail: "alice@jwt.org",
				OutputZone: "iplant", Now: fixedTime,
			},
			wantEmail: "real@x.org",
			check: func(t *testing.T, sub map[string]any) {
				if sub["name"] != "myrun" || sub["output_dir"] != "/iplant/home/alice/out" {
					t.Errorf("should preserve explicit values: %v", sub)
				}
			},
		},
		{
			name: "email falls back to username+suffix",
			in: PrepareInput{
				Submission: map[string]any{},
				SystemID:   "de", AppID: "app-1", Username: "alice",
				JWTEmail: "", UserSuffix: "@iplantcollaborative.org", OutputZone: "iplant", Now: fixedTime,
			},
			wantEmail: "alice@iplantcollaborative.org",
		},
		{
			name: "placeholder requirements removed",
			in: PrepareInput{
				Submission: map[string]any{
					"requirements": []any{map[string]any{
						"step_number": float64(0), "min_cpu_cores": float64(0),
						"max_cpu_cores": float64(0), "min_memory_limit": float64(0),
					}},
				},
				SystemID: "de", AppID: "app-1", Username: "alice", Now: fixedTime,
			},
			wantEmail: "alice",
			check: func(t *testing.T, sub map[string]any) {
				if _, ok := sub["requirements"]; ok {
					t.Error("placeholder requirements should be removed")
				}
			},
		},
		{
			name: "real requirements preserved",
			in: PrepareInput{
				Submission: map[string]any{
					"requirements": []any{map[string]any{
						"step_number": float64(0), "min_cpu_cores": float64(2),
						"max_cpu_cores": float64(4), "min_memory_limit": float64(1024),
					}},
				},
				SystemID: "de", AppID: "app-1", Username: "alice", Now: fixedTime,
			},
			wantEmail: "alice",
			check: func(t *testing.T, sub map[string]any) {
				if _, ok := sub["requirements"]; !ok {
					t.Error("real requirements should be preserved")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sub, email, err := c.PrepareSubmission(context.Background(), tc.in)
			if err != nil {
				t.Fatalf("PrepareSubmission error: %v", err)
			}
			if email != tc.wantEmail {
				t.Errorf("email = %q, want %q", email, tc.wantEmail)
			}
			if sub["app_id"] != tc.in.AppID {
				t.Errorf("app_id = %v", sub["app_id"])
			}
			if tc.check != nil {
				tc.check(t, sub)
			}
		})
	}
}
