package apps

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"strings"
	"time"
)

// PrepareInput carries the inputs needed to build an analysis submission.
type PrepareInput struct {
	Submission map[string]any
	SystemID   string
	AppID      string
	Username   string
	JWTEmail   string
	OutputZone string
	UserSuffix string
	Now        time.Time
}

var nameCleanRE = regexp.MustCompile(`[^a-z0-9-]+`)

// PrepareSubmission fills in defaults and derived values for an analysis
// submission, mirroring the original prepare_submission_dict. It returns the
// prepared submission (without the email, which the apps service expects as a
// query parameter) and the resolved email.
func (c *AppsClient) PrepareSubmission(ctx context.Context, in PrepareInput) (map[string]any, string, error) {
	sub := maps.Clone(in.Submission)
	if sub == nil {
		sub = map[string]any{}
	}
	sub["app_id"] = in.AppID

	email := resolveUserEmail(getString(sub, "email"), in.JWTEmail, in.Username, in.UserSuffix)

	if isPlaceholder(getString(sub, "system_id")) {
		sub["system_id"] = in.SystemID
	}

	setDefault(sub, "debug", false)
	setDefault(sub, "notify", true)
	setDefault(sub, "config", map[string]any{})

	if isPlaceholder(getString(sub, "name")) {
		sub["name"] = c.generateAnalysisName(ctx, in.AppID, in.Username, in.SystemID, in.Now)
	}

	if isPlaceholder(getString(sub, "output_dir")) {
		name := getString(sub, "name")
		if name == "" {
			name = "analysis"
		}
		sub["output_dir"] = generateOutputDirectory(in.OutputZone, in.Username, name)
	}

	if shouldRemovePlaceholderRequirements(sub["requirements"]) {
		delete(sub, "requirements")
	}

	delete(sub, "email")
	return sub, email, nil
}

// generateAnalysisName builds "{clean-app-name}-{timestamp}", falling back to
// "analysis" when the app name cannot be retrieved.
func (c *AppsClient) generateAnalysisName(ctx context.Context, appID, username, systemID string, now time.Time) string {
	clean := "analysis"
	if appID != "" {
		if app, err := c.GetApp(ctx, systemID, appID, username); err == nil && app.Name != "" {
			if cleaned := cleanAppName(app.Name); cleaned != "" {
				clean = cleaned
			}
		} else if err != nil && c.logger != nil {
			c.logger.Warn("could not fetch app name for analysis naming; using generic name", "app_id", appID, "error", err)
		}
	}
	return fmt.Sprintf("%s-%s", clean, now.Format("2006-01-02-150405"))
}

func cleanAppName(name string) string {
	return strings.Trim(nameCleanRE.ReplaceAllString(strings.ToLower(name), "-"), "-")
}

// generateOutputDirectory returns /{zone}/home/{username}/analyses/{name}.
func generateOutputDirectory(zone, username, name string) string {
	return fmt.Sprintf("/%s/home/%s/analyses/%s", zone, username, name)
}

// resolveUserEmail resolves the submission email: a non-placeholder body value
// wins, then the JWT email, then username+suffix.
func resolveUserEmail(emailFromBody, jwtEmail, username, suffix string) string {
	if !isPlaceholder(emailFromBody) {
		return emailFromBody
	}
	if jwtEmail != "" {
		return jwtEmail
	}
	return username + suffix
}

// isPlaceholder reports whether a value is empty or the Swagger UI default
// "string".
func isPlaceholder(v string) bool {
	return v == "" || v == "string"
}

// shouldRemovePlaceholderRequirements reports whether the requirements list is a
// Swagger UI placeholder (first entry has all zero resource values).
func shouldRemovePlaceholderRequirements(requirements any) bool {
	list, ok := requirements.([]any)
	if !ok || len(list) == 0 {
		return false
	}
	first, ok := list[0].(map[string]any)
	if !ok {
		return false
	}
	for _, field := range []string{"step_number", "min_cpu_cores", "max_cpu_cores", "min_memory_limit"} {
		if !isZeroNumber(first[field]) {
			return false
		}
	}
	return true
}

func isZeroNumber(v any) bool {
	switch n := v.(type) {
	case float64:
		return n == 0
	case int:
		return n == 0
	default:
		return false
	}
}

func getString(m map[string]any, key string) string {
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}

func setDefault(m map[string]any, key string, value any) {
	if _, ok := m[key]; !ok {
		m[key] = value
	}
}
