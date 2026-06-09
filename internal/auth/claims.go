// Package auth implements Keycloak OIDC token verification, the password
// grant used by /login, and the Echo middlewares that gate formation's routes.
package auth

import (
	"net/http"
	"slices"
	"strings"
	"unicode"

	"github.com/labstack/echo/v4"
)

const serviceAccountPrefix = "service-account-"

// AppRunnerRole is the realm role service accounts must hold to use the apps endpoints.
const AppRunnerRole = "app-runner"

// Claims is the subset of the Keycloak JWT payload formation uses. Pointer
// fields distinguish absent claims so /user can emit JSON nulls like Python.
type Claims struct {
	Sub               string  `json:"sub"`
	PreferredUsername *string `json:"preferred_username"`
	Email             *string `json:"email"`
	Name              *string `json:"name"`
	RealmAccess       struct {
		Roles []string `json:"roles"`
	} `json:"realm_access"`
}

// IsServiceAccount reports whether the token belongs to a Keycloak service account.
func (c *Claims) IsServiceAccount() bool {
	return c.PreferredUsername != nil && strings.HasPrefix(*c.PreferredUsername, serviceAccountPrefix)
}

// Username returns preferred_username, falling back to sub; it errors with the
// same 401 the Python version raised when neither claim is usable.
func (c *Claims) Username() (string, error) {
	if c.PreferredUsername != nil && *c.PreferredUsername != "" {
		return *c.PreferredUsername, nil
	}
	if c.Sub != "" {
		return c.Sub, nil
	}
	return "", echo.NewHTTPError(http.StatusUnauthorized, "Unable to determine user identity")
}

// HasRole reports whether the token carries the given realm role.
func (c *Claims) HasRole(role string) bool {
	return slices.Contains(c.RealmAccess.Roles, role)
}

// SanitizeUsername strips a username down to lowercase alphanumerics for
// backend compatibility (e.g. "de-service-account" -> "deserviceaccount").
// Like Python's str.isalnum it keeps Unicode letters and numbers.
func SanitizeUsername(username string) string {
	var b strings.Builder
	for _, r := range username {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsNumber(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}
