package clients

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/cyverse-de/formation/internal/apierror"
)

// Apps is the client for the DE apps service.
type Apps struct {
	base   *url.URL
	client *http.Client
}

// NewApps builds an apps-service client for the configured base URL.
func NewApps(baseURL string) (*Apps, error) {
	base, err := parseBase(baseURL)
	if err != nil {
		return nil, err
	}
	return &Apps{base: base, client: &http.Client{Timeout: defaultTimeout}}, nil
}

// GetApp fetches a single app's details, including its parameter groups.
func (a *Apps) GetApp(ctx context.Context, systemID, appID, username string) (map[string]any, error) {
	query := url.Values{"user": {username}}
	return getMap(ctx, a.client, endpoint(a.base, query, "apps", systemID, appID))
}

// SubmitAnalysis submits an analysis job; the user's email travels as a query parameter.
func (a *Apps) SubmitAnalysis(ctx context.Context, submission map[string]any, username, email string) (map[string]any, error) {
	query := url.Values{"user": {username}, "email": {email}}
	data, err := doJSON(ctx, a.client, http.MethodPost, endpoint(a.base, query, "analyses"), submission)
	if err != nil {
		return nil, err
	}
	return decodeMap(data, "analyses")
}

// GetAnalysis looks up one analysis. The apps service has no GET
// /analyses/{id}, so this filters the listing endpoint by id; an empty result
// becomes a 404 UpstreamError, matching the Python client's synthesized error.
func (a *Apps) GetAnalysis(ctx context.Context, analysisID, username string) (map[string]any, error) {
	filter, err := json.Marshal([]map[string]string{{"field": "id", "value": analysisID}})
	if err != nil {
		return nil, err
	}
	query := url.Values{"user": {username}, "filter": {string(filter)}}

	result, err := getMap(ctx, a.client, endpoint(a.base, query, "analyses"))
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

// ListApps lists apps accessible to the user, optionally filtered by a search term.
func (a *Apps) ListApps(ctx context.Context, username string, limit, offset int, search string) (map[string]any, error) {
	query := url.Values{
		"user":   {username},
		"limit":  {strconv.Itoa(limit)},
		"offset": {strconv.Itoa(offset)},
	}
	if search != "" {
		query.Set("search", search)
	}
	return getMap(ctx, a.client, endpoint(a.base, query, "apps"))
}

// ListAnalyses lists the user's analyses, optionally filtered by status.
func (a *Apps) ListAnalyses(ctx context.Context, username, status string) (map[string]any, error) {
	query := url.Values{"user": {username}}
	if status != "" {
		filter, err := json.Marshal([]map[string]string{{"field": "status", "value": status}})
		if err != nil {
			return nil, err
		}
		query.Set("filter", string(filter))
	}
	return getMap(ctx, a.client, endpoint(a.base, query, "analyses"))
}
