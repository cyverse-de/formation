package auth

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HTTPStatusError reports a non-2xx response from Keycloak's token endpoint.
type HTTPStatusError struct {
	StatusCode int
	Body       string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("keycloak returned status %d: %s", e.StatusCode, e.Body)
}

// Keycloak performs the resource-owner password grant used by /login.
type Keycloak struct {
	tokenURL string
	clientID string
	secret   string
	client   *http.Client
}

// NewKeycloak builds a client for the realm's token endpoint. serverURL must
// end with a slash (config.Load guarantees this).
func NewKeycloak(serverURL, realm, clientID, clientSecret string, sslVerify bool) (*Keycloak, error) {
	tokenURL, err := url.JoinPath(serverURL, "realms", realm, "protocol", "openid-connect", "token")
	if err != nil {
		return nil, fmt.Errorf("building Keycloak token URL: %w", err)
	}
	return &Keycloak{
		tokenURL: tokenURL,
		clientID: clientID,
		secret:   clientSecret,
		client:   newHTTPClient(sslVerify),
	}, nil
}

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

// PasswordGrant exchanges user credentials for Keycloak's token response,
// returned verbatim so /login can proxy it to the client.
func (k *Keycloak) PasswordGrant(ctx context.Context, username, password string) (map[string]any, error) {
	form := url.Values{
		"grant_type":    {"password"},
		"client_id":     {k.clientID},
		"client_secret": {k.secret},
		"username":      {username},
		"password":      {password},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, k.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := k.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPStatusError{StatusCode: resp.StatusCode, Body: string(body)}
	}

	var token map[string]any
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, fmt.Errorf("decoding Keycloak token response: %w", err)
	}
	return token, nil
}
