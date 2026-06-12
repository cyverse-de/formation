package clients

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/cyverse-de/formation/internal/apierror"
)

// Terrain is the client for the DE's terrain API gateway. Every method
// forwards the caller's bearer token; terrain derives the user identity (and
// email for notifications) from the token's claims.
type Terrain struct {
	base   *url.URL
	client *http.Client
	stream *http.Client
}

// NewTerrain builds a terrain client for the configured base URL.
func NewTerrain(baseURL string) (*Terrain, error) {
	base, err := parseBase(baseURL)
	if err != nil {
		return nil, err
	}
	return &Terrain{
		base:   base,
		client: &http.Client{Timeout: defaultTimeout},
		// stream has no overall deadline: it carries whole-file uploads and
		// downloads, which can legitimately outlive any fixed timeout. The
		// response-header timeout still bounds an unresponsive terrain.
		stream: &http.Client{Transport: &http.Transport{ResponseHeaderTimeout: defaultTimeout}},
	}, nil
}

// Bootstrap returns the caller's session/orientation info (identity, home and
// trash paths, default output folder) from terrain's aggregator endpoint.
func (t *Terrain) Bootstrap(ctx context.Context, token string) (map[string]any, error) {
	return getMap(ctx, t.client, endpoint(t.base, nil, "secured", "bootstrap"), token)
}

// GetApp fetches a single app's details, including its parameter groups.
func (t *Terrain) GetApp(ctx context.Context, token, systemID, appID string) (map[string]any, error) {
	return getMap(ctx, t.client, endpoint(t.base, nil, "apps", systemID, appID), token)
}

// ListApps lists apps accessible to the caller, optionally filtered by a search term.
func (t *Terrain) ListApps(ctx context.Context, token string, limit, offset int, search string) (map[string]any, error) {
	query := url.Values{
		"limit":  {strconv.Itoa(limit)},
		"offset": {strconv.Itoa(offset)},
	}
	if search != "" {
		query.Set("search", search)
	}
	return getMap(ctx, t.client, endpoint(t.base, query, "apps"), token)
}

// SubmitAnalysis submits an analysis job; terrain fills in the user and email
// from the token.
func (t *Terrain) SubmitAnalysis(ctx context.Context, token string, submission map[string]any) (map[string]any, error) {
	u := endpoint(t.base, nil, "analyses")
	data, err := doJSON(ctx, t.client, http.MethodPost, u, token, submission)
	if err != nil {
		return nil, err
	}
	return decodeMap(data, u)
}

// listingFilter builds terrain's JSON-encoded analysis listing filter.
func listingFilter(field, value string) (url.Values, error) {
	filter, err := json.Marshal([]map[string]string{{"field": field, "value": value}})
	if err != nil {
		return nil, err
	}
	return url.Values{"filter": {string(filter)}}, nil
}

// GetAnalysis looks up one analysis. Terrain has no GET /analyses/{id}, so
// this filters the listing endpoint by id; an empty result becomes a 404
// UpstreamError, matching the Python client's synthesized error.
func (t *Terrain) GetAnalysis(ctx context.Context, token, analysisID string) (map[string]any, error) {
	query, err := listingFilter("id", analysisID)
	if err != nil {
		return nil, err
	}

	result, err := getMap(ctx, t.client, endpoint(t.base, query, "analyses"), token)
	if err != nil {
		return nil, err
	}

	analyses, _ := result["analyses"].([]any)
	if len(analyses) == 0 {
		return nil, &apierror.UpstreamError{Status: http.StatusNotFound}
	}
	analysis, ok := analyses[0].(map[string]any)
	if !ok {
		return nil, &apierror.UpstreamError{Status: http.StatusNotFound}
	}
	return analysis, nil
}

// ListAnalyses lists the caller's analyses, optionally filtered by status.
func (t *Terrain) ListAnalyses(ctx context.Context, token, status string) (map[string]any, error) {
	query := url.Values{}
	if status != "" {
		var err error
		if query, err = listingFilter("status", status); err != nil {
			return nil, err
		}
	}
	return getMap(ctx, t.client, endpoint(t.base, query, "analyses"), token)
}

// ExtendTimeLimit extends the time limit for a VICE analysis by three days.
func (t *Terrain) ExtendTimeLimit(ctx context.Context, token, analysisID string) (map[string]any, error) {
	u := endpoint(t.base, nil, "analyses", analysisID, "time-limit")
	data, err := doJSON(ctx, t.client, http.MethodPost, u, token, nil)
	if err != nil {
		return nil, err
	}
	return decodeMap(data, u)
}

// SaveAndExit stops an analysis through terrain's user-level stop endpoint,
// which uploads outputs before terminating VICE analyses. Terrain returns only
// the analysis id, so the status payload is synthesized like the Python client.
func (t *Terrain) SaveAndExit(ctx context.Context, token, analysisID string) (map[string]any, error) {
	_, err := doJSON(ctx, t.client, http.MethodPost,
		endpoint(t.base, nil, "analyses", analysisID, "stop"), token, nil)
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": "terminated", "outputs_saved": true}, nil
}

// ExitWithoutSave terminates a VICE analysis without saving outputs.
func (t *Terrain) ExitWithoutSave(ctx context.Context, token, analysisID string) (map[string]any, error) {
	_, err := doJSON(ctx, t.client, http.MethodPost,
		endpoint(t.base, nil, "vice", "analyses", analysisID, "exit"), token, nil)
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": "terminated", "outputs_saved": false}, nil
}

// GetExternalID returns the external (invocation) ID for a VICE analysis.
// Terrain's response key is externalID, unlike app-exposer's external_id.
func (t *Terrain) GetExternalID(ctx context.Context, token, analysisID string) (map[string]any, error) {
	return getMap(ctx, t.client, endpoint(t.base, nil, "vice", "analyses", analysisID, "external-id"), token)
}

// GetAsyncData returns data generated after the analysis starts (including the
// subdomain); the backend responds 404 until the deployment is ready.
func (t *Terrain) GetAsyncData(ctx context.Context, token, externalID string) (map[string]any, error) {
	query := url.Values{"external-id": {externalID}}
	return getMap(ctx, t.client, endpoint(t.base, query, "vice", "async-data"), token)
}
