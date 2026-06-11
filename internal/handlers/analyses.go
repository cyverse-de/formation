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
	caller, err := h.caller(c)
	if err != nil {
		return err
	}

	status := "Running"
	if c.QueryParams().Has("status") {
		status = c.QueryParam("status")
	}

	analyses, err := h.AnalysesForUser(c.Request().Context(), caller, status)
	if err != nil {
		return err
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
// @Failure 502 {object} map[string]interface{} "Terrain service error (including unknown analyses)"
// @Router /apps/analyses/{analysis_id}/status [get]
func (h *Apps) Status(c echo.Context) error {
	caller, err := h.caller(c)
	if err != nil {
		return err
	}
	result, err := h.AnalysisStatus(c.Request().Context(), caller, c.Param("analysis_id"))
	if err != nil {
		return err
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
	caller, err := h.caller(c)
	if err != nil {
		return err
	}
	result, err := h.ControlAnalysis(c.Request().Context(), caller, c.Param("analysis_id"), c.QueryParam("operation"))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, result)
}

// Details serves GET /apps/analyses/{analysis_id}/details, returning the full
// analysis record from terrain.
//
// @Summary Get an analysis's full details
// @Tags Analyses
// @Security BearerAuth
// @Produce json
// @Param analysis_id path string true "Analysis UUID"
// @Success 200 {object} map[string]interface{} "The analysis record as returned by the DE backend"
// @Failure 400 {object} map[string]interface{} "Invalid analysis ID format"
// @Failure 502 {object} map[string]interface{} "Terrain service error (including unknown analyses)"
// @Router /apps/analyses/{analysis_id}/details [get]
func (h *Apps) Details(c echo.Context) error {
	caller, err := h.caller(c)
	if err != nil {
		return err
	}
	analysisID, err := validateUUID(c.Param("analysis_id"), "analysis_id")
	if err != nil {
		return err
	}

	analysis, err := h.terrain.GetAnalysis(c.Request().Context(), caller.Token, analysisID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, analysis)
}

// LaunchSubmission documents the optional launch request body for Swagger;
// the handler accepts arbitrary submission JSON and fills in defaults.
type LaunchSubmission struct {
	Name         string           `json:"name,omitempty"`
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
// @Description analysis name, an output directory under the user's home, debug=false, and notify=true.
// @Description Swagger UI placeholder values are stripped. Notification email comes from the token.
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
	caller, err := h.caller(c)
	if err != nil {
		return err
	}

	outputZone := h.outputZone
	if c.QueryParams().Has("output_zone") {
		outputZone = c.QueryParam("output_zone")
	}

	appID := c.Param("app_id")
	if _, err := validateUUID(appID, "app_id"); err != nil {
		return err
	}

	submission, err := readSubmission(c)
	if err != nil {
		return err
	}

	result, err := h.LaunchAnalysis(c.Request().Context(), caller, c.Param("system_id"), appID, outputZone, submission)
	if err != nil {
		return err
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

// prepareSubmission fills in submission defaults: system_id, debug/notify/
// config, generated analysis name and output directory, and removal of Swagger
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
