package apierror

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
)

func render(t *testing.T, err error) (int, map[string]any) {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	HTTPErrorHandler(err, c)

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body is not JSON: %v: %s", err, rec.Body.String())
	}
	return rec.Code, body
}

func TestHTTPErrorHandler(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantBody   map[string]any
	}{
		{
			name:       "validation error with field",
			err:        NewValidation("limit must be between 1 and 1000", "limit"),
			wantStatus: 400,
			wantBody: map[string]any{
				"detail":  "limit must be between 1 and 1000",
				"details": map[string]any{"field": "limit"},
			},
		},
		{
			name:       "validation error without field omits details",
			err:        NewValidation("bad input", ""),
			wantStatus: 400,
			wantBody:   map[string]any{"detail": "bad input"},
		},
		{
			name:       "bad request",
			err:        NewBadRequest("Invalid operation"),
			wantStatus: 400,
			wantBody:   map[string]any{"detail": "Invalid operation"},
		},
		{
			name:       "not found with id",
			err:        NewNotFound("Analysis", "abc-123"),
			wantStatus: 404,
			wantBody:   map[string]any{"detail": "Analysis 'abc-123' not found"},
		},
		{
			name:       "not found without id",
			err:        NewNotFound("Path", ""),
			wantStatus: 404,
			wantBody:   map[string]any{"detail": "Path not found"},
		},
		{
			name:       "permission denied default message",
			err:        NewPermissionDenied(""),
			wantStatus: 403,
			wantBody:   map[string]any{"detail": "Access denied"},
		},
		{
			name:       "service unavailable",
			err:        NewServiceUnavailable("apps"),
			wantStatus: 503,
			wantBody:   map[string]any{"detail": "apps service not configured"},
		},
		{
			name:       "external service error includes details",
			err:        NewExternalService("apps", 502, "boom"),
			wantStatus: 502,
			wantBody: map[string]any{
				"detail":  "apps error: boom",
				"details": map[string]any{"service": "apps", "original_error": "boom"},
			},
		},
		{
			name:       "upstream error maps to 502 with status_code",
			err:        &UpstreamError{Status: 404, Body: `{"reason":"nope"}`},
			wantStatus: 502,
			wantBody: map[string]any{
				"detail":      `External service error: {"reason":"nope"}`,
				"status_code": float64(404),
			},
		},
		{
			name:       "wrapped typed error still detected",
			err:        fmt.Errorf("context: %w", NewBadRequest("inner")),
			wantStatus: 400,
			wantBody:   map[string]any{"detail": "inner"},
		},
		{
			name:       "echo http error",
			err:        echo.NewHTTPError(http.StatusMethodNotAllowed, "Method Not Allowed"),
			wantStatus: 405,
			wantBody:   map[string]any{"detail": "Method Not Allowed"},
		},
		{
			name:       "unhandled error becomes 500 with error field",
			err:        errors.New("something exploded"),
			wantStatus: 500,
			wantBody:   map[string]any{"detail": "Internal server error", "error": "something exploded"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, body := render(t, tt.err)
			if status != tt.wantStatus {
				t.Errorf("status = %d, want %d", status, tt.wantStatus)
			}
			wantJSON, _ := json.Marshal(tt.wantBody)
			gotJSON, _ := json.Marshal(body)
			if string(wantJSON) != string(gotJSON) {
				t.Errorf("body = %s, want %s", gotJSON, wantJSON)
			}
		})
	}
}
