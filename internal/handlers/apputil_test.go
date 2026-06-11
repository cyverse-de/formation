package handlers

import (
	"testing"

	"github.com/cyverse-de/formation/internal/apierror"
)

func TestValidateUUID(t *testing.T) {
	if _, err := validateUUID("0123abcd-0000-4000-8000-00000000beef", "app_id"); err != nil {
		t.Errorf("valid UUID rejected: %v", err)
	}

	tests := []struct {
		field, wantMessage string
	}{
		{"app_id", "Invalid app ID format"},
		{"analysis_id", "Invalid analysis ID format"},
	}
	for _, tt := range tests {
		_, err := validateUUID("not-a-uuid", tt.field)
		apiErr, ok := err.(*apierror.Error)
		if !ok {
			t.Fatalf("err = %T, want *apierror.Error", err)
		}
		if apiErr.Message != tt.wantMessage || apiErr.Status != 400 || apiErr.Details["field"] != tt.field {
			t.Errorf("validateUUID error = %+v, want %q field %q", apiErr, tt.wantMessage, tt.field)
		}
	}
}

func TestIsPlaceholder(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  bool
	}{
		{"nil", nil, true},
		{"empty string", "", true},
		{"swagger placeholder", "string", true},
		{"real string", "hello", false},
		{"zero number", float64(0), true},
		{"nonzero number", float64(3), false},
		{"false", false, true},
		{"true", true, false},
		{"empty map", map[string]any{}, true},
		{"populated map", map[string]any{"k": 1}, false},
		{"empty list", []any{}, true},
		{"populated list", []any{1}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPlaceholder(tt.value); got != tt.want {
				t.Errorf("isPlaceholder(%v) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestFormatAppForResponse(t *testing.T) {
	app := map[string]any{
		"id":               "app-1",
		"name":             "JupyterLab",
		"description":      "notebooks",
		"version":          "1.0",
		"integrator_name":  "alice@iplantcollaborative.org",
		"integration_date": "2025-01-01T00:00:00Z",
		"edited_date":      "2025-02-01T00:00:00Z",
		"system_id":        "de",
		"overall_job_type": "Interactive",
		"extra_field":      "dropped",
	}

	formatted := formatAppForResponse(app, "@iplantcollaborative.org")

	if formatted["integrator_username"] != "alice" {
		t.Errorf("integrator_username = %v, want alice", formatted["integrator_username"])
	}
	if _, ok := formatted["extra_field"]; ok {
		t.Error("extra_field should be dropped")
	}
	if formatted["overall_job_type"] != "Interactive" || formatted["id"] != "app-1" {
		t.Errorf("formatted = %v", formatted)
	}

	// missing integrator_name stays null
	noIntegrator := formatAppForResponse(map[string]any{"id": "x"}, "@s")
	if noIntegrator["integrator_username"] != nil {
		t.Errorf("integrator_username = %v, want nil", noIntegrator["integrator_username"])
	}
}

func TestHasPlaceholderRequirements(t *testing.T) {
	zeroReq := map[string]any{
		"step_number": float64(0), "min_cpu_cores": float64(0),
		"max_cpu_cores": float64(0), "min_memory_limit": float64(0),
	}
	tests := []struct {
		name  string
		value any
		want  bool
	}{
		{"all-zero placeholder", []any{zeroReq}, true},
		{"nil", nil, false},
		{"empty list", []any{}, false},
		{"non-zero cpu", []any{map[string]any{
			"step_number": float64(0), "min_cpu_cores": float64(2),
			"max_cpu_cores": float64(0), "min_memory_limit": float64(0),
		}}, false},
		{"missing field", []any{map[string]any{
			"step_number": float64(0), "min_cpu_cores": float64(0), "max_cpu_cores": float64(0),
		}}, false},
		{"not a map", []any{"zero"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasPlaceholderRequirements(tt.value); got != tt.want {
				t.Errorf("hasPlaceholderRequirements(%v) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}
