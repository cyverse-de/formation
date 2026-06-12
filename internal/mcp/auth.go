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
// /mcp accepts the realm's user tokens.
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
		// Service-account tokens would be forwarded to terrain under a bogus
		// "service-account-*" username and fail far from the cause, so reject
		// them here with a clear error instead.
		if claims.IsServiceAccount() {
			return nil, fmt.Errorf("%w: service accounts are not supported; connect with a user account via OAuth", sdkauth.ErrInvalidToken)
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

// requestIdentity returns the verified JWT claims and bearer token for a tool call.
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

// requestCaller returns the terrain-ready caller for a tool call: the verified
// user's JWT username plus their own bearer token, forwarded as-is.
func requestCaller(req *sdk.CallToolRequest) (*auth.Caller, error) {
	claims, token, err := requestIdentity(req)
	if err != nil {
		return nil, err
	}
	return auth.UserCaller(claims, token)
}
