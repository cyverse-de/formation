package main

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/cyverse-de/formation/internal/config"
	"github.com/cyverse-de/formation/internal/mcpserver"
)

// oidcStub serves OIDC discovery + JWKS for realm "cyverse" and signs tokens.
type oidcStub struct {
	server *httptest.Server
	key    *rsa.PrivateKey
}

func newOIDCStub(t *testing.T) *oidcStub {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	s := &oidcStub{key: key}
	mux := http.NewServeMux()
	mux.HandleFunc("/realms/cyverse/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		issuer := "http://" + r.Host + "/realms/cyverse"
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 issuer,
			"jwks_uri":               issuer + "/protocol/openid-connect/certs",
			"authorization_endpoint": issuer + "/protocol/openid-connect/auth",
			"token_endpoint":         issuer + "/protocol/openid-connect/token",
		})
	})
	mux.HandleFunc("/realms/cyverse/protocol/openid-connect/certs", func(w http.ResponseWriter, _ *http.Request) {
		pub := key.PublicKey
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{{
			"kty": "RSA", "kid": "k1", "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
		}}})
	})
	s.server = httptest.NewServer(mux)
	return s
}

func (s *oidcStub) token(t *testing.T) string {
	t.Helper()
	claims := jwt.MapClaims{
		"iss":                s.server.URL + "/realms/cyverse",
		"preferred_username": "alice",
		"exp":                time.Now().Add(time.Hour).Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = "k1"
	signed, err := tok.SignedString(s.key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func TestHandlerEndToEnd(t *testing.T) {
	oidc := newOIDCStub(t)
	defer oidc.server.Close()

	cfg := &config.Config{
		KeycloakServerURL: oidc.server.URL + "/",
		KeycloakRealm:     "cyverse",
		PublicBaseURL:     "https://formation.example.org",
		PathPrefix:        "",
		HTTPTimeout:       5 * time.Second,
	}
	srv := mcpserver.New(&mcpserver.Deps{Version: "test"})
	handler := buildHandler(cfg, oidc.server.Client(), srv, slog.Default())

	ts := httptest.NewServer(handler)
	defer ts.Close()
	client := ts.Client()

	t.Run("health", func(t *testing.T) {
		resp, err := client.Get(ts.URL + "/")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 || !strings.Contains(string(body), "formation") {
			t.Errorf("health: status %d body %q", resp.StatusCode, body)
		}
	})

	t.Run("protected resource metadata", func(t *testing.T) {
		resp, err := client.Get(ts.URL + "/.well-known/oauth-protected-resource")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var m map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&m)
		if m["resource"] != "https://formation.example.org/mcp" {
			t.Errorf("resource = %v", m["resource"])
		}
		servers, _ := m["authorization_servers"].([]any)
		if len(servers) != 1 || !strings.Contains(servers[0].(string), "/realms/cyverse") {
			t.Errorf("authorization_servers = %v", m["authorization_servers"])
		}
	})

	t.Run("mcp without token returns 401 challenge", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}
		challenge := resp.Header.Get("WWW-Authenticate")
		if !strings.Contains(challenge, "resource_metadata=") {
			t.Errorf("WWW-Authenticate = %q", challenge)
		}
	})

	// TestHealthReachableWithPathPrefix guards the k8s probe path: probes GET /
	// directly (no ingress prefix), so the health check must work both bare and
	// under the configured prefix.
	t.Run("health reachable with path prefix", func(t *testing.T) {
		pcfg := &config.Config{
			KeycloakServerURL: oidc.server.URL + "/",
			KeycloakRealm:     "cyverse",
			PublicBaseURL:     "https://formation.example.org",
			PathPrefix:        "/formation",
			HTTPTimeout:       5 * time.Second,
		}
		ph := buildHandler(pcfg, oidc.server.Client(), srv, slog.Default())
		pts := httptest.NewServer(ph)
		defer pts.Close()

		for path, want := range map[string]int{"/": 200, "/formation/": 200, "/bogus": 404} {
			resp, err := pts.Client().Get(pts.URL + path)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != want {
				t.Errorf("GET %s = %d, want %d", path, resp.StatusCode, want)
			}
		}
	})

	t.Run("mcp with valid token passes auth", func(t *testing.T) {
		initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(initialize))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+oidc.token(t))
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode == http.StatusUnauthorized {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("authenticated request rejected: %s", body)
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("status = %d, body %s", resp.StatusCode, body)
		}
	})
}
