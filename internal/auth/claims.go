// Package auth implements Keycloak OIDC token verification and the caller
// identity forwarded to terrain.
package auth

import (
	"errors"
	"strings"
	"time"
)

const serviceAccountPrefix = "service-account-"

// Claims is the subset of the Keycloak JWT payload formation uses. Pointer
// fields distinguish absent claims from empty ones.
type Claims struct {
	Sub               string  `json:"sub"`
	PreferredUsername *string `json:"preferred_username"`
	Email             *string `json:"email"`
	Name              *string `json:"name"`
	GivenName         *string `json:"given_name"`
	FamilyName        *string `json:"family_name"`
	Exp               int64   `json:"exp"`
}

// Expiry returns the token expiration time from the exp claim.
func (c *Claims) Expiry() time.Time {
	return time.Unix(c.Exp, 0)
}

// IsServiceAccount reports whether the token belongs to a Keycloak service account.
func (c *Claims) IsServiceAccount() bool {
	return c.PreferredUsername != nil && strings.HasPrefix(*c.PreferredUsername, serviceAccountPrefix)
}

// DisplayName returns the user's display name: the name claim, falling back
// to given_name + family_name, or "" when the token carries neither.
func (c *Claims) DisplayName() string {
	if c.Name != nil && *c.Name != "" {
		return *c.Name
	}
	var parts []string
	for _, p := range []*string{c.GivenName, c.FamilyName} {
		if p != nil && *p != "" {
			parts = append(parts, *p)
		}
	}
	return strings.Join(parts, " ")
}

// EmailAddress returns the email claim, or "" when absent.
func (c *Claims) EmailAddress() string {
	if c.Email != nil {
		return *c.Email
	}
	return ""
}

// Username returns preferred_username, falling back to sub.
func (c *Claims) Username() (string, error) {
	if c.PreferredUsername != nil && *c.PreferredUsername != "" {
		return *c.PreferredUsername, nil
	}
	if c.Sub != "" {
		return c.Sub, nil
	}
	return "", errors.New("unable to determine user identity")
}
