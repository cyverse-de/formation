package handlers

import (
	"context"

	"github.com/cyverse-de/formation/internal/apierror"
	"github.com/cyverse-de/formation/internal/auth"
)

// This file holds the echo-free bodies of the apps/analyses endpoints so the
// MCP tools can call the same logic in-process. The Echo handlers wrap these
// and must keep their response shapes byte-compatible with the Python API.

// ListAppsPage returns one upstream page of apps, formatted for responses,
// along with the upstream total.
func (h *Apps) ListAppsPage(ctx context.Context, username, search string, limit, offset int) (map[string]any, error) {
	response, err := h.apps.ListApps(ctx, username, limit, offset, search)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"total": response["total"],
		"apps":  formatApps(appsFromResponse(response), h.userSuffix),
	}, nil
}

// AppParameters returns an app's parameter groups and overall job type.
func (h *Apps) AppParameters(ctx context.Context, username, systemID, rawAppID string) (map[string]any, error) {
	appID, err := validateUUID(rawAppID, "app_id")
	if err != nil {
		return nil, err
	}

	appData, err := h.apps.GetApp(ctx, systemID, appID, username)
	if err != nil {
		return nil, err
	}

	groups, ok := appData["groups"]
	if !ok {
		groups = []any{}
	}
	return map[string]any{
		"groups":           groups,
		"overall_job_type": appData["overall_job_type"],
	}, nil
}

// AnalysesForUser lists the user's analyses filtered by status (empty status
// lists all), reshaped to the formation summary fields.
func (h *Apps) AnalysesForUser(ctx context.Context, username, status string) ([]map[string]any, error) {
	result, err := h.apps.ListAnalyses(ctx, username, status)
	if err != nil {
		return nil, err
	}

	raw, _ := result["analyses"].([]any)
	analyses := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		analysis, ok := item.(map[string]any)
		if !ok {
			continue
		}
		analyses = append(analyses, map[string]any{
			"analysis_id": analysis["id"],
			"name":        analysis["name"],
			"app_id":      analysis["app_id"],
			"system_id":   analysis["system_id"],
			"status":      analysis["status"],
		})
	}
	return analyses, nil
}

// AnalysisStatus returns an analysis's status, probing the VICE URL for
// readiness when a subdomain exists.
func (h *Apps) AnalysisStatus(ctx context.Context, username, rawAnalysisID string) (map[string]any, error) {
	analysisID, err := validateUUID(rawAnalysisID, "analysis_id")
	if err != nil {
		return nil, err
	}

	analysis, err := h.apps.GetAnalysis(ctx, analysisID, username)
	if err != nil {
		return nil, err
	}

	subdomain := h.subdomains.Resolve(ctx, analysisID)
	urlReady := false
	var urlCheckDetails map[string]any
	if subdomain != "" {
		urlReady, urlCheckDetails = h.urls.Check(ctx, h.viceURL(subdomain))
	}

	result := map[string]any{
		"analysis_id": rawAnalysisID,
		"status":      valueOr(analysis, "status", "Unknown"),
		"url_ready":   urlReady,
	}
	if subdomain != "" {
		result["url"] = h.viceURL(subdomain)
	}
	if len(urlCheckDetails) > 0 {
		result["url_check_details"] = urlCheckDetails
	}
	return result, nil
}

// ControlAnalysis performs the extend_time, save_and_exit, or exit operation
// on a running VICE analysis.
func (h *Apps) ControlAnalysis(ctx context.Context, rawAnalysisID, operation string) (map[string]any, error) {
	if operation != "extend_time" && operation != "save_and_exit" && operation != "exit" {
		return nil, apierror.NewValidation(
			"Invalid operation. Must be one of: extend_time, save_and_exit, exit", "operation")
	}

	analysisID, err := validateUUID(rawAnalysisID, "analysis_id")
	if err != nil {
		return nil, err
	}

	var result map[string]any
	switch operation {
	case "extend_time":
		result, err = h.exposer.ExtendTimeLimit(ctx, analysisID)
	case "save_and_exit":
		result, err = h.exposer.SaveAndExit(ctx, analysisID)
	default:
		result, err = h.exposer.ExitWithoutSave(ctx, analysisID)
	}
	if err != nil {
		return nil, err
	}

	result["operation"] = operation
	return result, nil
}

// LaunchAnalysis fills in submission defaults and submits the analysis,
// returning the summary (analysis_id, name, status, and url for VICE apps).
func (h *Apps) LaunchAnalysis(ctx context.Context, info *auth.Info, systemID, rawAppID, outputZone string, submission map[string]any) (map[string]any, error) {
	username, err := info.UsernameForBackend(h.serviceAccountUsernames)
	if err != nil {
		return nil, err
	}

	appID, err := validateUUID(rawAppID, "app_id")
	if err != nil {
		return nil, err
	}

	prepared, email := h.prepareSubmission(ctx, submission, appID, systemID, info, username, outputZone)

	response, err := h.apps.SubmitAnalysis(ctx, prepared, username, email)
	if err != nil {
		return nil, err
	}

	result := map[string]any{
		"analysis_id": response["id"],
		"name":        valueOr(response, "name", valueOr(prepared, "name", "Unnamed")),
		"status":      valueOr(response, "status", "Submitted"),
	}

	if analysisID, ok := response["id"].(string); ok && analysisID != "" {
		if subdomain := h.subdomains.Resolve(ctx, analysisID); subdomain != "" {
			result["url"] = h.viceURL(subdomain)
		}
	}
	return result, nil
}
