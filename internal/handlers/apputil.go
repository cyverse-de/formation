package handlers

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/cyverse-de/formation/internal/apierror"
)

// jobTypeAliases maps user-facing job type names to the internal names used
// by the apps service.
var jobTypeAliases = map[string]string{
	"vice":        "Interactive",
	"interactive": "Interactive",
	"de":          "DE",
	"osg":         "OSG",
	"tapis":       "Tapis",
}

// jobTypes is the metadata served by GET /apps/job-types.
var jobTypes = []map[string]any{
	{
		"name":          "VICE",
		"description":   "Visual Interactive Computing Environment applications",
		"internal_name": "Interactive",
	},
	{
		"name":          "DE",
		"description":   "Discovery Environment batch applications",
		"internal_name": "DE",
	},
	{
		"name":          "OSG",
		"description":   "Open Science Grid applications",
		"internal_name": "OSG",
	},
	{
		"name":          "Tapis",
		"description":   "High-Performance Computing applications",
		"internal_name": "Tapis",
	},
}

// normalizeJobType maps aliases like "vice" to internal names like
// "Interactive"; unknown values pass through for future job types.
func normalizeJobType(jobType string) string {
	if normalized, ok := jobTypeAliases[strings.ToLower(jobType)]; ok {
		return normalized
	}
	return jobType
}

// validateUUID checks UUID syntax, producing the Python error wording
// ("app_id" -> "Invalid app ID format").
func validateUUID(value, fieldName string) (string, error) {
	parsed, err := uuid.Parse(value)
	if err != nil {
		parts := strings.Split(fieldName, "_")
		for i, part := range parts {
			if part == "id" {
				parts[i] = "ID"
			}
		}
		return "", apierror.NewValidation(
			fmt.Sprintf("Invalid %s format", strings.Join(parts, " ")), fieldName)
	}
	return parsed.String(), nil
}

// stripUserSuffix removes the configured user suffix from a username.
func stripUserSuffix(username, suffix string) string {
	if suffix != "" {
		return strings.TrimSuffix(username, suffix)
	}
	return username
}

// isPlaceholder reports whether a submission value is empty/falsy or the
// literal "string" placeholder Swagger UI generates.
func isPlaceholder(value any) bool {
	switch v := value.(type) {
	case nil:
		return true
	case string:
		return v == "" || v == "string"
	case bool:
		return !v
	case float64:
		return v == 0
	case map[string]any:
		return len(v) == 0
	case []any:
		return len(v) == 0
	default:
		return false
	}
}

var dateFilterPattern = regexp.MustCompile(`^(>=|<=|==|>|<)\s*(.+)$`)

// isoLayouts covers the ISO-8601 forms Python's datetime.fromisoformat accepts.
var isoLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05.999999999",
	"2006-01-02T15:04",
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04",
	"2006-01-02",
}

