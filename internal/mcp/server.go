// Package mcp hosts formation's Model Context Protocol server at /mcp,
// re-exposing the apps, analyses, and data operations as MCP tools behind
// the same Keycloak bearer-token auth as the REST endpoints.
package mcp

import (
	"net/http"
	"time"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cyverse-de/formation/internal/auth"
	"github.com/cyverse-de/formation/internal/config"
	"github.com/cyverse-de/formation/internal/handlers"
)

// launchPollInterval is the delay between status polls in launch_app_and_wait;
// a variable so tests can shorten it.
var launchPollInterval = 5 * time.Second

// Deps carries the shared handlers and config the MCP tools delegate to.
type Deps struct {
	Apps *handlers.Apps
	Data *handlers.Data
	Cfg  *config.Config
}

// server holds the tool handlers' shared dependencies.
type server struct {
	apps *handlers.Apps
	data *handlers.Data
	cfg  *config.Config
}

// ToolNames lists every tool NewServer registers, in registration order. The
// landing page renders it, and TestMCPListTools keeps it in sync with the
// actual registrations.
var ToolNames = []string{
	"list_apps", "launch_app_and_wait", "get_analysis_status",
	"list_running_analyses", "get_app_parameters", "stop_analysis",
	"browse_data", "create_directory", "upload_file", "set_metadata",
	"delete_data",
}

// NewServer builds the MCP server with all formation tools registered.
func NewServer(d Deps) *sdk.Server {
	s := &server{apps: d.Apps, data: d.Data, cfg: d.Cfg}
	srv := sdk.NewServer(&sdk.Implementation{
		Name:    "formation",
		Title:   "CyVerse Formation",
		Version: "1.0.0",
	}, nil)
	s.registerAppsTools(srv)
	s.registerDataTools(srv)
	return srv
}

// Handler returns the /mcp HTTP handler: Keycloak bearer-token verification
// wrapping the streamable HTTP transport. The transport runs stateless so
// any replica can serve any request without session affinity.
func Handler(d Deps, v *auth.Verifier) http.Handler {
	srv := NewServer(d)
	streamable := sdk.NewStreamableHTTPHandler(
		func(*http.Request) *sdk.Server { return srv },
		&sdk.StreamableHTTPOptions{Stateless: true},
	)
	requireToken := sdkauth.RequireBearerToken(tokenVerifier(v), &sdkauth.RequireBearerTokenOptions{
		ResourceMetadataURL: d.Cfg.PublicBaseURL + "/.well-known/oauth-protected-resource/mcp",
	})
	return requireToken(streamable)
}

// textResult wraps plain text as a successful tool result.
func textResult(text string) *sdk.CallToolResult {
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: text}}}
}
