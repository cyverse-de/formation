// Package mcp hosts formation's Model Context Protocol server at /mcp,
// exposing the apps, analyses, and data operations as MCP tools behind
// Keycloak bearer-token auth.
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

// Deps carries the shared operations and config the MCP tools delegate to.
type Deps struct {
	Apps *handlers.Apps
	Data *handlers.Data
	User *handlers.User
	Cfg  *config.Config
}

// server holds the tool handlers' shared dependencies.
type server struct {
	apps *handlers.Apps
	data *handlers.Data
	user *handlers.User
	cfg  *config.Config
}

// ToolNames lists every tool NewServer registers, in registration order. The
// landing page renders it, and TestMCPListTools keeps it in sync with the
// actual registrations.
var ToolNames = []string{
	"whoami",
	"list_apps", "launch_app_and_wait", "get_analysis_status",
	"list_running_analyses", "get_app_parameters", "stop_analysis",
	"browse_data", "create_directory", "upload_file", "set_metadata",
	"delete_data",
}

// serverInstructions is surfaced to MCP hosts in the initialize result so the
// model understands formation's scope and avoids overwhelming the context with
// large or binary reads.
const serverInstructions = "formation provides text-based access to the CyVerse Discovery Environment: " +
	"apps, analyses, and the data store. browse_data returns file contents inline, so they become part of " +
	"the conversation and consume context — prefer the offset and limit parameters to page through a file " +
	"rather than reading it whole. Only text-based formats can be retrieved; binary files (images, archives, " +
	"compiled data, and similar) cannot be read back through these tools."

// NewServer builds the MCP server with all formation tools registered.
func NewServer(d Deps) *sdk.Server {
	s := &server{apps: d.Apps, data: d.Data, user: d.User, cfg: d.Cfg}
	srv := sdk.NewServer(&sdk.Implementation{
		Name:    "formation",
		Title:   "CyVerse Formation",
		Version: "1.0.0",
	}, &sdk.ServerOptions{Instructions: serverInstructions})
	s.registerUserTools(srv)
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
