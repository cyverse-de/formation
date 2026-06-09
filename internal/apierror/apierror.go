// Package apierror defines formation's typed errors and the Echo error handler
// that renders them with the same JSON shapes as the original FastAPI service,
// which existing clients may parse.
package apierror

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"
)

// Error mirrors the Python FormationError hierarchy: a message, an HTTP status,
// and optional structured details included in the response only when non-empty.
type Error struct {
	Message string
	Status  int
	Details map[string]any
}

func (e *Error) Error() string {
	return e.Message
}

// NewValidation reports invalid input (400) with an optional offending field name.
func NewValidation(message, field string) *Error {
	details := map[string]any{}
	if field != "" {
		details["field"] = field
	}
	return &Error{Message: message, Status: http.StatusBadRequest, Details: details}
}

// NewBadRequest reports an invalid operation or parameters (400).
func NewBadRequest(message string) *Error {
	return &Error{Message: message, Status: http.StatusBadRequest}
}

// NewNotFound reports a missing resource (404); resourceID may be empty.
func NewNotFound(resourceType, resourceID string) *Error {
	message := fmt.Sprintf("%s not found", resourceType)
	if resourceID != "" {
		message = fmt.Sprintf("%s '%s' not found", resourceType, resourceID)
	}
	return &Error{Message: message, Status: http.StatusNotFound}
}

// NewPermissionDenied reports an authorization failure (403).
func NewPermissionDenied(message string) *Error {
	if message == "" {
		message = "Access denied"
	}
	return &Error{Message: message, Status: http.StatusForbidden}
}

// NewServiceUnavailable reports an unconfigured or unreachable dependency (503).
func NewServiceUnavailable(serviceName string) *Error {
	return &Error{Message: fmt.Sprintf("%s service not configured", serviceName), Status: http.StatusServiceUnavailable}
}

// NewExternalService reports an error relayed from a named external service.
func NewExternalService(serviceName string, status int, detail string) *Error {
	return &Error{
		Message: fmt.Sprintf("%s error: %s", serviceName, detail),
		Status:  status,
		Details: map[string]any{"service": serviceName, "original_error": detail},
	}
}

// UpstreamError is an unexpected HTTP error from an external service; the
// handler maps it to 502 like the Python httpx.HTTPStatusError handler.
type UpstreamError struct {
	Status int
	Body   string
}

func (e *UpstreamError) Error() string {
	return fmt.Sprintf("external service error: status %d: %s", e.Status, e.Body)
}

// HTTPErrorHandler renders errors with FastAPI-compatible JSON bodies.
func HTTPErrorHandler(err error, c echo.Context) {
	if c.Response().Committed {
		return
	}

	var (
		apiErr      *Error
		upstreamErr *UpstreamError
		echoErr     *echo.HTTPError
	)

	var status int
	var body map[string]any

	switch {
	case errors.As(err, &apiErr):
		status = apiErr.Status
		body = map[string]any{"detail": apiErr.Message}
		if len(apiErr.Details) > 0 {
			body["details"] = apiErr.Details
		}
	case errors.As(err, &upstreamErr):
		status = http.StatusBadGateway
		body = map[string]any{
			"detail":      fmt.Sprintf("External service error: %s", upstreamErr.Body),
			"status_code": upstreamErr.Status,
		}
	case errors.As(err, &echoErr):
		status = echoErr.Code
		body = map[string]any{"detail": echoErr.Message}
	default:
		status = http.StatusInternalServerError
		body = map[string]any{"detail": "Internal server error", "error": err.Error()}
	}

	c.Logger().Error(err)

	var writeErr error
	if c.Request().Method == http.MethodHead {
		writeErr = c.NoContent(status)
	} else {
		writeErr = c.JSON(status, body)
	}
	if writeErr != nil {
		c.Logger().Error(writeErr)
	}
}
