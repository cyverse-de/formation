package handlers

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/cyverse-de/formation/internal/config"
)

func TestLanding(t *testing.T) {
	tools := []string{"list_apps", "delete_data"}
	tests := []struct {
		name       string
		cfg        config.Config
		wantBody   []string
		wantAbsent []string
	}{
		{
			name: "public base url",
			cfg:  config.Config{PublicBaseURL: "https://de.example.org/formation"},
			wantBody: []string{
				"https://de.example.org/formation/mcp",
				"claude mcp add --transport http formation",
				`"serverUrl"`,
				"mcp_config.json",
				"list_apps",
				"delete_data",
			},
			wantAbsent: []string{"/docs", "REST API"},
		},
		{
			name:     "path prefix fallback",
			cfg:      config.Config{PathPrefix: "/formation"},
			wantBody: []string{"/formation/mcp"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, err := Landing(&tt.cfg, tools)
			if err != nil {
				t.Fatalf("Landing() error = %v", err)
			}
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			if err := handler(echo.New().NewContext(req, rec)); err != nil {
				t.Fatalf("handler error = %v", err)
			}
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			if ct := rec.Header().Get(echo.HeaderContentType); !strings.HasPrefix(ct, echo.MIMETextHTML) {
				t.Errorf("Content-Type = %q, want %q", ct, echo.MIMETextHTML)
			}
			body := rec.Body.String()
			if cl := rec.Header().Get(echo.HeaderContentLength); cl != strconv.Itoa(len(body)) {
				t.Errorf("Content-Length = %q, want %d", cl, len(body))
			}
			for _, want := range tt.wantBody {
				if !strings.Contains(body, want) {
					t.Errorf("body missing %q", want)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(body, absent) {
					t.Errorf("body unexpectedly contains %q", absent)
				}
			}
		})
	}
}
