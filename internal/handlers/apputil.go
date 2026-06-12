package handlers

import (
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/cyverse-de/formation/internal/apierror"
)

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
// literal "string" placeholder some clients generate.
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

// valueOr mirrors Python dict.get(key, default): the default applies only
// when the key is absent, not when its value is null.
func valueOr(m map[string]any, key string, fallback any) any {
	if value, ok := m[key]; ok {
		return value
	}
	return fallback
}

// subMap returns m[key] as a nested object, or nil when absent or not a map.
func subMap(m map[string]any, key string) map[string]any {
	nested, _ := m[key].(map[string]any)
	return nested
}

// mapString returns m[key] as a string, or "" when absent or not a string.
func mapString(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// setDefault mirrors Python dict.setdefault.
func setDefault(m map[string]any, key string, value any) {
	if _, ok := m[key]; !ok {
		m[key] = value
	}
}

// hasPlaceholderRequirements detects an all-zero requirements entry, which
// must be removed so the app's defaults apply.
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