// parseISODate parses an ISO-8601 date, normalizing to UTC: timezone-aware
// values are converted, naive values are taken as UTC (the Python code
// compares naive datetimes the same way).
func parseISODate(value string) (time.Time, error) {
	value = strings.TrimSpace(strings.Replace(value, "Z", "+00:00", 1))
	for _, layout := range isoLayouts {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid ISO 8601 date: %q", value)
}

// dateFilter is a parsed date filter expression like ">2025-09-29".
type dateFilter struct {
	operator string
	date     time.Time
}

// parseDateFilter parses an expression like ">=2024-12-31" with the same
// error messages as the Python version; "==" is normalized to "=".
func parseDateFilter(expr string) (*dateFilter, error) {
	match := dateFilterPattern.FindStringSubmatch(strings.TrimSpace(expr))
	if match == nil {
		//nolint:staticcheck // capitalized to match the Python API error text verbatim
		return nil, fmt.Errorf(
			"Invalid date filter format: '%s'. Expected format: <operator><date> (e.g., '>2025-09-29', '<=2024-12-31T23:59:59')",
			expr)
	}
	operator, dateStr := match[1], match[2]

	date, err := parseISODate(dateStr)
	if err != nil {
		//nolint:staticcheck // capitalized to match the Python API error text verbatim
		return nil, fmt.Errorf(
			"Invalid date format: '%s'. Expected ISO 8601 format (e.g., '2025-09-29', '2025-09-29T14:30:00', '2025-09-29T14:30:00Z')",
			strings.TrimSpace(dateStr))
	}

	if operator == "==" {
		operator = "="
	}
	return &dateFilter{operator: operator, date: date}, nil
}

// matches applies the filter's comparison to an app date.
func (f *dateFilter) matches(appDate time.Time) bool {
	switch f.operator {
	case ">":
		return appDate.After(f.date)
	case "<":
		return appDate.Before(f.date)
	case ">=":
		return !appDate.Before(f.date)
	case "<=":
		return !appDate.After(f.date)
	case "=":
		return appDate.Equal(f.date)
	default:
		return false
	}
}

// formatAppForResponse reshapes an apps-service app into formation's format.
func formatAppForResponse(app map[string]any, userSuffix string) map[string]any {
	var integratorUsername any
	if name, ok := app["integrator_name"].(string); ok {
		integratorUsername = stripUserSuffix(name, userSuffix)
	}
	return map[string]any{
		"id":                  app["id"],
		"name":                app["name"],
		"description":         app["description"],
		"version":             app["version"],
		"integrator_username": integratorUsername,
		"integration_date":    app["integration_date"],
		"edited_date":         app["edited_date"],
		"system_id":           app["system_id"],
		"overall_job_type":    app["overall_job_type"],
	}
}

// appFilters holds the client-side filters for GET /apps.
type appFilters struct {
	jobType         string // normalized internal name
	description     string
	integrator      string
	integrationDate *dateFilter
	editedDate      *dateFilter
}

// active reports whether any client-side filtering is needed.
func (f *appFilters) active() bool {
	return f.jobType != "" || f.description != "" || f.integrator != "" ||
		f.integrationDate != nil || f.editedDate != nil
}

// apply filters the apps list, mirroring the Python filter chain. A date
// field that fails to parse aborts with an error, like the Python ValueError.
func (f *appFilters) apply(apps []map[string]any, userSuffix string) ([]map[string]any, error) {
	filtered := make([]map[string]any, 0, len(apps))
	integratorSearch := strings.ToLower(stripUserSuffix(f.integrator, userSuffix))

	for _, app := range apps {
		if f.jobType != "" && app["overall_job_type"] != f.jobType {
			continue
		}
		if f.description != "" {
			description, _ := app["description"].(string)
			if !strings.Contains(strings.ToLower(description), strings.ToLower(f.description)) {
				continue
			}
		}
		if f.integrator != "" {
			integratorName, _ := app["integrator_name"].(string)
			if integratorSearch == "" || !strings.Contains(strings.ToLower(integratorName), integratorSearch) {
				continue
			}
		}
		ok, err := matchesDateFilter(app, "integration_date", f.integrationDate)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		ok, err = matchesDateFilter(app, "edited_date", f.editedDate)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		filtered = append(filtered, app)
	}
	return filtered, nil
}

// matchesDateFilter checks one date field; apps missing the field are
// excluded when a filter is set, matching the Python behavior.
func matchesDateFilter(app map[string]any, field string, filter *dateFilter) (bool, error) {
	if filter == nil {
		return true, nil
	}
	value, ok := app[field].(string)
	if !ok || value == "" {
		return false, nil
	}
	appDate, err := parseISODate(value)
	if err != nil {
		return false, err
	}
	return filter.matches(appDate), nil
}

// intQueryParam parses an integer query parameter with a default.
func intQueryParam(c echo.Context, name string, fallback int) (int, error) {
	value := c.QueryParam(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, apierror.NewValidation("Invalid integer value for "+name, name)
	}
	return parsed, nil
}

// valueOr mirrors Python dict.get(key, default): the default applies only
// when the key is absent, not when its value is null.
func valueOr(m map[string]any, key string, fallback any) any {
	if value, ok := m[key]; ok {
		return value
	}
	return fallback
}

// setDefault mirrors Python dict.setdefault.
func setDefault(m map[string]any, key string, value any) {
	if _, ok := m[key]; !ok {
		m[key] = value
	}
}

// hasPlaceholderRequirements detects the all-zero requirements entry Swagger
// UI generates, which must be removed so the app's defaults apply.
func hasPlaceholderRequirements(value any) bool {
	requirements, ok := value.([]any)
	if !ok || len(requirements) == 0 {
		return false
	}
	first, ok := requirements[0].(map[string]any)
	if !ok {
		return false
	}
	for _, field := range []string{"step_number", "min_cpu_cores", "max_cpu_cores", "min_memory_limit"} {
		if !isZero(first[field]) {
			return false
		}
	}
	return true
}

// isZero mirrors Python's `value == 0`, where False also equals 0.
func isZero(value any) bool {
	switch v := value.(type) {
	case float64:
		return v == 0
	case bool:
		return !v
	default:
		return false
	}
}
