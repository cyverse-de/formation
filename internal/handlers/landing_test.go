package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/cyverse-de/formation/internal/config"
)

func TestLanding(t *testing.T) {
	tests := []struct {
		name       string
		cfg        config.Config
		wantBody   []string
		wantAbsent []string
	}{
		{
			name: "mcp enabled with public base url",
			cfg:  config.Config{MCPEnabled: true, PublicBaseURL: "https://de.example.org/formation"},
			wantBody: []string{
				"https://de.example.org/formation/mcp",
				"https://de.example.org/formation/docs",
				"claude mcp add --transport http formation",
				`"serverUrl"`,
				"mcp_config.json",
				"launch_app_and_wait",
			},
		},
		{
			name:       "mcp disabled with path prefix",
			cfg:        config.Config{PathPrefix: "/formation/"},
			wantBody:   []string{`href="/formation/docs"`},
			wantAbsent: []string{"MCP Server", "mcp_config.json"},
		},
		{
			name:       "mcp disabled without prefix",
			cfg:        config.Config{},
			wantBody:   []string{`href="/docs"`},
			wantAbsent: []string{"MCP Server"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, err := Landing(&tt.cfg)
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
