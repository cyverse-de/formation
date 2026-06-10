// Package handlers contains formation's HTTP handlers.
package handlers

import (
	"bytes"
	_ "embed"
	"html/template"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/cyverse-de/formation/internal/config"
)

//go:embed landing.html
var landingHTML string

// Landing builds the unauthenticated landing page handler. The page explains
// how to point MCP clients at /mcp and links to the Swagger UI; the Kubernetes
// probes that hit "/" only check the status code, so HTML keeps them passing.
//
// @Summary Landing page
// @Description Static page describing how to connect AI agents to the hosted MCP server, with a link to the Swagger UI.
// @Tags Status
// @Produce html
// @Success 200 {string} string "HTML landing page"
// @Router / [get]
func Landing(cfg *config.Config, mcpTools []string) (echo.HandlerFunc, error) {
	tmpl, err := template.New("landing").Parse(landingHTML)
	if err != nil {
		return nil, err
	}
	base := cfg.PublicBaseURL
	if base == "" {
		base = cfg.PathPrefix
	}
	var page bytes.Buffer
	err = tmpl.Execute(&page, struct {
		MCPEnabled      bool
		MCPURL, DocsURL string
		Tools           []string
	}{cfg.MCPEnabled, base + "/mcp", base + "/docs", mcpTools})
	if err != nil {
		return nil, err
	}
	rendered := page.Bytes()
	// Pre-set Content-Length: the body exceeds net/http's pre-chunking
	// buffer, and the probes hitting "/" shouldn't pay for chunked framing.
	length := strconv.Itoa(len(rendered))
	return func(c echo.Context) error {
		c.Response().Header().Set(echo.HeaderContentLength, length)
		return c.HTMLBlob(http.StatusOK, rendered)
	}, nil
}
