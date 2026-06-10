package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cyverse-de/formation/internal/auth"
)

// claimsExtraKey carries the verified *auth.Claims through TokenInfo.Extra
// from the bearer-token middleware to the tool handlers.
const claimsExtraKey = "formation.claims"

// tokenVerifier adapts the Keycloak verifier to the SDK's TokenVerifier so
// /mcp accepts the same realm tokens as the REST endpoints.
func tokenVerifier(v *auth.Verifier) sdkauth.TokenVerifier {
	return func(ctx context.Context, token string, _ *http.Request) (*sdkauth.TokenInfo, error) {
		claims, err := v.Verify(ctx, token)
		if err != nil {
			var discoveryErr *auth.DiscoveryError
			if errors.As(err, &discoveryErr) {
				// Keycloak being unreachable is a server-side failure, not an
				// invalid token; a 500 here avoids sending clients into a
				// futile re-authentication loop.
				return nil, fmt.Errorf("authentication error: %w", err)
			}
			return nil, fmt.Errorf("%w: %s", sdkauth.ErrInvalidToken, err)
		}
		userID, err := claims.Username()
		if err != nil {
			return nil, fmt.Errorf("%w: unable to determine user identity", sdkauth.ErrInvalidToken)
		}
		return &sdkauth.TokenInfo{
			UserID:     userID,
			Expiration: claims.Expiry(),
			Extra:      map[string]any{claimsExtraKey: claims},
		}, nil
	}
}

// requestClaims returns the verified token claims for a tool call.
func requestClaims(req *sdk.CallToolRequest) (*auth.Claims, error) {
	if req.Extra != nil && req.Extra.TokenInfo != nil {
		if claims, ok := req.Extra.TokenInfo.Extra[claimsExtraKey].(*auth.Claims); ok {
			return claims, nil
		}
	}
	return nil, errors.New("not authenticated")
}

// appsIdentity resolves the identity for the apps/analyses tools, mirroring
// the REST RequireUserOrServiceAccount policy: service accounts need the
// app-runner realm role, and service-accounts-only mode rejects users.
func (s *server) appsIdentity(req *sdk.CallToolRequest) (*auth.Info, error) {
	claims, err := requestClaims(req)
	if err != nil {
		return nil, err
	}

	if claims.IsServiceAccount() {
		if !claims.HasRole(auth.AppRunnerRole) {
			return nil, fmt.Errorf("service account missing required role: %q", auth.AppRunnerRole)
		}
		return &auth.Info{Type: auth.TypeServiceAccount, Claims: claims}, nil
	}

	if s.cfg.ServiceAccountsOnly {
		return nil, errors.New("service accounts only mode: regular user authentication is disabled")
	}
	return &auth.Info{Type: auth.TypeUser, Claims: claims}, nil
}

// appsUsername resolves the backend username for the apps/analyses tools.
func (s *server) appsUsername(req *sdk.CallToolRequest) (*auth.Info, string, error) {
	info, err := s.appsIdentity(req)
	if err != nil {
		return nil, "", err
	}
	username, err := info.UsernameForBackend(s.cfg.ServiceAccountUsernames)
	if err != nil {
		return nil, "", err
	}
	return info, username, nil
}

// dataUsername resolves the identity for the data tools, mirroring the REST
// RequireUser policy: any valid token acts as a user under its JWT username.
func dataUsername(req *sdk.CallToolRequest) (string, error) {
	claims, err := requestClaims(req)
	if err != nil {
		return "", err
	}
	return claims.Username()
}
