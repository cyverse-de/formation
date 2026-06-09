package auth

import (
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
)

const infoContextKey = "formation.authInfo"

// Auth type discriminators, matching the Python auth_info "type" values.
const (
	TypeUser           = "user"
	TypeServiceAccount = "service_account"
)

// Info is the authenticated identity stored on the Echo context.
type Info struct {
	Type   string
	Claims *Claims
}

// GetInfo returns the identity placed on the context by the auth middlewares.
func GetInfo(c echo.Context) *Info {
	info, _ := c.Get(infoContextKey).(*Info)
	return info
}

// UsernameForBackend returns the username passed to backend services: the
// configured (then sanitized) mapping for service accounts, or the JWT
// username for regular users.
func (i *Info) UsernameForBackend(serviceAccountUsernames map[string]string) (string, error) {
	if i.Type == TypeServiceAccount {
		username, ok := serviceAccountUsernames[AppRunnerRole]
		if !ok {
			username = AppRunnerRole
		}
		return SanitizeUsername(username), nil
	}
	return i.Claims.Username()
}

// bearerToken extracts the bearer token, reproducing FastAPI HTTPBearer's
// responses: 403 "Not authenticated" when the header or token is missing and
// 403 "Invalid authentication credentials" for a non-Bearer scheme.
func bearerToken(c echo.Context) (string, error) {
	header := c.Request().Header.Get(echo.HeaderAuthorization)
	scheme, token, _ := strings.Cut(header, " ")
	if header == "" || scheme == "" || token == "" {
		return "", echo.NewHTTPError(http.StatusForbidden, "Not authenticated")
	}
	if !strings.EqualFold(scheme, "Bearer") {
		return "", echo.NewHTTPError(http.StatusForbidden, "Invalid authentication credentials")
	}
	return token, nil
}

// verify maps verification failures to the Python 401 messages.
func verify(c echo.Context, v *Verifier) (*Claims, error) {
	token, err := bearerToken(c)
	if err != nil {
		return nil, err
	}
	claims, err := v.Verify(c.Request().Context(), token)
	if err != nil {
		var discoveryErr *DiscoveryError
		if errors.As(err, &discoveryErr) {
			return nil, echo.NewHTTPError(http.StatusUnauthorized, "Authentication error: "+err.Error())
		}
		return nil, echo.NewHTTPError(http.StatusUnauthorized, "Token validation failed: "+err.Error())
	}
	return claims, nil
}

// RequireUser accepts any valid token and stores the identity as a user,
// matching the Python get_current_user dependency.
func RequireUser(v *Verifier) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			claims, err := verify(c, v)
			if err != nil {
				return err
			}
			c.Set(infoContextKey, &Info{Type: TypeUser, Claims: claims})
			return next(c)
		}
	}
}

// RequireUserOrServiceAccount accepts user tokens (unless serviceAccountsOnly)
// and service-account tokens holding the app-runner realm role, matching the
// Python get_current_user_or_service_account dependency.
func RequireUserOrServiceAccount(v *Verifier, serviceAccountsOnly bool) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			claims, err := verify(c, v)
			if err != nil {
				return err
			}

			if claims.IsServiceAccount() {
				if !claims.HasRole(AppRunnerRole) {
					return echo.NewHTTPError(http.StatusForbidden,
						`Service account missing required role: "app-runner"`)
				}
				c.Set(infoContextKey, &Info{Type: TypeServiceAccount, Claims: claims})
				return next(c)
			}

			if serviceAccountsOnly {
				return echo.NewHTTPError(http.StatusForbidden,
					"Service accounts only mode: regular user authentication is disabled")
			}

			c.Set(infoContextKey, &Info{Type: TypeUser, Claims: claims})
			return next(c)
		}
	}
}
