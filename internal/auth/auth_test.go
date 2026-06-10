package auth_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/cyverse-de/formation/internal/apierror"
	"github.com/cyverse-de/formation/internal/auth"
	"github.com/cyverse-de/formation/internal/authtest"
)

func TestSanitizeUsername(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"de-service-account", "deserviceaccount"},
		{"app-runner", "apprunner"},
		{"Service123_Account", "service123account"},
		{"", ""},
		{"---", ""},
		{"UPPER", "upper"},
	}
	for _, tt := range tests {
		if got := auth.SanitizeUsername(tt.in); got != tt.want {
			t.Errorf("SanitizeUsername(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestUsernameForBackend(t *testing.T) {
	preferred := "alice"
	tests := []struct {
		name    string
		info    *auth.Info
		mapping map[string]string
		want    string
		wantErr bool
	}{
		{
			name:    "service account with mapping",
			info:    &auth.Info{Type: auth.TypeServiceAccount, Claims: &auth.Claims{}},
			mapping: map[string]string{"app-runner": "de-service-account"},
			want:    "deserviceaccount",
		},
		{
			name: "service account without mapping falls back to role name",
			info: &auth.Info{Type: auth.TypeServiceAccount, Claims: &auth.Claims{}},
			want: "apprunner",
		},
		{
			name: "regular user uses preferred_username",
			info: &auth.Info{Type: auth.TypeUser, Claims: &auth.Claims{PreferredUsername: &preferred}},
			want: "alice",
		},
		{
			name: "regular user falls back to sub",
			info: &auth.Info{Type: auth.TypeUser, Claims: &auth.Claims{Sub: "uuid-123"}},
			want: "uuid-123",
		},
		{
			name:    "no identity errors",
			info:    &auth.Info{Type: auth.TypeUser, Claims: &auth.Claims{}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.info.UsernameForBackend(tt.mapping)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("UsernameForBackend() = %q, want %q", got, tt.want)
			}
		})
	}
}

// testServer builds an echo instance with both middlewares mounted.
func testServer(v *auth.Verifier, serviceAccountsOnly bool) *echo.Echo {
	e := echo.New()
	e.HTTPErrorHandler = apierror.HTTPErrorHandler
	handler := func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]any{"type": auth.GetInfo(c).Type})
	}
	e.GET("/user-only", handler, auth.RequireUser(v))
	e.GET("/user-or-sa", handler, auth.RequireUserOrServiceAccount(v, serviceAccountsOnly))
	return e
}

func request(e *echo.Echo, path, authHeader string) (int, map[string]any) {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if authHeader != "" {
		req.Header.Set(echo.HeaderAuthorization, authHeader)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

func TestMiddlewares(t *testing.T) {
	kc := authtest.New(t, "de")
	verifier := auth.NewVerifier(kc.ServerURL(), "de", true)

	userToken := kc.Token(t, map[string]any{"preferred_username": "alice", "sub": "u-1"})
	saToken := kc.Token(t, map[string]any{
		"preferred_username": "service-account-formation",
		"realm_access":       map[string]any{"roles": []string{"app-runner"}},
	})
	saNoRoleToken := kc.Token(t, map[string]any{
		"preferred_username": "service-account-formation",
		"realm_access":       map[string]any{"roles": []string{"other"}},
	})
	expiredToken := kc.Token(t, map[string]any{
		"preferred_username": "alice",
		"exp":                time.Now().Add(-time.Hour).Unix(),
	})
	badIssuerToken := kc.Token(t, map[string]any{
		"preferred_username": "alice",
		"iss":                "https://evil.example.org/realms/de",
	})

	tests := []struct {
		name       string
		path       string
		saOnly     bool
		header     string
		wantStatus int
		wantType   string
		wantDetail string // substring match
	}{
		{name: "valid user token on user-only", path: "/user-only", header: "Bearer " + userToken, wantStatus: 200, wantType: "user"},
		{name: "service account passes user-only as user", path: "/user-only", header: "Bearer " + saToken, wantStatus: 200, wantType: "user"},
		{name: "missing header", path: "/user-only", header: "", wantStatus: 403, wantDetail: "Not authenticated"},
		{name: "wrong scheme", path: "/user-only", header: "Basic dXNlcjpwYXNz", wantStatus: 403, wantDetail: "Invalid authentication credentials"},
		{name: "bearer without token", path: "/user-only", header: "Bearer", wantStatus: 403, wantDetail: "Not authenticated"},
		{name: "lowercase bearer scheme accepted", path: "/user-only", header: "bearer " + userToken, wantStatus: 200, wantType: "user"},
		{name: "garbage token", path: "/user-only", header: "Bearer abc.def.ghi", wantStatus: 401, wantDetail: "Token validation failed"},
		{name: "expired token", path: "/user-only", header: "Bearer " + expiredToken, wantStatus: 401, wantDetail: "Token validation failed"},
		{name: "bad issuer", path: "/user-only", header: "Bearer " + badIssuerToken, wantStatus: 401, wantDetail: "Token validation failed"},
		{name: "user on user-or-sa", path: "/user-or-sa", header: "Bearer " + userToken, wantStatus: 200, wantType: "user"},
		{name: "service account with role", path: "/user-or-sa", header: "Bearer " + saToken, wantStatus: 200, wantType: "service_account"},
		{name: "service account missing role", path: "/user-or-sa", header: "Bearer " + saNoRoleToken, wantStatus: 403, wantDetail: `Service account missing required role: "app-runner"`},
		{name: "service accounts only rejects user", path: "/user-or-sa", saOnly: true, header: "Bearer " + userToken, wantStatus: 403, wantDetail: "Service accounts only mode"},
		{name: "service accounts only allows service account", path: "/user-or-sa", saOnly: true, header: "Bearer " + saToken, wantStatus: 200, wantType: "service_account"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, body := request(testServer(verifier, tt.saOnly), tt.path, tt.header)
			if status != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %v)", status, tt.wantStatus, body)
			}
			if tt.wantType != "" && body["type"] != tt.wantType {
				t.Errorf("identity type = %v, want %q", body["type"], tt.wantType)
			}
			if tt.wantDetail != "" {
				detail, _ := body["detail"].(string)
				if !strings.Contains(detail, tt.wantDetail) {
					t.Errorf("detail = %q, want containing %q", detail, tt.wantDetail)
				}
			}
		})
	}
}

func TestVerifierKeycloakUnreachable(t *testing.T) {
	verifier := auth.NewVerifier("http://127.0.0.1:1/", "de", true)
	status, body := request(testServer(verifier, false), "/user-only", "Bearer abc.def.ghi")
	if status != 401 {
		t.Fatalf("status = %d, want 401", status)
	}
	detail, _ := body["detail"].(string)
	if !strings.Contains(detail, "Authentication error:") {
		t.Errorf("detail = %q, want Authentication error prefix", detail)
	}
}
