package handlers_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/cyverse-de/formation/internal/apierror"
	"github.com/cyverse-de/formation/internal/auth"
	"github.com/cyverse-de/formation/internal/authtest"
	"github.com/cyverse-de/formation/internal/handlers"
)

func basicHeader(username, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
}

func TestLogin(t *testing.T) {
	kc := authtest.New(t, "de")
	keycloak, err := auth.NewKeycloak(kc.ServerURL(), "de", "formation", "secret", true)
	if err != nil {
		t.Fatal(err)
	}

	e := echo.New()
	e.HTTPErrorHandler = apierror.HTTPErrorHandler
	e.POST("/login", handlers.Login(keycloak))

	tokenResponse := map[string]any{
		"access_token":  "tok",
		"token_type":    "Bearer",
		"expires_in":    float64(3600),
		"refresh_token": "refresh",
	}

	tests := []struct {
		name        string
		header      string
		tokenStatus int
		wantStatus  int
		wantDetail  string
		wantBody    map[string]any
	}{
		{
			name:        "successful login proxies token verbatim",
			header:      basicHeader("alice", "goodpass"),
			tokenStatus: 200,
			wantStatus:  200,
			wantBody:    tokenResponse,
		},
		{
			name:        "keycloak 401 becomes Invalid credentials",
			header:      basicHeader("alice", "badpass"),
			tokenStatus: 401,
			wantStatus:  401,
			wantDetail:  "Invalid credentials",
		},
		{
			name:        "keycloak 500 becomes Authentication service error",
			header:      basicHeader("alice", "goodpass"),
			tokenStatus: 500,
			wantStatus:  500,
			wantDetail:  "Authentication service error",
		},
		{
			name:       "missing credentials",
			header:     "",
			wantStatus: 401,
			wantDetail: "Not authenticated",
		},
		{
			name:       "wrong scheme",
			header:     "Bearer abc",
			wantStatus: 401,
			wantDetail: "Not authenticated",
		},
		{
			name:       "invalid base64",
			header:     "Basic !!!",
			wantStatus: 401,
			wantDetail: "Invalid authentication credentials",
		},
		{
			name:       "no colon in credentials",
			header:     "Basic " + base64.StdEncoding.EncodeToString([]byte("nocolon")),
			wantStatus: 401,
			wantDetail: "Invalid authentication credentials",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotForm map[string]string
			kc.TokenHandler = func(w http.ResponseWriter, r *http.Request) {
				_ = r.ParseForm()
				gotForm = map[string]string{
					"grant_type": r.PostFormValue("grant_type"),
					"client_id":  r.PostFormValue("client_id"),
					"username":   r.PostFormValue("username"),
				}
				if tt.tokenStatus != 200 {
					w.WriteHeader(tt.tokenStatus)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(tokenResponse)
			}

			req := httptest.NewRequest(http.MethodPost, "/login", nil)
			if tt.header != "" {
				req.Header.Set(echo.HeaderAuthorization, tt.header)
			}
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("invalid JSON response: %s", rec.Body.String())
			}
			if tt.wantDetail != "" {
				detail, _ := body["detail"].(string)
				if !strings.Contains(detail, tt.wantDetail) {
					t.Errorf("detail = %q, want containing %q", detail, tt.wantDetail)
				}
			}
			if tt.wantBody != nil {
				want, _ := json.Marshal(tt.wantBody)
				got, _ := json.Marshal(body)
				if string(want) != string(got) {
					t.Errorf("body = %s, want %s", got, want)
				}
				if gotForm["grant_type"] != "password" || gotForm["client_id"] != "formation" || gotForm["username"] != "alice" {
					t.Errorf("unexpected form sent to Keycloak: %v", gotForm)
				}
			}
		})
	}
}

func TestUserInfo(t *testing.T) {
	kc := authtest.New(t, "de")
	verifier := auth.NewVerifier(kc.ServerURL(), "de", true)

	e := echo.New()
	e.HTTPErrorHandler = apierror.HTTPErrorHandler
	e.GET("/user", handlers.UserInfo, auth.RequireUser(verifier))

	tests := []struct {
		name   string
		claims map[string]any
		want   map[string]any
	}{
		{
			name: "full profile",
			claims: map[string]any{
				"preferred_username": "alice",
				"email":              "alice@example.org",
				"name":               "Alice Doe",
				"sub":                "u-1",
			},
			want: map[string]any{
				"username":           "alice",
				"email":              "alice@example.org",
				"name":               "Alice Doe",
				"preferred_username": "alice",
			},
		},
		{
			name:   "missing claims become nulls and sub is username",
			claims: map[string]any{"sub": "u-2"},
			want: map[string]any{
				"username":           "u-2",
				"email":              nil,
				"name":               nil,
				"preferred_username": nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/user", nil)
			req.Header.Set(echo.HeaderAuthorization, "Bearer "+kc.Token(t, tt.claims))
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			if rec.Code != 200 {
				t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
			}
			var body map[string]any
			_ = json.Unmarshal(rec.Body.Bytes(), &body)
			want, _ := json.Marshal(tt.want)
			got, _ := json.Marshal(body)
			if string(want) != string(got) {
				t.Errorf("body = %s, want %s", got, want)
			}
		})
	}
}
