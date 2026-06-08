package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cyverse-de/formation/internal/apperr"
	"github.com/cyverse-de/formation/internal/apps"
)

func (d *Deps) listApps(ctx context.Context, req *mcp.CallToolRequest, in ListAppsIn) (*mcp.CallToolResult, ListAppsOut, error) {
	id, err := caller(ctx, req)
	if err != nil {
		return nil, ListAppsOut{}, err
	}

	limit := in.Limit
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 1000 {
		return nil, ListAppsOut{}, apperr.Validation("limit", "Limit must be between 1 and 1000")
	}
	if in.Offset < 0 {
		return nil, ListAppsOut{}, apperr.Validation("offset", "Offset must be non-negative")
	}

	// Fetch a large page and filter/paginate client-side, mirroring the original.
	list, err := d.Apps.ListApps(ctx, id.DownstreamUsername, 1000, 0, in.Name)
	if err != nil {
		return nil, ListAppsOut{}, err
	}

	filtered, err := apps.FilterApps(list.Apps, apps.ListFilter{
		Description:     in.Description,
		Integrator:      in.Integrator,
		IntegrationDate: in.IntegrationDate,
		EditedDate:      in.EditedDate,
		JobType:         in.JobType,
		UserSuffix:      d.UserSuffix,
	})
	if err != nil {
		return nil, ListAppsOut{}, err
	}

	total := len(filtered)
	page := paginate(filtered, in.Offset, limit)

	out := ListAppsOut{Total: total, Apps: make([]AppOut, 0, len(page))}
	for _, a := range page {
		out.Apps = append(out.Apps, AppOut{
			ID:                 a.ID,
			Name:               a.Name,
			Description:        a.Description,
			Version:            a.Version,
			IntegratorUsername: apps.StripUserSuffix(a.IntegratorName, d.UserSuffix),
			IntegrationDate:    a.IntegrationDate,
			EditedDate:         a.EditedDate,
			SystemID:           a.SystemID,
			OverallJobType:     a.OverallJobType,
		})
	}
	return nil, out, nil
}

func (d *Deps) getAppParameters(ctx context.Context, req *mcp.CallToolRequest, in GetAppParametersIn) (*mcp.CallToolResult, GetAppParametersOut, error) {
	id, err := caller(ctx, req)
	if err != nil {
		return nil, GetAppParametersOut{}, err
	}
	if err := validateUUID(in.AppID, "app_id"); err != nil {
		return nil, GetAppParametersOut{}, err
	}
	app, err := d.Apps.GetApp(ctx, in.SystemID, in.AppID, id.DownstreamUsername)
	if err != nil {
		return nil, GetAppParametersOut{}, err
	}
	return nil, GetAppParametersOut{Groups: app.Groups, OverallJobType: app.OverallJobType}, nil
}

func (d *Deps) launchAppAndWait(ctx context.Context, req *mcp.CallToolRequest, in LaunchAppIn) (*mcp.CallToolResult, LaunchAppOut, error) {
	id, err := caller(ctx, req)
	if err != nil {
		return nil, LaunchAppOut{}, err
	}
	if err := validateUUID(in.AppID, "app_id"); err != nil {
		return nil, LaunchAppOut{}, err
	}

	outputZone := in.OutputZone
	if outputZone == "" {
		outputZone = d.OutputZone
	}

	// Service-account tokens carry a non-routable keycloak email; fall back to
	// the mapped user's address (username+suffix) for them.
	jwtEmail := id.Email
	if id.IsServiceAccount {
		jwtEmail = ""
	}

	sub, email, err := d.Apps.PrepareSubmission(ctx, apps.PrepareInput{
		Submission: in.Submission,
		SystemID:   in.SystemID,
		AppID:      in.AppID,
		Username:   id.DownstreamUsername,
		JWTEmail:   jwtEmail,
		OutputZone: outputZone,
		UserSuffix: d.UserSuffix,
		Now:        d.Now(),
	})
	if err != nil {
		return nil, LaunchAppOut{}, err
	}

	result, err := d.Apps.SubmitAnalysis(ctx, sub, id.DownstreamUsername, email)
	if err != nil {
		return nil, LaunchAppOut{}, err
	}

	name := result.Name
	if name == "" {
		if s, ok := sub["name"].(string); ok {
			name = s
		}
	}
	status := result.Status
	if status == "" {
		status = "Submitted"
	}

	out := LaunchAppOut{AnalysisID: result.ID, Name: name, Status: status}
	if result.ID != "" {
		if subdomain := d.Vice.ResolveSubdomain(ctx, result.ID); subdomain != "" {
			out.URL = d.Vice.URLFor(subdomain)
		}
	}
	return nil, out, nil
}

