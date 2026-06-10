package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
)

func TestStripPathPrefix(t *testing.T) {
	e := echo.New()
	e.Pre(StripPathPrefix("/formation"))
	e.GET("/", func(c echo.Context) error { return c.String(200, "root") })
	e.GET("/apps", func(c echo.Context) error { return c.String(200, "apps") })
	e.GET("/data/*", func(c echo.Context) error { return c.String(200, "data:"+c.Param("*")) })

	tests := []struct {
		name     string
		target   string
		wantCode int
		wantBody string
	}{
		{"prefixed root", "/formation", 200, "root"},
		{"prefixed root with slash", "/formation/", 200, "root"},
		{"prefixed route", "/formation/apps", 200, "apps"},
		{"unprefixed route still works", "/apps", 200, "apps"},
		{"prefixed wildcard", "/formation/data/iplant/home/alice", 200, "data:iplant/home/alice"},
		// %20 round-trips, so only URL.Path is set (decoded by net/http).
		{"prefixed wildcard with space", "/formation/data/iplant/a%20b", 200, "data:iplant/a b"},
		// %2F does not round-trip, so RawPath is set and echo routes on it.
		{"prefixed wildcard with encoded slash", "/formation/data/iplant/a%2Fb", 200, "data:iplant/a%2Fb"},
		{"similar but different prefix", "/formationx", 404, ""},
		{"unknown prefixed route", "/formation/nope", 404, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.target, nil)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.wantCode, rec.Body)
			}
			if tt.wantBody != "" && rec.Body.String() != tt.wantBody {
				t.Errorf("body = %q, want %q", rec.Body.String(), tt.wantBody)
			}
		})
	}

	t.Run("empty prefix is a no-op", func(t *testing.T) {
		plain := echo.New()
		plain.Pre(StripPathPrefix(""))
		plain.GET("/apps", func(c echo.Context) error { return c.String(200, "apps") })
		req := httptest.NewRequest(http.MethodGet, "/apps", nil)
		rec := httptest.NewRecorder()
		plain.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("status = %d", rec.Code)
		}
	})
}
