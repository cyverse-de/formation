package mcp

import (
	"context"
	"fmt"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cyverse-de/formation/internal/apierror"
)

// toolHandler is a tool body that returns text or an error; wrapTool adapts
// it to the SDK signature with the Python server's error wording.
type toolHandler[In any] func(ctx context.Context, req *sdk.CallToolRequest, in In) (*sdk.CallToolResult, error)

func wrapTool[In any](name string, h toolHandler[In]) sdk.ToolHandlerFor[In, any] {
	return func(ctx context.Context, req *sdk.CallToolRequest, in In) (*sdk.CallToolResult, any, error) {
		result, err := h(ctx, req, in)
		if err != nil {
			// The SDK renders this as an isError text result for the model.
			//nolint:staticcheck // capitalized to match the Python MCP server's error text
			return nil, nil, fmt.Errorf("Error executing %s: %s", name, err)
		}
		return result, nil, nil
	}
}

type listAppsInput struct {
	Name   string `json:"name,omitempty" jsonschema:"Filter apps by name (case-insensitive partial match)"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum number of apps to return (default: 10)"`
	Offset int    `json:"offset,omitempty" jsonschema:"Number of apps to skip for pagination"`
}

type launchAppInput struct {
	AppID          string         `json:"app_id" jsonschema:"UUID of the app to launch"`
	SystemID       string         `json:"system_id,omitempty" jsonschema:"System identifier (default: 'de')"`
	Name           string         `json:"name,omitempty" jsonschema:"Custom name for the analysis (optional)"`
	MaxWait        int            `json:"max_wait,omitempty" jsonschema:"Maximum seconds to wait for app to be ready (default: 300)"`
	Config         map[string]any `json:"config,omitempty" jsonschema:"Configuration parameters required by the app"`
	OverallJobType string         `json:"overall_job_type,omitempty" jsonschema:"Job type from a previous list_apps call; if provided, avoids an extra API request. Values: 'Interactive', 'DE', 'OSG', 'Tapis'"`
}

type analysisStatusInput struct {
	AnalysisID string `json:"analysis_id" jsonschema:"UUID of the analysis"`
}

type listRunningInput struct{}

type appParametersInput struct {
	AppID    string `json:"app_id" jsonschema:"UUID of the app"`
	SystemID string `json:"system_id,omitempty" jsonschema:"System identifier (default: 'de')"`
}

type stopAnalysisInput struct {
	AnalysisID  string `json:"analysis_id" jsonschema:"UUID of the analysis to stop"`
	SaveOutputs *bool  `json:"save_outputs,omitempty" jsonschema:"Whether to save outputs before stopping (default: true)"`
}

func (s *server) registerAppsTools(srv *sdk.Server) {
	sdk.AddTool(srv, &sdk.Tool{
		Name:        "list_apps",
		Description: "List available interactive VICE applications with optional filtering by name",
	}, wrapTool("list_apps", s.listApps))

	sdk.AddTool(srv, &sdk.Tool{
		Name: "launch_app_and_wait",
		Description: "Launch an interactive application and wait for it to become ready. " +
			"Returns the URL when ready. If the app requires parameters, " +
			"will return a list of required parameters to collect from the user.",
	}, wrapTool("launch_app_and_wait", s.launchAppAndWait))

	sdk.AddTool(srv, &sdk.Tool{
		Name:        "get_analysis_status",
		Description: "Check the current status of a running analysis",
	}, wrapTool("get_analysis_status", s.getAnalysisStatus))

	sdk.AddTool(srv, &sdk.Tool{
		Name:        "list_running_analyses",
		Description: "List all currently running analyses for the authenticated user",
	}, wrapTool("list_running_analyses", s.listRunningAnalyses))

	sdk.AddTool(srv, &sdk.Tool{
		Name: "get_app_parameters",
		Description: "Get the parameters for a specific app, including required parameters, " +
			"parameter types, and default values. Use this to check what parameters " +
			"an app needs before launching it.",
	}, wrapTool("get_app_parameters", s.getAppParameters))

	sdk.AddTool(srv, &sdk.Tool{
		Name:        "stop_analysis",
		Description: "Stop a running analysis, optionally saving outputs",
	}, wrapTool("stop_analysis", s.stopAnalysis))
}

func (s *server) listApps(ctx context.Context, req *sdk.CallToolRequest, in listAppsInput) (*sdk.CallToolResult, error) {
	caller, err := requestCaller(req)
	if err != nil {
		return nil, err
	}

	limit := in.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 1000 {
		return nil, apierror.NewValidation("Limit must be between 1 and 1000", "limit")
	}
	offset := max(in.Offset, 0)

	result, err := s.apps.ListAppsPage(ctx, caller, in.Name, limit, offset)
	if err != nil {
		return nil, err
	}
	return textResult(formatAppsList(result)), nil
}

