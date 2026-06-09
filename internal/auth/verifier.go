package auth

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
)

// Verifier validates Keycloak bearer tokens: RS256 signature against the
// realm JWKS, issuer, and expiry. The audience check is skipped to match the
// Python implementation.
type Verifier struct {
	issuer    string
	sslVerify bool

	mu       sync.Mutex
	verifier *oidc.IDTokenVerifier
}

// NewVerifier prepares a verifier for the realm. The OIDC provider is
// discovered lazily on first use so formation can start while Keycloak is
// unreachable, mirroring Python's per-request JWKS fetch; once discovered,
// go-oidc caches and rotates the keyset automatically.
func NewVerifier(serverURL, realm string, sslVerify bool) *Verifier {
	return &Verifier{
		issuer:    strings.TrimSuffix(serverURL, "/") + "/realms/" + realm,
		sslVerify: sslVerify,
	}
}

// Verify checks the raw bearer token and returns its claims.
func (v *Verifier) Verify(ctx context.Context, rawToken string) (*Claims, error) {
	verifier, err := v.idTokenVerifier(ctx)
	if err != nil {
		return nil, &DiscoveryError{Err: err}
	}

	token, err := verifier.Verify(ctx, rawToken)
	if err != nil {
		return nil, err
	}

	var claims Claims
	if err := token.Claims(&claims); err != nil {
		return nil, err
	}
	return &claims, nil
}

func (v *Verifier) idTokenVerifier(ctx context.Context) (*oidc.IDTokenVerifier, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.verifier != nil {
		return v.verifier, nil
	}

	provider, err := oidc.NewProvider(oidc.ClientContext(ctx, newHTTPClient(v.sslVerify)), v.issuer)
	if err != nil {
		return nil, fmt.Errorf("discovering OIDC provider %s: %w", v.issuer, err)
	}
	v.verifier = provider.Verifier(&oidc.Config{
		SkipClientIDCheck:    true,
		SupportedSigningAlgs: []string{oidc.RS256},
	})
	return v.verifier, nil
}

// DiscoveryError marks failures reaching Keycloak (as opposed to an invalid
// token) so the middleware can use Python's "Authentication error:" wording.
type DiscoveryError struct {
	Err error
}

func (e *DiscoveryError) Error() string { return e.Err.Error() }
func (e *DiscoveryError) Unwrap() error { return e.Err }
