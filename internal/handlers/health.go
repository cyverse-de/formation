// Package handlers contains formation's HTTP handlers.
package handlers

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// Health is the unauthenticated health-check endpoint. The body is a
// JSON-encoded string (quotes included) to match the FastAPI implementation.
func Health(c echo.Context) error {
	return c.JSON(http.StatusOK, "Hello from formation.")
}
