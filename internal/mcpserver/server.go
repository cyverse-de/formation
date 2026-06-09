// Package mcpserver wires Formation's MCP tools to the DE backend clients. Tool
// handlers read the authenticated identity from the request context and never
// re-derive the service-account mapping.
package mcpserver

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cyverse-de/formation/internal/apps"
	"github.com/cyverse-de/formation/internal/datastore"
)

// AppsAPI is the subset of the apps service client used by the tools.
type AppsAPI interface {
	GetApp(ctx context.Context, systemID, appID, username string) (*apps.App, error)
	ListApps(ctx context.Context, username string, limit, offset int, search string) (*apps.AppList, error)
	SubmitAnalysis(ctx context.Context, submission map[string]any, username, email string) (*apps.SubmitResult, error)
	GetAnalysis(ctx context.Context, analysisID, username string) (*apps.Analysis, error)
	ListAnalyses(ctx context.Context, username, status string) ([]apps.Analysis, error)
	PrepareSubmission(ctx context.Context, in apps.PrepareInput) (map[string]any, string, error)
}

// ExposerAPI is the subset of the app-exposer client used by the tools.
type ExposerAPI interface {
	SaveAndExit(ctx context.Context, analysisID string) error
	ExitWithoutSave(ctx context.Context, analysisID string) error
	ExtendTimeLimit(ctx context.Context, analysisID string) (*apps.TimeLimit, error)
}

// VICEAPI resolves VICE subdomains and probes URL readiness.
type VICEAPI interface {
	ResolveSubdomain(ctx context.Context, analysisID string) string
	CheckURLReady(ctx context.Context, rawurl string) (bool, apps.ProbeDetails)
	URLFor(subdomain string) string
}

// DataStoreAPI is the subset of the iRODS datastore used by the tools.
type DataStoreAPI interface {
	Browse(username, path string, offset, limit int, includeMetadata bool, avuDelimiter string) (*datastore.BrowseResult, error)
	CreateDirectory(username, path string, metadata []datastore.AVU) (*datastore.WriteResult, error)
	UploadFile(username, path string, content []byte, metadata []datastore.AVU, replaceMetadata bool) (*datastore.WriteResult, error)
	SetMetadata(username, path string, metadata []datastore.AVU, replace bool) (*datastore.WriteResult, error)
	Delete(username, path string, recurse, dryRun bool) (*datastore.DeleteResult, error)
}

// Deps holds the dependencies and settings for the tool handlers.
type Deps struct {
	Apps       AppsAPI
	Exposer    ExposerAPI
	Vice       VICEAPI
	Data       DataStoreAPI
	UserSuffix string
	OutputZone string
	Version    string

	// Now is injectable for testing; defaults to time.Now.
	Now func() time.Time
}

// New builds an MCP server with all of Formation's tools registered.
func New(d *Deps) *mcp.Server {
	if d.Now == nil {
		d.Now = time.Now
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "formation", Version: d.Version}, nil)

	mcp.AddTool(s, &mcp.Tool{Name: "list_apps", Description: "List Discovery Environment apps available to the user, with optional filters."}, d.listApps)
	mcp.AddTool(s, &mcp.Tool{Name: "get_app_parameters", Description: "Get the parameter group definitions needed to launch an app."}, d.getAppParameters)
	mcp.AddTool(s, &mcp.Tool{Name: "launch_app_and_wait", Description: "Launch an app and, for interactive apps, wait for and return its access URL."}, d.launchAppAndWait)
	mcp.AddTool(s, &mcp.Tool{Name: "get_analysis_status", Description: "Get the status of an analysis and, for interactive apps, whether its URL is ready."}, d.getAnalysisStatus)
	mcp.AddTool(s, &mcp.Tool{Name: "list_running_analyses", Description: "List the user's analyses filtered by status (default Running)."}, d.listRunningAnalyses)
	mcp.AddTool(s, &mcp.Tool{Name: "stop_analysis", Description: "Control a running analysis: save_and_exit, exit, or extend_time."}, d.stopAnalysis)
	mcp.AddTool(s, &mcp.Tool{Name: "open_in_browser", Description: "Resolve the access URL for an interactive analysis."}, d.openInBrowser)

	mcp.AddTool(s, &mcp.Tool{Name: "browse_data", Description: "List a directory or read a file in the iRODS data store."}, d.browseData)
	mcp.AddTool(s, &mcp.Tool{Name: "create_directory", Description: "Create a directory in the iRODS data store."}, d.createDirectory)
	mcp.AddTool(s, &mcp.Tool{Name: "upload_file", Description: "Create or overwrite a file in the iRODS data store."}, d.uploadFile)
	mcp.AddTool(s, &mcp.Tool{Name: "delete_data", Description: "Delete a file or directory from the iRODS data store."}, d.deleteData)
	mcp.AddTool(s, &mcp.Tool{Name: "set_metadata", Description: "Set AVU metadata on an iRODS file or directory."}, d.setMetadata)

	return s
}
