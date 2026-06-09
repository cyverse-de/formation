package authz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/cyverse-de/formation/internal/config"
)

// ErrInvalidCredentials indicates Keycloak rejected the supplied username and
// password during the password-grant login.
var ErrInvalidCredentials = errors.New("invalid credentials")

// Keycloak performs the legacy resource-owner password-credentials grant used
// by the /login endpoint to exchange a username and password for a token.
type Keycloak struct {
	tokenURL     string
	clientID     string
	clientSecret string
	httpClient   *http.Client
}

// NewKeycloak builds a Keycloak client for the configured realm.
func NewKeycloak(cfg *config.Config, httpClient *http.Client) *Keycloak {
	tokenURL := cfg.KeycloakIssuer() + "/protocol/openid-connect/token"
	return &Keycloak{
		tokenURL:     tokenURL,
		clientID:     cfg.KeycloakClientID,
		clientSecret: cfg.KeycloakClientSecret,
		httpClient:   httpClient,
	}
}

// GetAccessToken exchanges credentials for a token, returning Keycloak's raw
// token JSON response on success. It returns ErrInvalidCredentials for a 401.
func (k *Keycloak) GetAccessToken(ctx context.Context, username, password string) (json.RawMessage, error) {
	form := url.Values{
		"grant_type":    {"password"},
		"client_id":     {k.clientID},
		"client_secret": {k.clientSecret},
		"username":      {username},
		"password":      {password},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, k.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	switch resp.StatusCode {
	case http.StatusOK:
		return json.RawMessage(body), nil
	case http.StatusUnauthorized:
		return nil, ErrInvalidCredentials
	default:
		return nil, fmt.Errorf("keycloak token endpoint returned status %d", resp.StatusCode)
	}
}
