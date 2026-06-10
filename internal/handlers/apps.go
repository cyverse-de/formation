package handlers

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/cyverse-de/formation/internal/apierror"
	"github.com/cyverse-de/formation/internal/auth"
	"github.com/cyverse-de/formation/internal/clients"
	"github.com/cyverse-de/formation/internal/config"
	"github.com/cyverse-de/formation/internal/vice"
)

// listAppsPageSize is the upstream page size used when client-side filters
// force paging through the full apps corpus.
const listAppsPageSize = 500

// Apps serves the app discovery, launch, and analysis endpoints.
type Apps struct {
	apps       *clients.Apps
	exposer    *clients.AppExposer
	urls       *vice.URLChecker
	subdomains *vice.SubdomainResolver

	userSuffix              string
	viceDomain              string
	outputZone              string
	serviceAccountUsernames map[string]string
}

// NewApps wires the apps handler with its clients and config values.
func NewApps(appsClient *clients.Apps, exposer *clients.AppExposer, urls *vice.URLChecker, subdomains *vice.SubdomainResolver, cfg *config.Config) *Apps {
	return &Apps{
		apps:                    appsClient,
		exposer:                 exposer,
		urls:                    urls,
		subdomains:              subdomains,
		userSuffix:              cfg.UserSuffix,
		viceDomain:              cfg.ViceDomain,
		outputZone:              cfg.OutputZone,
		serviceAccountUsernames: cfg.ServiceAccountUsernames,
	}
}

// username resolves the backend username for the authenticated identity.
func (h *Apps) username(c echo.Context) (string, error) {
	return auth.GetInfo(c).UsernameForBackend(h.serviceAccountUsernames)
}

// JobTypes lists the valid job type values for the GET /apps job_type filter.
//
// @Summary List supported job types
// @Tags Apps
// @Security BearerAuth
// @Produce json
// @Success 200 {object} map[string]interface{} "job_types entries with name, description, and internal_name"
// @Router /apps/job-types [get]
func (h *Apps) JobTypes(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]any{"job_types": jobTypes})
}

// List serves GET /apps with filtering and pagination. Unlike the Python
// version (which always fetched one 1000-app page), unfiltered requests pass
// limit/offset straight upstream and filtered requests page through the full
// corpus, so results are no longer truncated at 1000 apps.
//
// @Summary List apps available to the user
// @Description Lists apps with optional filters. Date filters use the form <operator><ISO-8601 date>,
// @Description e.g. '>2025-09-29' or '<=2024-12-31T23:59:59' (see docs/DATE_FILTERING.md).
// @Tags Apps
// @Security BearerAuth
// @Produce json
// @Param limit query int false "Page size (1-1000)" default(100)
// @Param offset query int false "Page offset" default(0)
// @Param name query string false "Search term forwarded to the apps service"
// @Param job_type query string false "Job type filter" Enums(vice, interactive, de, osg, tapis)
// @Param description query string false "Case-insensitive substring match on the description"
// @Param integrator query string false "Case-insensitive substring match on the integrator username"
// @Param integration_date query string false "Date filter on the integration date"
// @Param edited_date query string false "Date filter on the last-edited date"
// @Success 200 {object} map[string]interface{} "total and apps"
// @Failure 400 {object} map[string]interface{} "Invalid pagination value or date filter"
// @Router /apps [get]
func (h *Apps) List(c echo.Context) error {
	limit, err := intQueryParam(c, "limit", 100)
	if err != nil {
		return err
	}
	offset, err := intQueryParam(c, "offset", 0)
	if err != nil {
		return err
	}
	if limit < 1 || limit > 1000 {
		return apierror.NewValidation("Limit must be between 1 and 1000", "limit")
	}
	if offset < 0 {
		return apierror.NewValidation("Offset must be non-negative", "offset")
	}

	username, err := h.username(c)
	if err != nil {
		return err
	}

	filters := &appFilters{
		description: c.QueryParam("description"),
		integrator:  c.QueryParam("integrator"),
	}
	if jobType := c.QueryParam("job_type"); jobType != "" {
		filters.jobType = normalizeJobType(jobType)
	}
	if expr := c.QueryParam("integration_date"); expr != "" {
		if filters.integrationDate, err = parseDateFilter(expr); err != nil {
			return apierror.NewValidation(err.Error(), "integration_date")
		}
	}
	if expr := c.QueryParam("edited_date"); expr != "" {
		if filters.editedDate, err = parseDateFilter(expr); err != nil {
			return apierror.NewValidation(err.Error(), "edited_date")
		}
	}

	ctx := c.Request().Context()
	search := c.QueryParam("name")

	// Without client-side filters the apps service can paginate for us.
	if !filters.active() {
		result, err := h.ListAppsPage(ctx, username, search, limit, offset)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusOK, result)
	}

	// With filters, page through the full corpus before filtering so matches
	// beyond the first upstream page are not lost.
	var all []map[string]any
	for upstreamOffset := 0; ; upstreamOffset += listAppsPageSize {
		response, err := h.apps.ListApps(ctx, username, listAppsPageSize, upstreamOffset, search)
		if err != nil {
			return err
		}
		page := appsFromResponse(response)
		all = append(all, page...)
		if len(page) < listAppsPageSize {
			break
		}
	}

	filtered, err := filters.apply(all, h.userSuffix)
	if err != nil {
		return apierror.NewBadRequest(err.Error())
	}

	total := len(filtered)
	page := paginate(filtered, offset, limit)

	return c.JSON(http.StatusOK, map[string]any{
		"total": total,
		"apps":  formatApps(page, h.userSuffix),
	})
}

// Parameters serves GET /apps/{system_id}/{app_id}/parameters.
//
// @Summary Get an app's parameter groups
// @Tags Apps
// @Security BearerAuth
// @Produce json
// @Param system_id path string true "Execution system id (e.g. de)"
// @Param app_id path string true "App UUID"
// @Success 200 {object} map[string]interface{} "groups and overall_job_type"
// @Failure 400 {object} map[string]interface{} "Invalid app ID format"
// @Failure 502 {object} map[string]interface{} "Apps service error (including unknown apps)"
// @Router /apps/{system_id}/{app_id}/parameters [get]
func (h *Apps) Parameters(c echo.Context) error {
	username, err := h.username(c)
	if err != nil {
		return err
	}
	result, err := h.AppParameters(c.Request().Context(), username, c.Param("system_id"), c.Param("app_id"))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, result)
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

func paginate(apps []map[string]any, offset, limit int) []map[string]any {
	if offset >= len(apps) {
		return nil
	}
	end := min(offset+limit, len(apps))
	return apps[offset:end]
}
