package authz

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/cyverse-de/formation/internal/config"
)

// Verifier validates Keycloak bearer tokens against the realm's JWKS and
// derives the downstream Identity. The OIDC provider (and its auto-refreshing
// key set) is initialized lazily so the server can start before Keycloak is
// reachable.
type Verifier struct {
	cfg        *config.Config
	httpClient *http.Client
	logger     *slog.Logger

	mu       sync.Mutex
	verifier *oidc.IDTokenVerifier
}

// NewVerifier constructs a Verifier. The http client is used for OIDC discovery
// and JWKS retrieval (and should carry any required TLS settings).
func NewVerifier(cfg *config.Config, httpClient *http.Client, logger *slog.Logger) *Verifier {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Verifier{cfg: cfg, httpClient: httpClient, logger: logger}
}

// claims captures the Keycloak token fields Formation relies on.
type claims struct {
	PreferredUsername string `json:"preferred_username"`
	Subject           string `json:"sub"`
	Email             string `json:"email"`
	RealmAccess       struct {
		Roles []string `json:"roles"`
	} `json:"realm_access"`
}

// Verify implements auth.TokenVerifier. On success it returns a TokenInfo whose
// Extra carries the derived Identity and whose Expiration is taken from the
// token so the middleware's expiry check passes.
func (v *Verifier) Verify(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
	idVerifier, err := v.idTokenVerifier(ctx)
	if err != nil {
		// A server-side failure to reach Keycloak is not the caller's fault;
		// surfacing a non-ErrInvalidToken error yields a 500 rather than 401.
		// The middleware echoes this error's text into the response body, so log
		// the real error (which embeds internal URLs) and return a generic one.
		v.logger.Error("OIDC discovery failed; this usually means Keycloak is unreachable or the issuer URL is misconfigured", "issuer", v.cfg.KeycloakIssuer(), "error", err)
		return nil, errors.New("authentication service unavailable")
	}

	idToken, err := idVerifier.Verify(oidc.ClientContext(ctx, v.httpClient), token)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", auth.ErrInvalidToken, err)
	}

	var c claims
	if err := idToken.Claims(&c); err != nil {
		return nil, fmt.Errorf("%w: cannot parse token claims: %v", auth.ErrInvalidToken, err)
	}

	identity, err := v.deriveIdentity(c)
	if err != nil {
		return nil, err
	}

	return &auth.TokenInfo{
		UserID:     identity.DownstreamUsername,
		Expiration: idToken.Expiry,
		Scopes:     identity.Roles,
		Extra:      map[string]any{identityKey: identity},
	}, nil
}

// deriveIdentity folds the original auth.py + dependencies.py logic: it
// classifies the principal, enforces the service-account role, and resolves the
// downstream username.
//
// The MCP bearer middleware can only emit 403 via global required scopes (which
// would reject regular users), so an unauthorized service account is rejected
// here as an invalid token (401). The token is genuine; it simply lacks the
// authorization this resource requires.
func (v *Verifier) deriveIdentity(c claims) (Identity, error) {
	roles := c.RealmAccess.Roles

	if isServiceAccountUsername(c.PreferredUsername) {
		if !slices.Contains(roles, serviceAccountRole) {
			return Identity{}, fmt.Errorf("%w: service account missing required role %q", auth.ErrInvalidToken, serviceAccountRole)
		}
		mapped := serviceAccountRole
		if u, ok := v.cfg.ServiceAccountUsernames[serviceAccountRole]; ok {
			mapped = u
		}
		// Service-account tokens carry a non-routable Keycloak email; leave
		// Email empty so consumers fall back to the mapped user's address.
		return Identity{
			DownstreamUsername: sanitizeUsername(mapped),
			IsServiceAccount:   true,
			Roles:              roles,
		}, nil
	}

	if v.cfg.ServiceAccountsOnly {
		return Identity{}, fmt.Errorf("%w: regular user authentication is disabled", auth.ErrInvalidToken)
	}

	username := c.PreferredUsername
	if username == "" {
		username = c.Subject
	}
	if username == "" {
		return Identity{}, fmt.Errorf("%w: unable to determine user identity", auth.ErrInvalidToken)
	}
	return Identity{
		DownstreamUsername: username,
		Email:              c.Email,
		Roles:              roles,
	}, nil
}

// idTokenVerifier lazily initializes (and caches) the OIDC verifier for the
// configured realm issuer. OIDC discovery (a network call) is performed without
// holding the lock so concurrent callers are not serialized, and a transient
// failure is not cached.
func (v *Verifier) idTokenVerifier(ctx context.Context) (*oidc.IDTokenVerifier, error) {
	v.mu.Lock()
	cached := v.verifier
	v.mu.Unlock()
	if cached != nil {
		return cached, nil
	}

	provider, err := oidc.NewProvider(oidc.ClientContext(ctx, v.httpClient), v.cfg.KeycloakIssuer())
	if err != nil {
		return nil, err
	}
	// SkipClientIDCheck mirrors the Python verify_aud=False: Keycloak access
	// tokens carry varying audiences, so only signature, issuer, and expiry
	// are enforced.
	verifier := provider.Verifier(&oidc.Config{SkipClientIDCheck: true})

	v.mu.Lock()
	if v.verifier == nil {
		v.verifier = verifier
	}
	cached = v.verifier
	v.mu.Unlock()
	return cached, nil
}

func isServiceAccountUsername(preferredUsername string) bool {
	return strings.HasPrefix(preferredUsername, serviceAccountPrefix)
}
