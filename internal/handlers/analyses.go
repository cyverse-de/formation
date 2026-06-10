package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/cyverse-de/formation/internal/apierror"
	"github.com/cyverse-de/formation/internal/auth"
)

// ListAnalyses serves GET /apps/analyses/, filtered by status (default
// "Running"; an explicitly empty status lists all analyses, like Python).
//
// @Summary List the user's analyses
// @Description Lists analyses filtered by status (default Running). Pass status with an empty value to list all analyses.
// @Tags Analyses
// @Security BearerAuth
// @Produce json
// @Param status query string false "Analysis status filter; explicitly empty lists all" default(Running)
// @Success 200 {object} map[string]interface{} "analyses with analysis_id, name, app_id, system_id, and status"
// @Router /apps/analyses/ [get]
func (h *Apps) ListAnalyses(c echo.Context) error {
	username, err := h.username(c)
	if err != nil {
		return err
	}

	status := "Running"
	if c.QueryParams().Has("status") {
		status = c.QueryParam("status")
	}

	result, err := h.apps.ListAnalyses(c.Request().Context(), username, status)
	if err != nil {
		return err
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

	return c.JSON(http.StatusOK, map[string]any{"analyses": analyses})
}

// Status serves GET /apps/analyses/{analysis_id}/status, probing the VICE URL
// for readiness when a subdomain exists.
//
// @Summary Get an analysis's status and VICE URL readiness
// @Tags Analyses
// @Security BearerAuth
// @Produce json
// @Param analysis_id path string true "Analysis UUID"
// @Success 200 {object} map[string]interface{} "analysis_id, status, url_ready, plus url and url_check_details when a subdomain exists"
// @Failure 400 {object} map[string]interface{} "Invalid analysis ID format"
// @Failure 502 {object} map[string]interface{} "Apps service error (including unknown analyses)"
// @Router /apps/analyses/{analysis_id}/status [get]
func (h *Apps) Status(c echo.Context) error {
	username, err := h.username(c)
	if err != nil {
		return err
	}
	rawAnalysisID := c.Param("analysis_id")
	analysisID, err := validateUUID(rawAnalysisID, "analysis_id")
	if err != nil {
		return err
	}

	ctx := c.Request().Context()
	analysis, err := h.apps.GetAnalysis(ctx, analysisID, username)
	if err != nil {
		return err
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
	return c.JSON(http.StatusOK, result)
}

// Control serves POST /apps/analyses/{analysis_id}/control for the
// extend_time, save_and_exit, and exit operations.
//
// @Summary Control a running VICE analysis
// @Description extend_time extends the analysis's time limit; save_and_exit terminates it after saving outputs; exit terminates it without saving.
// @Tags Analyses
// @Security BearerAuth
// @Produce json
// @Param analysis_id path string true "Analysis UUID"
// @Param operation query string true "Operation to perform" Enums(extend_time, save_and_exit, exit)
// @Success 200 {object} map[string]interface{} "Operation result; includes the echoed operation"
// @Failure 400 {object} map[string]interface{} "Invalid operation or analysis ID"
// @Router /apps/analyses/{analysis_id}/control [post]
func (h *Apps) Control(c echo.Context) error {
	operation := c.QueryParam("operation")
	if operation != "extend_time" && operation != "save_and_exit" && operation != "exit" {
		return apierror.NewValidation(
			"Invalid operation. Must be one of: extend_time, save_and_exit, exit", "operation")
	}

	analysisID, err := validateUUID(c.Param("analysis_id"), "analysis_id")
	if err != nil {
		return err
	}

	ctx := c.Request().Context()
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
		return err
	}

	result["operation"] = operation
	return c.JSON(http.StatusOK, result)
}

// Details serves GET /apps/analyses/{analysis_id}/details, returning the full
// analysis record from the apps service.
//
// @Summary Get an analysis's full details
// @Tags Analyses
// @Security BearerAuth
// @Produce json
// @Param analysis_id path string true "Analysis UUID"
// @Success 200 {object} map[string]interface{} "The analysis record as returned by the apps service"
// @Failure 400 {object} map[string]interface{} "Invalid analysis ID format"
// @Failure 502 {object} map[string]interface{} "Apps service error (including unknown analyses)"
// @Router /apps/analyses/{analysis_id}/details [get]
func (h *Apps) Details(c echo.Context) error {
	username, err := h.username(c)
	if err != nil {
		return err
	}
	analysisID, err := validateUUID(c.Param("analysis_id"), "analysis_id")
	if err != nil {
		return err
	}

	analysis, err := h.apps.GetAnalysis(c.Request().Context(), analysisID, username)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, analysis)
}

