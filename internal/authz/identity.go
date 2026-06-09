// Package authz verifies Keycloak bearer tokens and derives the downstream
// identity used when calling DE backend services. It implements the MCP SDK's
// TokenVerifier so the streamable-HTTP transport can act as an OAuth 2.0
// resource server, and also provides the legacy password-grant login.
package authz

import (
	"context"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

// serviceAccountPrefix identifies Keycloak service-account principals by their
// preferred_username claim.
const serviceAccountPrefix = "service-account-"

// serviceAccountRole is the realm role a service account must hold to use the
// service, and the key used to look up its mapped downstream username.
const serviceAccountRole = "app-runner"

// Identity is the resolved caller identity passed to tool handlers. For service
// accounts, DownstreamUsername is already mapped and sanitized for backend use
// and Email is empty (the Keycloak service-account address is non-routable).
type Identity struct {
	DownstreamUsername string
	Email              string
	IsServiceAccount   bool
	Roles              []string
}

// identityKey is the key under which the Identity is stored in TokenInfo.Extra.
const identityKey = "identity"

// identityCtxKey keys an Identity stored directly in a context, used by
// ContextWithIdentity (e.g. in tests) independently of the bearer middleware.
type identityCtxKey struct{}

// ContextWithIdentity returns a context carrying the given Identity.
func ContextWithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityCtxKey{}, id)
}

// IdentityFromTokenInfo extracts the Identity carried in a verified TokenInfo,
// falling back to a minimal Identity built from UserID.
func IdentityFromTokenInfo(ti *auth.TokenInfo) Identity {
	if ti == nil {
		return Identity{}
	}
	if id, ok := ti.Extra[identityKey].(Identity); ok {
		return id
	}
	return Identity{DownstreamUsername: ti.UserID}
}

// FromContext returns the caller Identity. In production it is read from the
// TokenInfo the bearer-token middleware stores; a directly-attached Identity
// (ContextWithIdentity) takes precedence.
func FromContext(ctx context.Context) Identity {
	if id, ok := ctx.Value(identityCtxKey{}).(Identity); ok {
		return id
	}
	return IdentityFromTokenInfo(auth.TokenInfoFromContext(ctx))
}

// sanitizeUsername removes all non-alphanumeric characters and lowercases the
// result, matching the original Python sanitize_username. Backend service
// allowlists use this sanitized form (e.g. "de-service-account" -> "deserviceaccount").
func sanitizeUsername(username string) string {
	out := make([]rune, 0, len(username))
	for _, r := range username {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out = append(out, unicode.ToLower(r))
		}
	}
	return string(out)
}
