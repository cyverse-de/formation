// Package authtest provides a fake Keycloak server for tests: OIDC discovery,
// a JWKS endpoint backed by a generated RSA key, token signing, and a
// configurable password-grant token endpoint.
package authtest

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"maps"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const keyID = "test-key"

// Keycloak is a fake Keycloak instance. TokenHandler, when set, serves the
// password-grant token endpoint.
type Keycloak struct {
	Server       *httptest.Server
	Key          *rsa.PrivateKey
	Realm        string
	TokenHandler http.HandlerFunc
}

// New starts a fake Keycloak for the realm; it is shut down via t.Cleanup.
func New(t *testing.T, realm string) *Keycloak {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	k := &Keycloak{Key: key, Realm: realm}

	mux := http.NewServeMux()
	mux.HandleFunc("/realms/"+realm+"/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"issuer":   k.Issuer(),
			"jwks_uri": k.Server.URL + "/realms/" + realm + "/protocol/openid-connect/certs",
		})
	})
	mux.HandleFunc("/realms/"+realm+"/protocol/openid-connect/certs", func(w http.ResponseWriter, r *http.Request) {
		pub := &key.PublicKey
		writeJSON(w, map[string]any{
			"keys": []map[string]any{{
				"kty": "RSA",
				"kid": keyID,
				"use": "sig",
				"alg": "RS256",
				"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
			}},
		})
	})
	mux.HandleFunc("/realms/"+realm+"/protocol/openid-connect/token", func(w http.ResponseWriter, r *http.Request) {
		if k.TokenHandler != nil {
			k.TokenHandler(w, r)
			return
		}
		http.Error(w, "no token handler configured", http.StatusInternalServerError)
	})

	k.Server = httptest.NewServer(mux)
	t.Cleanup(k.Server.Close)
	return k
}

// ServerURL returns the base URL with a trailing slash, as config.Load would.
func (k *Keycloak) ServerURL() string {
	return k.Server.URL + "/"
}

// Issuer returns the issuer string Keycloak would put in tokens.
func (k *Keycloak) Issuer() string {
	return k.Server.URL + "/realms/" + k.Realm
}

// Token signs an RS256 JWT with the fake realm key. Issuer and expiry default
// to valid values; pass explicit "iss"/"exp" claims to override them.
func (k *Keycloak) Token(t *testing.T, claims map[string]any) string {
	t.Helper()

	merged := jwt.MapClaims{
		"iss": k.Issuer(),
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Add(-time.Minute).Unix(),
	}
	maps.Copy(merged, claims)

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, merged)
	token.Header["kid"] = keyID
	signed, err := token.SignedString(k.Key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

// TokenExchange records the impersonation tokens issued by ServeTokenExchange.
type TokenExchange struct {
	mu     sync.Mutex
	issued map[string]string
}

// IssuedFor returns the token issued for a requested_subject, or "" if the
// subject was never exchanged.
func (x *TokenExchange) IssuedFor(subject string) string {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.issued[subject]
}

// ServeTokenExchange installs a TokenHandler implementing the RFC 8693
// token-exchange grant: it signs a user token for the requested_subject and
// records it for assertions.
func (k *Keycloak) ServeTokenExchange(t *testing.T) *TokenExchange {
	t.Helper()

	exchange := &TokenExchange{issued: map[string]string{}}
	k.TokenHandler = func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if got := r.Form.Get("grant_type"); got != "urn:ietf:params:oauth:grant-type:token-exchange" {
			http.Error(w, "unexpected grant_type "+got, http.StatusBadRequest)
			return
		}
		if r.Form.Get("subject_token") == "" {
			http.Error(w, "missing subject_token", http.StatusBadRequest)
			return
		}
		subject := r.Form.Get("requested_subject")
		token := k.Token(t, map[string]any{"preferred_username": subject})

		exchange.mu.Lock()
		exchange.issued[subject] = token
		exchange.mu.Unlock()

		writeJSON(w, map[string]any{"access_token": token, "expires_in": 300})
	}
	return exchange
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
