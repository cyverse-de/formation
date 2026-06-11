package handlers

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/cyverse-de/formation/internal/apierror"
	"github.com/cyverse-de/formation/internal/auth"
	"github.com/cyverse-de/formation/internal/clients"
	"github.com/cyverse-de/formation/internal/config"
	"github.com/cyverse-de/formation/internal/vice"
)

// Apps provides the app discovery, launch, and analysis operations backed by
// terrain, exposed through the MCP tools.
type Apps struct {
	terrain    *clients.Terrain
	urls       *vice.URLChecker
	subdomains *vice.SubdomainResolver

	userSuffix string
	viceDomain string
	outputZone string
}

// NewApps wires the apps operations with their clients and config values.
func NewApps(terrainClient *clients.Terrain, urls *vice.URLChecker, subdomains *vice.SubdomainResolver, cfg *config.Config) *Apps {
	return &Apps{
		terrain:    terrainClient,
		urls:       urls,
		subdomains: subdomains,
		userSuffix: cfg.UserSuffix,
		viceDomain: cfg.ViceDomain,
		outputZone: cfg.OutputZone,
	}
}

func appsFromResponse(response map[string]any) []map[string]any {
	raw, _ := response["apps"].([]any)
	apps := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if app, ok := item.(map[string]any); ok {
			apps = append(apps, app)
		}
	}
	return apps
}

func formatApps(apps []map[string]any, userSuffix string) []map[string]any {
	formatted := make([]map[string]any, 0, len(apps))
	for _, app := range apps {
		formatted = append(formatted, formatAppForResponse(app, userSuffix))
	}
	return formatted
}

// ListAppsPage returns one upstream page of apps, formatted for responses,
// along with the upstream total.
func (h *Apps) ListAppsPage(ctx context.Context, caller *auth.Caller, search string, limit, offset int) (map[string]any, error) {
	response, err := h.terrain.ListApps(ctx, caller.Token, limit, offset, search)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"total": response["total"],
		"apps":  formatApps(appsFromResponse(response), h.userSuffix),
	}, nil
}

// AppParameters returns an app's parameter groups and overall job type.
func (h *Apps) AppParameters(ctx context.Context, caller *auth.Caller, systemID, rawAppID string) (map[string]any, error) {
	appID, err := validateUUID(rawAppID, "app_id")
	if err != nil {
		return nil, err
	}

	appData, err := h.terrain.GetApp(ctx, caller.Token, systemID, appID)
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

// AnalysesForUser lists the caller's analyses filtered by status (empty status
// lists all), reshaped to the formation summary fields.
func (h *Apps) AnalysesForUser(ctx context.Context, caller *auth.Caller, status string) ([]map[string]any, error) {
	result, err := h.terrain.ListAnalyses(ctx, caller.Token, status)
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
func (h *Apps) AnalysisStatus(ctx context.Context, caller *auth.Caller, rawAnalysisID string) (map[string]any, error) {
	analysisID, err := validateUUID(rawAnalysisID, "analysis_id")
	if err != nil {
		return nil, err
	}

	analysis, err := h.terrain.GetAnalysis(ctx, caller.Token, analysisID)
	if err != nil {
		return nil, err
	}

	subdomain := h.subdomains.Resolve(ctx, caller.Token, analysisID)
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
func (h *Apps) ControlAnalysis(ctx context.Context, caller *auth.Caller, rawAnalysisID, operation string) (map[string]any, error) {
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
		result, err = h.terrain.ExtendTimeLimit(ctx, caller.Token, analysisID)
	case "save_and_exit":
		result, err = h.terrain.SaveAndExit(ctx, caller.Token, analysisID)
	default:
		result, err = h.terrain.ExitWithoutSave(ctx, caller.Token, analysisID)
	}
	if err != nil {
		return nil, err
	}

	result["operation"] = operation
	return result, nil
}

// LaunchAnalysis fills in submission defaults and submits the analysis,
// returning the summary (analysis_id, name, status, and url for VICE apps).
func (h *Apps) LaunchAnalysis(ctx context.Context, caller *auth.Caller, systemID, rawAppID, outputZone string, submission map[string]any) (map[string]any, error) {
	appID, err := validateUUID(rawAppID, "app_id")
	if err != nil {
		return nil, err
	}

	prepared := h.prepareSubmission(ctx, submission, appID, systemID, caller, outputZone)

	response, err := h.terrain.SubmitAnalysis(ctx, caller.Token, prepared)
	if err != nil {
		return nil, err
	}

	result := map[string]any{
		"analysis_id": response["id"],
		"name":        valueOr(response, "name", valueOr(prepared, "name", "Unnamed")),
		"status":      valueOr(response, "status", "Submitted"),
	}

	if analysisID, ok := response["id"].(string); ok && analysisID != "" {
		if subdomain := h.subdomains.Resolve(ctx, caller.Token, analysisID); subdomain != "" {
			result["url"] = h.viceURL(subdomain)
		}
	}
	return result, nil
}

// prepareSubmission fills in submission defaults: system_id, debug/notify/
// config, generated analysis name and output directory, and removal of
// placeholder requirements. Mirrors the Python prepare_submission_dict, except
// the email moves nowhere: terrain derives it from the forwarded token, so a
// body-supplied email is stripped rather than promoted to a query parameter.
func (h *Apps) prepareSubmission(ctx context.Context, submission map[string]any, appID, systemID string, caller *auth.Caller, outputZone string) map[string]any {
	submission["app_id"] = appID

	delete(submission, "email")

	if isPlaceholder(submission["system_id"]) {
		submission["system_id"] = systemID
	}

	setDefault(submission, "debug", false)
	setDefault(submission, "notify", true)
	setDefault(submission, "config", map[string]any{})

	if isPlaceholder(submission["name"]) {
		submission["name"] = h.generateAnalysisName(ctx, caller, appID, systemID)
	}

	if isPlaceholder(submission["output_dir"]) {
		name, _ := submission["name"].(string)
		if name == "" {
			name = "analysis"
		}
		submission["output_dir"] = fmt.Sprintf("/%s/home/%s/analyses/%s", outputZone, caller.Username, name)
	}

	if hasPlaceholderRequirements(submission["requirements"]) {
		delete(submission, "requirements")
	}

	return submission
}

var analysisNameClean = regexp.MustCompile(`[^a-z0-9-]+`)

// generateAnalysisName builds "{cleaned-app-name}-{timestamp}", falling back
// to "analysis" when the app name cannot be fetched.
func (h *Apps) generateAnalysisName(ctx context.Context, caller *auth.Caller, appID, systemID string) string {
	cleaned := "analysis"
	if appID != "" {
		if appData, err := h.terrain.GetApp(ctx, caller.Token, systemID, appID); err == nil {
			if name, ok := appData["name"].(string); ok {
				cleaned = strings.Trim(analysisNameClean.ReplaceAllString(strings.ToLower(name), "-"), "-")
			}
		}
	}
	return cleaned + "-" + time.Now().Format("2006-01-02-150405")
}

func (h *Apps) viceURL(subdomain string) string {
	return "https://" + subdomain + h.viceDomain
}
