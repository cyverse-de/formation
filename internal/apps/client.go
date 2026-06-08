package apps

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/cyverse-de/formation/internal/apperr"
)

// AppsClient calls the DE apps service for app discovery and analysis submission.
type AppsClient struct {
	httpClient *http.Client
	baseURL    *url.URL
	logger     *slog.Logger
}

// NewAppsClient constructs an AppsClient. The base URL is parsed once by the
// caller; the http client must carry an appropriate timeout.
func NewAppsClient(httpClient *http.Client, baseURL *url.URL, logger *slog.Logger) *AppsClient {
	return &AppsClient{httpClient: httpClient, baseURL: baseURL, logger: logger}
}

// GetApp fetches a single app definition, including its parameter groups.
func (c *AppsClient) GetApp(ctx context.Context, systemID, appID, username string) (*App, error) {
	u := c.baseURL.JoinPath("apps", systemID, appID)
	u.RawQuery = url.Values{"user": {username}}.Encode()
	var app App
	if err := doJSON(ctx, c.httpClient, http.MethodGet, u, nil, "apps", &app); err != nil {
		return nil, err
	}
	return &app, nil
}

// ListApps returns apps accessible to the user, optionally filtered by a search
// term. The apps service applies search and pagination server-side.
func (c *AppsClient) ListApps(ctx context.Context, username string, limit, offset int, search string) (*AppList, error) {
	q := url.Values{
		"user":   {username},
		"limit":  {fmt.Sprintf("%d", limit)},
		"offset": {fmt.Sprintf("%d", offset)},
	}
	if search != "" {
		q.Set("search", search)
	}
	u := c.baseURL.JoinPath("apps")
	u.RawQuery = q.Encode()
	var list AppList
	if err := doJSON(ctx, c.httpClient, http.MethodGet, u, nil, "apps", &list); err != nil {
		return nil, err
	}
	return &list, nil
}

// SubmitAnalysis submits an analysis. The submission is an opaque map because
// the apps-service schema is large and validated downstream.
func (c *AppsClient) SubmitAnalysis(ctx context.Context, submission map[string]any, username, email string) (*SubmitResult, error) {
	u := c.baseURL.JoinPath("analyses")
	u.RawQuery = url.Values{"user": {username}, "email": {email}}.Encode()
	body, err := json.Marshal(submission)
	if err != nil {
		return nil, fmt.Errorf("encoding submission: %w", err)
	}
	var result SubmitResult
	if err := doJSON(ctx, c.httpClient, http.MethodPost, u, body, "apps", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetAnalysis fetches a single analysis by ID. The apps service has no
// GET /analyses/{id}; it is queried via the JSON filter array. A missing
// analysis is reported as an apperr.NotFoundError.
func (c *AppsClient) GetAnalysis(ctx context.Context, analysisID, username string) (*Analysis, error) {
	list, err := c.queryAnalyses(ctx, username, analysisFilter{Field: "id", Value: analysisID})
	if err != nil {
		return nil, err
	}
	if len(list.Analyses) == 0 {
		return nil, apperr.NotFound("Analysis", analysisID)
	}
	return &list.Analyses[0], nil
}

// ListAnalyses returns analyses for the user filtered by status.
func (c *AppsClient) ListAnalyses(ctx context.Context, username, status string) ([]Analysis, error) {
	var filters []analysisFilter
	if status != "" {
		filters = append(filters, analysisFilter{Field: "status", Value: status})
	}
	list, err := c.queryAnalyses(ctx, username, filters...)
	if err != nil {
		return nil, err
	}
	return list.Analyses, nil
}

func (c *AppsClient) queryAnalyses(ctx context.Context, username string, filters ...analysisFilter) (*analysisList, error) {
	q := url.Values{"user": {username}}
	if len(filters) > 0 {
		encoded, err := json.Marshal(filters)
		if err != nil {
			return nil, fmt.Errorf("encoding analysis filter: %w", err)
		}
		q.Set("filter", string(encoded))
	}
	u := c.baseURL.JoinPath("analyses")
	u.RawQuery = q.Encode()
	var list analysisList
	if err := doJSON(ctx, c.httpClient, http.MethodGet, u, nil, "apps", &list); err != nil {
		return nil, err
	}
	return &list, nil
}

// doJSON performs an HTTP request and decodes a JSON response into out (when
// non-nil). A non-2xx status yields a *StatusError; the body is read for
// server-side diagnostics only.
func doJSON(ctx context.Context, client *http.Client, method string, u *url.URL, body []byte, service string, out any) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &StatusError{Service: service, Code: resp.StatusCode, Body: string(respBody)}
	}
	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("decoding %s response: %w", service, err)
		}
	}
	return nil
}
