package clients

import (
	"context"
	"net/http"
	"net/url"
)

// AppExposer is the client for the app-exposer service's VICE admin endpoints.
type AppExposer struct {
	base   *url.URL
	client *http.Client
}

// NewAppExposer builds an app-exposer client for the configured base URL.
func NewAppExposer(baseURL string) (*AppExposer, error) {
	base, err := parseBase(baseURL)
	if err != nil {
		return nil, err
	}
	return &AppExposer{base: base, client: &http.Client{Timeout: defaultTimeout}}, nil
}

// ExtendTimeLimit extends the time limit for a VICE analysis.
func (a *AppExposer) ExtendTimeLimit(ctx context.Context, analysisID string) (map[string]any, error) {
	data, err := doJSON(ctx, a.client, http.MethodPost,
		endpoint(a.base, nil, "vice", "admin", "analyses", analysisID, "time-limit"), nil)
	if err != nil {
		return nil, err
	}
	return decodeMap(data, "time-limit")
}

// SaveAndExit saves outputs and terminates a VICE analysis. App-exposer
// returns no body, so the status payload is synthesized like the Python client.
func (a *AppExposer) SaveAndExit(ctx context.Context, analysisID string) (map[string]any, error) {
	_, err := doJSON(ctx, a.client, http.MethodPost,
		endpoint(a.base, nil, "vice", "admin", "analyses", analysisID, "save-and-exit"), nil)
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": "terminated", "outputs_saved": true}, nil
}

// ExitWithoutSave terminates a VICE analysis without saving outputs.
func (a *AppExposer) ExitWithoutSave(ctx context.Context, analysisID string) (map[string]any, error) {
	_, err := doJSON(ctx, a.client, http.MethodPost,
		endpoint(a.base, nil, "vice", "admin", "analyses", analysisID, "exit"), nil)
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": "terminated", "outputs_saved": false}, nil
}

// GetExternalID returns the external (invocation) ID for an analysis.
func (a *AppExposer) GetExternalID(ctx context.Context, analysisID string) (map[string]any, error) {
	return getMap(ctx, a.client, endpoint(a.base, nil, "vice", "admin", "analyses", analysisID, "external-id"))
}

// GetAsyncData returns data generated after the analysis starts (including the
// subdomain); app-exposer responds 404 until the deployment is ready.
func (a *AppExposer) GetAsyncData(ctx context.Context, externalID string) (map[string]any, error) {
	query := url.Values{"external-id": {externalID}}
	return getMap(ctx, a.client, endpoint(a.base, query, "vice", "async-data"))
}
