package authz

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/cyverse-de/formation/internal/config"
)

// oidcTestServer is a minimal Keycloak-like OIDC provider serving discovery and
// JWKS documents and signing tokens with a generated RSA key.
type oidcTestServer struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	issuer string
}

func newOIDCTestServer(t *testing.T) *oidcTestServer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	ots := &oidcTestServer{key: key}

	const realmPath = "/realms/cyverse"
	mux := http.NewServeMux()
	mux.HandleFunc(realmPath+"/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 ots.issuer,
			"jwks_uri":               ots.issuer + "/protocol/openid-connect/certs",
			"authorization_endpoint": ots.issuer + "/protocol/openid-connect/auth",
			"token_endpoint":         ots.issuer + "/protocol/openid-connect/token",
		})
	})
	mux.HandleFunc(realmPath+"/protocol/openid-connect/certs", func(w http.ResponseWriter, _ *http.Request) {
		pub := key.PublicKey
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{
				"kty": "RSA",
				"kid": "test-key",
				"use": "sig",
				"alg": "RS256",
				"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
			}},
		})
	})

	ots.server = httptest.NewServer(mux)
	ots.issuer = ots.server.URL + realmPath
	return ots
}

func (o *oidcTestServer) close() { o.server.Close() }

func (o *oidcTestServer) sign(t *testing.T, c jwt.MapClaims) string {
	t.Helper()
	if _, ok := c["iss"]; !ok {
		c["iss"] = o.issuer
	}
	if _, ok := c["exp"]; !ok {
		c["exp"] = time.Now().Add(time.Hour).Unix()
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, c)
	tok.Header["kid"] = "test-key"
	signed, err := tok.SignedString(o.key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signed
}

func TestVerifyAndDeriveIdentity(t *testing.T) {
	ots := newOIDCTestServer(t)
	defer ots.close()

	tests := []struct {
		name        string
		saOnly      bool
		saUsernames map[string]string
		claims      jwt.MapClaims
		expired     bool
		wantErr     bool
		check       func(t *testing.T, id Identity)
	}{
		{
			name:   "regular user",
			claims: jwt.MapClaims{"preferred_username": "alice", "email": "alice@example.org"},
			check: func(t *testing.T, id Identity) {
				if id.DownstreamUsername != "alice" || id.IsServiceAccount {
					t.Errorf("got %+v", id)
				}
				if id.Email != "alice@example.org" {
					t.Errorf("email = %q", id.Email)
				}
			},
		},
		{
			name:    "regular user rejected in service-accounts-only mode",
			saOnly:  true,
			claims:  jwt.MapClaims{"preferred_username": "alice"},
			wantErr: true,
		},
		{
			name:        "service account with role mapped and sanitized",
			saUsernames: map[string]string{"app-runner": "de-service-account"},
			claims: jwt.MapClaims{
				"preferred_username": "service-account-formation",
				"realm_access":       map[string]any{"roles": []string{"app-runner", "default-roles"}},
			},
			check: func(t *testing.T, id Identity) {
				if !id.IsServiceAccount {
					t.Error("expected service account")
				}
				if id.DownstreamUsername != "deserviceaccount" {
					t.Errorf("downstream username = %q, want deserviceaccount", id.DownstreamUsername)
				}
			},
		},
		{
			name: "service account missing role rejected",
			claims: jwt.MapClaims{
				"preferred_username": "service-account-formation",
				"realm_access":       map[string]any{"roles": []string{"other"}},
			},
			wantErr: true,
		},
		{
			name:    "expired token rejected",
			claims:  jwt.MapClaims{"preferred_username": "alice"},
			expired: true,
			wantErr: true,
		},
		{
			name:   "falls back to sub when preferred_username absent",
			claims: jwt.MapClaims{"sub": "abc-123"},
			check: func(t *testing.T, id Identity) {
				if id.DownstreamUsername != "abc-123" {
					t.Errorf("downstream = %q", id.DownstreamUsername)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// KeycloakIssuer() == KeycloakServerURL + "realms/" + realm, which
			// must equal the discovery document's issuer ({server}/realms/cyverse).
			cfg := &config.Config{
				KeycloakServerURL:       ots.server.URL + "/",
				KeycloakRealm:           "cyverse",
				ServiceAccountsOnly:     tc.saOnly,
				ServiceAccountUsernames: tc.saUsernames,
			}
			v := NewVerifier(cfg, ots.server.Client())

			if tc.expired {
				tc.claims["exp"] = time.Now().Add(-time.Hour).Unix()
			}
			token := ots.sign(t, tc.claims)

			info, err := v.Verify(context.Background(), token, nil)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !errors.Is(err, auth.ErrInvalidToken) {
					t.Errorf("expected ErrInvalidToken, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Verify error: %v", err)
			}
			if info.Expiration.IsZero() {
				t.Error("expiration must be set so the middleware accepts the token")
			}
			id := FromContextInfo(info)
			tc.check(t, id)
		})
	}
}

// FromContextInfo extracts the Identity directly from a TokenInfo for testing,
// mirroring what FromContext does after the middleware stores it in context.
func FromContextInfo(info *auth.TokenInfo) Identity {
	if id, ok := info.Extra[identityKey].(Identity); ok {
		return id
	}
	return Identity{}
}

func TestSanitizeUsername(t *testing.T) {
	tests := []struct{ in, want string }{
		{"de-service-account", "deserviceaccount"},
		{"app-runner", "apprunner"},
		{"Service123_Account", "service123account"},
	}
	for _, tc := range tests {
		if got := sanitizeUsername(tc.in); got != tc.want {
			t.Errorf("sanitizeUsername(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
