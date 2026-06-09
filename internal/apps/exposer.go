package apps

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
)

// AppExposerClient calls the DE app-exposer service for VICE analysis lifecycle
// operations and subdomain discovery.
type AppExposerClient struct {
	httpClient *http.Client
	baseURL    *url.URL
	logger     *slog.Logger
}

// NewAppExposerClient constructs an AppExposerClient.
func NewAppExposerClient(httpClient *http.Client, baseURL *url.URL, logger *slog.Logger) *AppExposerClient {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &AppExposerClient{httpClient: httpClient, baseURL: baseURL, logger: logger}
}

// GetExternalID returns the external (invocation) ID for an analysis.
func (c *AppExposerClient) GetExternalID(ctx context.Context, analysisID string) (string, error) {
	u := c.baseURL.JoinPath("vice", "admin", "analyses", analysisID, "external-id")
	var resp struct {
		ExternalID string `json:"external_id"`
	}
	if err := doJSON(ctx, c.httpClient, c.logger, http.MethodGet, u, nil, "app-exposer", &resp); err != nil {
		return "", err
	}
	return resp.ExternalID, nil
}

// GetAsyncData returns the asynchronously generated data (including subdomain)
// for an analysis. A 404 (deployment not ready) surfaces as a *StatusError so
// callers can retry.
func (c *AppExposerClient) GetAsyncData(ctx context.Context, externalID string) (*AsyncData, error) {
	u := c.baseURL.JoinPath("vice", "async-data")
	u.RawQuery = url.Values{"external-id": {externalID}}.Encode()
	var data AsyncData
	if err := doJSON(ctx, c.httpClient, c.logger, http.MethodGet, u, nil, "app-exposer", &data); err != nil {
		return nil, err
	}
	return &data, nil
}

// SaveAndExit saves outputs and terminates the analysis.
func (c *AppExposerClient) SaveAndExit(ctx context.Context, analysisID string) error {
	u := c.baseURL.JoinPath("vice", "admin", "analyses", analysisID, "save-and-exit")
	return doJSON(ctx, c.httpClient, c.logger, http.MethodPost, u, nil, "app-exposer", nil)
}

// ExitWithoutSave terminates the analysis without saving outputs.
func (c *AppExposerClient) ExitWithoutSave(ctx context.Context, analysisID string) error {
	u := c.baseURL.JoinPath("vice", "admin", "analyses", analysisID, "exit")
	return doJSON(ctx, c.httpClient, c.logger, http.MethodPost, u, nil, "app-exposer", nil)
}

// ExtendTimeLimit extends the analysis time limit, returning the new planned
// end time so callers can schedule around it.
func (c *AppExposerClient) ExtendTimeLimit(ctx context.Context, analysisID string) (*TimeLimit, error) {
	u := c.baseURL.JoinPath("vice", "admin", "analyses", analysisID, "time-limit")
	var tl TimeLimit
	if err := doJSON(ctx, c.httpClient, c.logger, http.MethodPost, u, nil, "app-exposer", &tl); err != nil {
		return nil, err
	}
	return &tl, nil
}