func (s *server) launchAppAndWait(ctx context.Context, req *sdk.CallToolRequest, in launchAppInput) (*sdk.CallToolResult, error) {
	caller, err := requestCaller(req)
	if err != nil {
		return nil, err
	}

	systemID := in.SystemID
	if systemID == "" {
		systemID = "de"
	}
	maxWait := 300 * time.Second
	if in.MaxWait > 0 {
		maxWait = time.Duration(in.MaxWait) * time.Second
	}
	maxWait = min(maxWait, s.cfg.MCPLaunchMaxWait)

	// Without a caller-provided job type, fetch the app's parameters to check
	// for missing required values before launching anything.
	jobType := in.OverallJobType
	if jobType == "" {
		parameters, err := s.apps.AppParameters(ctx, caller, systemID, in.AppID)
		if err != nil {
			return nil, err
		}
		if missing := missingRequiredParams(parameters, in.Config); len(missing) > 0 {
			return textResult(formatMissingParams(missing)), nil
		}
		jobType = strOr(parameters, "overall_job_type", "")
	}

	submission := map[string]any{}
	if in.Name != "" {
		submission["name"] = in.Name
	}
	if in.Config != nil {
		submission["config"] = in.Config
	}

	launched, err := s.apps.LaunchAnalysis(ctx, caller, systemID, in.AppID, s.cfg.OutputZone, submission)
	if err != nil {
		return nil, err
	}
	analysisID := strOr(launched, "analysis_id", "")

	// Some apps lack overall_job_type in their metadata, but VICE launches
	// always come back with a URL, so the URL is the more reliable signal
	// that there's something to wait for.
	_, hasURL := launched["url"]
	if jobType != "Interactive" && !hasURL {
		return textResult(formatBatchLaunch(analysisID, jobType)), nil
	}

	// Poll the analysis status until the VICE URL responds or maxWait passes.
	start := time.Now()
	for time.Since(start) < maxWait {
		status, err := s.apps.AnalysisStatus(ctx, caller, analysisID)
		if err != nil {
			// The analysis is already launched; a failed status poll (e.g.
			// the caller's token expiring mid-wait) must not read as a
			// launch failure.
			return textResult(formatLaunchPollFailure(analysisID, err)), nil
		}
		if ready, _ := status["url_ready"].(bool); ready {
			return textResult(formatInteractiveLaunch(analysisID, status, int(time.Since(start).Seconds()))), nil
		}

		notifyProgress(ctx, req,
			fmt.Sprintf("Analysis %s status: %s", analysisID, strOr(status, "status", "Unknown")),
			time.Since(start).Seconds(), maxWait.Seconds())

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(launchPollInterval):
		}
	}

	//nolint:staticcheck // capitalized to match the Python MCP server's timeout text
	return nil, fmt.Errorf("Analysis %s not ready after %ds (max: %ds)",
		analysisID, int(time.Since(start).Seconds()), int(maxWait.Seconds()))
}

// notifyProgress sends a progress notification when the client asked for one;
// it also keeps the response stream alive through gateways during long polls.
func notifyProgress(ctx context.Context, req *sdk.CallToolRequest, message string, progress, total float64) {
	token := req.Params.GetProgressToken()
	if token == nil {
		return
	}
	// Progress is best-effort; a failed notification must not fail the launch.
	_ = req.Session.NotifyProgress(ctx, &sdk.ProgressNotificationParams{
		ProgressToken: token,
		Progress:      progress,
		Total:         total,
		Message:       message,
	})
}

func (s *server) getAnalysisStatus(ctx context.Context, req *sdk.CallToolRequest, in analysisStatusInput) (*sdk.CallToolResult, error) {
	caller, err := requestCaller(req)
	if err != nil {
		return nil, err
	}
	status, err := s.apps.AnalysisStatus(ctx, caller, in.AnalysisID)
	if err != nil {
		return nil, err
	}
	return textResult(formatAnalysisStatus(status)), nil
}

func (s *server) listRunningAnalyses(ctx context.Context, req *sdk.CallToolRequest, _ listRunningInput) (*sdk.CallToolResult, error) {
	caller, err := requestCaller(req)
	if err != nil {
		return nil, err
	}
	analyses, err := s.apps.AnalysesForUser(ctx, caller, "Running")
	if err != nil {
		return nil, err
	}
	return textResult(formatRunningAnalyses(analyses)), nil
}

func (s *server) getAppParameters(ctx context.Context, req *sdk.CallToolRequest, in appParametersInput) (*sdk.CallToolResult, error) {
	caller, err := requestCaller(req)
	if err != nil {
		return nil, err
	}
	systemID := in.SystemID
	if systemID == "" {
		systemID = "de"
	}
	parameters, err := s.apps.AppParameters(ctx, caller, systemID, in.AppID)
	if err != nil {
		return nil, err
	}
	return textResult(formatAppParameters(in.AppID, parameters)), nil
}

func (s *server) stopAnalysis(ctx context.Context, req *sdk.CallToolRequest, in stopAnalysisInput) (*sdk.CallToolResult, error) {
	caller, err := requestCaller(req)
	if err != nil {
		return nil, err
	}

	save := in.SaveOutputs == nil || *in.SaveOutputs
	operation := "exit"
	saveMsg := "without"
	if save {
		operation = "save_and_exit"
		saveMsg = "with"
	}

	if _, err := s.apps.ControlAnalysis(ctx, caller, in.AnalysisID, operation); err != nil {
		return nil, err
	}
	return textResult(fmt.Sprintf("Analysis stopped %s saving outputs", saveMsg)), nil
}
