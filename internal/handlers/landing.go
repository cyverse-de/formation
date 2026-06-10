// Package handlers contains formation's HTTP handlers.
package handlers

import (
	_ "embed"
	"html/template"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/cyverse-de/formation/internal/config"
)

//go:embed landing.html
var landingHTML string

var landingTemplate = template.Must(template.New("landing").Parse(landingHTML))

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
func Landing(cfg *config.Config) (echo.HandlerFunc, error) {
	base := cfg.PublicBaseURL
	if base == "" {
		base = strings.TrimSuffix(cfg.PathPrefix, "/")
	}
	var page strings.Builder
	err := landingTemplate.Execute(&page, struct {
		MCPEnabled      bool
		MCPURL, DocsURL string
	}{cfg.MCPEnabled, base + "/mcp", base + "/docs"})
	if err != nil {
		return nil, err
	}
	rendered := page.String()
	return func(c echo.Context) error {
		return c.HTML(http.StatusOK, rendered)
	}, nil
}
