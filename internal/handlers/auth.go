package handlers

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/cyverse-de/formation/internal/auth"
)

// Login authenticates with HTTP Basic credentials against Keycloak's password
// grant and proxies the token response verbatim.
//
// @Summary Log in with username and password
// @Description Exchanges HTTP Basic credentials for a Keycloak token using the password grant.
// @Description Use the Authorize dialog's BasicAuth fields, run this endpoint, then paste the
// @Description access_token from the response into the BearerAuth field to call the other endpoints.
// @Tags Authentication
// @Security BasicAuth
// @Produce json
// @Success 200 {object} map[string]interface{} "Keycloak token response (access_token, refresh_token, expires_in, ...)"
// @Failure 401 {object} map[string]interface{} "Invalid credentials"
// @Failure 500 {object} map[string]interface{} "Authentication service error"
// @Router /login [post]
func Login(kc *auth.Keycloak) echo.HandlerFunc {
	return func(c echo.Context) error {
		username, password, err := basicCredentials(c)
		if err != nil {
			return err
		}

		token, err := kc.PasswordGrant(c.Request().Context(), username, password)
		if err != nil {
			var statusErr *auth.HTTPStatusError
			if errors.As(err, &statusErr) {
				if statusErr.StatusCode == http.StatusUnauthorized {
					return echo.NewHTTPError(http.StatusUnauthorized, "Invalid credentials")
				}
				return echo.NewHTTPError(http.StatusInternalServerError, "Authentication service error")
			}
			return echo.NewHTTPError(http.StatusInternalServerError, "Login failed: "+err.Error())
		}

		return c.JSON(http.StatusOK, token)
	}
}

// basicCredentials parses HTTP Basic auth, reproducing FastAPI HTTPBasic's
// responses: 401 "Not authenticated" for a missing header or non-Basic scheme,
// 401 "Invalid authentication credentials" for undecodable credentials.
func basicCredentials(c echo.Context) (string, string, error) {
	header := c.Request().Header.Get(echo.HeaderAuthorization)
	scheme, encoded, _ := strings.Cut(header, " ")
	if header == "" || encoded == "" || !strings.EqualFold(scheme, "Basic") {
		return "", "", echo.NewHTTPError(http.StatusUnauthorized, "Not authenticated")
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", "", echo.NewHTTPError(http.StatusUnauthorized, "Invalid authentication credentials")
	}
	username, password, found := strings.Cut(string(decoded), ":")
	if !found {
		return "", "", echo.NewHTTPError(http.StatusUnauthorized, "Invalid authentication credentials")
	}
	return username, password, nil
}

// UserInfo returns the authenticated user's profile from the JWT claims.
//
// @Summary Get the authenticated user's profile
// @Tags Authentication
// @Security BearerAuth
// @Produce json
// @Success 200 {object} map[string]interface{} "username, email, name, preferred_username"
// @Failure 401 {object} map[string]interface{} "Invalid or missing token"
// @Router /user [get]
func UserInfo(c echo.Context) error {
	claims := auth.GetInfo(c).Claims
	username, err := claims.Username()
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{
		"username":           username,
		"email":              claims.Email,
		"name":               claims.Name,
		"preferred_username": claims.PreferredUsername,
	})
}
