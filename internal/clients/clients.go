// Package clients holds the HTTP clients for the DE backend services
// formation fronts (apps and app-exposer). Responses are decoded into generic
// maps and passed through so the original payload shapes survive the rewrite.
package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/cyverse-de/formation/internal/apierror"
)

const defaultTimeout = 30 * time.Second

// endpoint joins the base URL with path segments and query parameters.
func endpoint(base *url.URL, query url.Values, segments ...string) string {
	u := base.JoinPath(segments...)
	u.RawQuery = query.Encode()
	return u.String()
}

// doJSON performs a request and returns the raw response body; non-2xx
// responses become *apierror.UpstreamError like httpx raise_for_status.
func doJSON(ctx context.Context, client *http.Client, method, rawurl string, body any) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encoding request body: %w", err)
		}
		reader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, rawurl, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &apierror.UpstreamError{Status: resp.StatusCode, Body: string(data)}
	}
	return data, nil
}

// getMap performs a GET and decodes the JSON response into a map.
func getMap(ctx context.Context, client *http.Client, rawurl string) (map[string]any, error) {
	data, err := doJSON(ctx, client, http.MethodGet, rawurl, nil)
	if err != nil {
		return nil, err
	}
	return decodeMap(data, rawurl)
}

func decodeMap(data []byte, rawurl string) (map[string]any, error) {
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("decoding response from %s: %w", rawurl, err)
	}
	return result, nil
}

func parseBase(rawurl string) (*url.URL, error) {
	base, err := url.Parse(rawurl)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL %q: %w", rawurl, err)
	}
	return base, nil
}