func (d *Deps) getAnalysisStatus(ctx context.Context, req *mcp.CallToolRequest, in AnalysisStatusIn) (*mcp.CallToolResult, AnalysisStatusOut, error) {
	id, err := caller(ctx, req)
	if err != nil {
		return nil, AnalysisStatusOut{}, err
	}
	if err := validateUUID(in.AnalysisID, "analysis_id"); err != nil {
		return nil, AnalysisStatusOut{}, err
	}
	analysis, err := d.Apps.GetAnalysis(ctx, in.AnalysisID, id.DownstreamUsername)
	if err != nil {
		return nil, AnalysisStatusOut{}, err
	}

	status := analysis.Status
	if status == "" {
		status = "Unknown"
	}
	out := AnalysisStatusOut{AnalysisID: in.AnalysisID, Status: status}

	if subdomain := d.Vice.ResolveSubdomain(ctx, in.AnalysisID); subdomain != "" {
		url := d.Vice.URLFor(subdomain)
		out.URL = url
		ready, details := d.Vice.CheckURLReady(ctx, url)
		out.URLReady = ready
		out.URLCheckDetails = toProbeDetails(details)
	}
	return nil, out, nil
}

func (d *Deps) listRunningAnalyses(ctx context.Context, req *mcp.CallToolRequest, in ListRunningAnalysesIn) (*mcp.CallToolResult, ListRunningAnalysesOut, error) {
	id, err := caller(ctx, req)
	if err != nil {
		return nil, ListRunningAnalysesOut{}, err
	}
	status := in.Status
	if status == "" {
		status = "Running"
	}
	analyses, err := d.Apps.ListAnalyses(ctx, id.DownstreamUsername, status)
	if err != nil {
		return nil, ListRunningAnalysesOut{}, err
	}
	out := ListRunningAnalysesOut{Analyses: make([]AnalysisOut, 0, len(analyses))}
	for _, a := range analyses {
		out.Analyses = append(out.Analyses, AnalysisOut{
			AnalysisID: a.ID,
			Name:       a.Name,
			AppID:      a.AppID,
			SystemID:   a.SystemID,
			Status:     a.Status,
		})
	}
	return nil, out, nil
}

func (d *Deps) stopAnalysis(ctx context.Context, req *mcp.CallToolRequest, in StopAnalysisIn) (*mcp.CallToolResult, StopAnalysisOut, error) {
	if _, err := caller(ctx, req); err != nil {
		return nil, StopAnalysisOut{}, err
	}
	if err := validateUUID(in.AnalysisID, "analysis_id"); err != nil {
		return nil, StopAnalysisOut{}, err
	}

	out := StopAnalysisOut{AnalysisID: in.AnalysisID, Operation: in.Operation}
	switch in.Operation {
	case "save_and_exit":
		if err := d.Exposer.SaveAndExit(ctx, in.AnalysisID); err != nil {
			return nil, StopAnalysisOut{}, err
		}
		out.Status = "terminated"
		out.OutputsSaved = true
	case "exit":
		if err := d.Exposer.ExitWithoutSave(ctx, in.AnalysisID); err != nil {
			return nil, StopAnalysisOut{}, err
		}
		out.Status = "terminated"
	case "extend_time":
		if err := d.Exposer.ExtendTimeLimit(ctx, in.AnalysisID); err != nil {
			return nil, StopAnalysisOut{}, err
		}
		out.Status = "extended"
	default:
		return nil, StopAnalysisOut{}, apperr.Validation("operation", "Invalid operation. Must be one of: save_and_exit, exit, extend_time")
	}
	return nil, out, nil
}

func (d *Deps) openInBrowser(ctx context.Context, req *mcp.CallToolRequest, in OpenInBrowserIn) (*mcp.CallToolResult, OpenInBrowserOut, error) {
	if _, err := caller(ctx, req); err != nil {
		return nil, OpenInBrowserOut{}, err
	}
	if err := validateUUID(in.AnalysisID, "analysis_id"); err != nil {
		return nil, OpenInBrowserOut{}, err
	}
	subdomain := d.Vice.ResolveSubdomain(ctx, in.AnalysisID)
	if subdomain == "" {
		return nil, OpenInBrowserOut{}, apperr.NotFound("Analysis URL", in.AnalysisID)
	}
	url := d.Vice.URLFor(subdomain)
	ready, _ := d.Vice.CheckURLReady(ctx, url)
	return nil, OpenInBrowserOut{URL: url, Ready: ready}, nil
}

func paginate(list []apps.App, offset, limit int) []apps.App {
	if offset >= len(list) {
		return nil
	}
	end := min(offset+limit, len(list))
	return list[offset:end]
}

func toProbeDetails(d apps.ProbeDetails) *ProbeDetails {
	return &ProbeDetails{
		StatusCode:     d.StatusCode,
		ResponseTimeMs: d.ResponseTimeMs,
		Attempt:        d.Attempt,
		Error:          d.Error,
	}
}
