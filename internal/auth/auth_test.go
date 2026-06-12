package auth_test

import (
	"errors"
	"testing"
	"time"

	"github.com/cyverse-de/formation/internal/auth"
	"github.com/cyverse-de/formation/internal/authtest"
)

func TestVerifierVerify(t *testing.T) {
	kc := authtest.New(t, "de")
	verifier := auth.NewVerifier(kc.ServerURL(), "de", true)

	tests := []struct {
		name    string
		token   string
		wantErr bool
	}{
		{
			name:  "valid token",
			token: kc.Token(t, map[string]any{"preferred_username": "alice", "sub": "u-1"}),
		},
		{
			name:    "garbage token",
			token:   "abc.def.ghi",
			wantErr: true,
		},
		{
			name: "expired token",
			token: kc.Token(t, map[string]any{
				"preferred_username": "alice",
				"exp":                time.Now().Add(-time.Hour).Unix(),
			}),
			wantErr: true,
		},
		{
			name: "bad issuer",
			token: kc.Token(t, map[string]any{
				"preferred_username": "alice",
				"iss":                "https://evil.example.org/realms/de",
			}),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims, err := verifier.Verify(t.Context(), tt.token)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected a verification error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			username, err := claims.Username()
			if err != nil || username != "alice" {
				t.Errorf("Username() = %q, %v, want alice", username, err)
			}
		})
	}
}

// Keycloak being unreachable must surface as a DiscoveryError, not an
// invalid-token error, so callers can report a server-side failure.
func TestVerifierKeycloakUnreachable(t *testing.T) {
	verifier := auth.NewVerifier("http://127.0.0.1:1/", "de", true)
	_, err := verifier.Verify(t.Context(), "abc.def.ghi")
	var discoveryErr *auth.DiscoveryError
	if !errors.As(err, &discoveryErr) {
		t.Fatalf("error = %v, want *auth.DiscoveryError", err)
	}
}

func TestUserCaller(t *testing.T) {
	preferred := "alice"
	serviceAccount := "service-account-formation"

	tests := []struct {
		name             string
		claims           *auth.Claims
		wantUsername     string
		wantErr          bool
		isServiceAccount bool
	}{
		{
			name:         "preferred_username wins",
			claims:       &auth.Claims{PreferredUsername: &preferred, Sub: "u-1"},
			wantUsername: "alice",
		},
		{
			name:         "falls back to sub",
			claims:       &auth.Claims{Sub: "u-1"},
			wantUsername: "u-1",
		},
		{
			name:    "no identity errors",
			claims:  &auth.Claims{},
			wantErr: true,
		},
		{
			name:             "service-account prefix detected",
			claims:           &auth.Claims{PreferredUsername: &serviceAccount},
			wantUsername:     serviceAccount,
			isServiceAccount: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.claims.IsServiceAccount(); got != tt.isServiceAccount {
				t.Errorf("IsServiceAccount() = %v, want %v", got, tt.isServiceAccount)
			}

			caller, err := auth.UserCaller(tt.claims, "tok")
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if caller.Username != tt.wantUsername || caller.Token != "tok" {
				t.Errorf("caller = %+v, want username %q token %q", caller, tt.wantUsername, "tok")
			}
		})
	}
}
