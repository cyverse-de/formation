package auth

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

// newHTTPClient returns a client with an explicit timeout; sslVerify=false
// disables certificate checks like the Python verify=False option.
func newHTTPClient(sslVerify bool) *http.Client {
	client := &http.Client{Timeout: 30 * time.Second}
	if !sslVerify {
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // #nosec G402 -- mirrors keycloak.ssl_verify config
		}
	}
	return client
}

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
