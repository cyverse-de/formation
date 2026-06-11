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

// claimsExtraKey and tokenExtraKey carry the verified *auth.Claims and the raw
// bearer token through TokenInfo.Extra from the bearer-token middleware to the
// tool handlers; the token is forwarded on calls to terrain.
const (
	claimsExtraKey = "formation.claims"
	tokenExtraKey  = "formation.token"
)

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
			Extra:      map[string]any{claimsExtraKey: claims, tokenExtraKey: token},
		}, nil
	}
}

// requestIdentity returns the verified token claims and the raw bearer token
// for a tool call.
func requestIdentity(req *sdk.CallToolRequest) (*auth.Claims, string, error) {
	if req.Extra != nil && req.Extra.TokenInfo != nil {
		claims, ok := req.Extra.TokenInfo.Extra[claimsExtraKey].(*auth.Claims)
		token, _ := req.Extra.TokenInfo.Extra[tokenExtraKey].(string)
		if ok {
			return claims, token, nil
		}
	}
	return nil, "", errors.New("not authenticated")
}

// appsIdentity resolves the identity for the apps/analyses tools, mirroring
// the REST RequireUserOrServiceAccount policy: service accounts need the
// app-runner realm role, and service-accounts-only mode rejects users.
func (s *server) appsIdentity(req *sdk.CallToolRequest) (*auth.Info, error) {
	claims, token, err := requestIdentity(req)
	if err != nil {
		return nil, err
	}

	if claims.IsServiceAccount() {
		if !claims.HasRole(auth.AppRunnerRole) {
			return nil, fmt.Errorf("service account missing required role: %q", auth.AppRunnerRole)
		}
		return &auth.Info{Type: auth.TypeServiceAccount, Claims: claims, Token: token}, nil
	}

	if s.cfg.ServiceAccountsOnly {
		return nil, errors.New("service accounts only mode: regular user authentication is disabled")
	}
	return &auth.Info{Type: auth.TypeUser, Claims: claims, Token: token}, nil
}

// appsCaller resolves the terrain-ready caller for the apps/analyses tools.
func (s *server) appsCaller(ctx context.Context, req *sdk.CallToolRequest) (*auth.Caller, error) {
	info, err := s.appsIdentity(req)
	if err != nil {
		return nil, err
	}
	return s.apps.ResolveCaller(ctx, info)
}

// dataCaller resolves the identity for the data tools, mirroring the REST
// RequireUser policy: any valid token acts as a user under its JWT username.
func dataCaller(req *sdk.CallToolRequest) (*auth.Caller, error) {
	claims, token, err := requestIdentity(req)
	if err != nil {
		return nil, err
	}
	return auth.UserCaller(&auth.Info{Type: auth.TypeUser, Claims: claims, Token: token})
}