// LaunchSubmission documents the optional launch request body for Swagger;
// the handler accepts arbitrary submission JSON and fills in defaults.
type LaunchSubmission struct {
	Name         string           `json:"name,omitempty"`
	Email        string           `json:"email,omitempty"`
	Debug        bool             `json:"debug,omitempty"`
	Notify       bool             `json:"notify,omitempty"`
	OutputDir    string           `json:"output_dir,omitempty"`
	Config       map[string]any   `json:"config,omitempty"`
	Requirements []map[string]any `json:"requirements,omitempty"`
}

// Launch serves POST /app/launch/{system_id}/{app_id}.
//
// @Summary Launch an app
// @Description Submits an analysis. The body is optional; missing fields get defaults: a generated
// @Description analysis name, an output directory under the user's home, the email from the JWT,
// @Description debug=false, and notify=true. Swagger UI placeholder values are stripped.
// @Tags Apps
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param system_id path string true "Execution system id (e.g. de)"
// @Param app_id path string true "App UUID"
// @Param output_zone query string false "iRODS zone for the output directory (defaults to the configured zone)"
// @Param submission body LaunchSubmission false "Analysis submission; all fields optional"
// @Success 200 {object} map[string]interface{} "analysis_id, name, status, plus url for VICE analyses"
// @Failure 400 {object} map[string]interface{} "Invalid app ID or request body"
// @Router /app/launch/{system_id}/{app_id} [post]
func (h *Apps) Launch(c echo.Context) error {
	info := auth.GetInfo(c)
	username, err := info.UsernameForBackend(h.serviceAccountUsernames)
	if err != nil {
		return err
	}

	outputZone := h.outputZone
	if c.QueryParams().Has("output_zone") {
		outputZone = c.QueryParam("output_zone")
	}

	systemID := c.Param("system_id")
	appID := c.Param("app_id")
	if _, err := validateUUID(appID, "app_id"); err != nil {
		return err
	}

	submission, err := readSubmission(c)
	if err != nil {
		return err
	}

	ctx := c.Request().Context()
	prepared, email := h.prepareSubmission(ctx, submission, appID, systemID, info, username, outputZone)

	response, err := h.apps.SubmitAnalysis(ctx, prepared, username, email)
	if err != nil {
		return err
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
	return c.JSON(http.StatusOK, result)
}

// readSubmission decodes the optional JSON request body into a submission map.
func readSubmission(c echo.Context) (map[string]any, error) {
	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		return nil, err
	}
	submission := map[string]any{}
	if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &submission); err != nil {
			return nil, apierror.NewValidation("Invalid JSON in request body", "")
		}
		if submission == nil { // body was the JSON literal null
			submission = map[string]any{}
		}
	}
	return submission, nil
}

// prepareSubmission fills in submission defaults: resolved email (returned
// separately for the query parameter), system_id, debug/notify/config,
// generated analysis name and output directory, and removal of Swagger
// placeholder requirements. Mirrors the Python prepare_submission_dict.
func (h *Apps) prepareSubmission(ctx context.Context, submission map[string]any, appID, systemID string, info *auth.Info, username, outputZone string) (map[string]any, string) {
	submission["app_id"] = appID

	submission["email"] = h.resolveEmail(submission["email"], info, username)

	if isPlaceholder(submission["system_id"]) {
		submission["system_id"] = systemID
	}

	setDefault(submission, "debug", false)
	setDefault(submission, "notify", true)
	setDefault(submission, "config", map[string]any{})

	if isPlaceholder(submission["name"]) {
		submission["name"] = h.generateAnalysisName(ctx, appID, username, systemID)
	}

	if isPlaceholder(submission["output_dir"]) {
		name, _ := submission["name"].(string)
		if name == "" {
			name = "analysis"
		}
		submission["output_dir"] = fmt.Sprintf("/%s/home/%s/analyses/%s", outputZone, username, name)
	}

	if hasPlaceholderRequirements(submission["requirements"]) {
		delete(submission, "requirements")
	}

	email := fmt.Sprint(submission["email"])
	delete(submission, "email")
	return submission, email
}

// resolveEmail picks the email in priority order: request body, JWT claim,
// then username + the configured suffix.
func (h *Apps) resolveEmail(fromBody any, info *auth.Info, username string) any {
	if !isPlaceholder(fromBody) {
		return fromBody
	}
	if info.Type == auth.TypeUser && info.Claims.Email != nil && *info.Claims.Email != "" {
		return *info.Claims.Email
	}
	return username + h.userSuffix
}

var analysisNameClean = regexp.MustCompile(`[^a-z0-9-]+`)

// generateAnalysisName builds "{cleaned-app-name}-{timestamp}", falling back
// to "analysis" when the app name cannot be fetched.
func (h *Apps) generateAnalysisName(ctx context.Context, appID, username, systemID string) string {
	cleaned := "analysis"
	if appID != "" {
		if appData, err := h.apps.GetApp(ctx, systemID, appID, username); err == nil {
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
