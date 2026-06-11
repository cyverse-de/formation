// Package vice resolves VICE analysis subdomains and probes app URLs for
// readiness, porting the helpers at the top of the Python routes/apps.py.
package vice

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"syscall"
	"time"

	"github.com/cyverse-de/formation/internal/apierror"
	"github.com/cyverse-de/formation/internal/clients"
)

// URLChecker probes VICE app URLs with retries, exponential backoff, and a
// TTL cache of both successes and failures.
type URLChecker struct {
	client  *http.Client
	timeout time.Duration
	retries int
	ttl     time.Duration

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	at      time.Time
	ready   bool
	details map[string]any
}

// NewURLChecker builds a checker with the configured probe timeout, retry
// count, and cache TTL. Redirects are not followed: a 3xx counts as ready.
func NewURLChecker(timeout time.Duration, retries int, ttl time.Duration) *URLChecker {
	return &URLChecker{
		client: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		timeout: timeout,
		retries: retries,
		ttl:     ttl,
		// The cache is unbounded like the Python version; entries are tiny and
		// keyed by active analysis URLs, so growth is negligible in practice.
		cache: make(map[string]cacheEntry),
	}
}

// Check reports whether the URL responds with a 2xx/3xx, along with probe
// details (status code, response time, attempt, or error information).
func (uc *URLChecker) Check(ctx context.Context, url string) (bool, map[string]any) {
	now := time.Now()

	uc.mu.Lock()
	if entry, ok := uc.cache[url]; ok && now.Sub(entry.at) < uc.ttl {
		uc.mu.Unlock()
		return entry.ready, entry.details
	}
	uc.mu.Unlock()

	for attempt := range uc.retries {
		start := time.Now()
		status, err := uc.probe(ctx, url)
		if err == nil {
			ready := status >= 200 && status < 400
			details := map[string]any{
				"status_code":      status,
				"response_time_ms": int(time.Since(start).Milliseconds()),
				"attempt":          attempt + 1,
			}
			uc.store(url, now, ready, details)
			return ready, details
		}

		if attempt < uc.retries-1 {
			backoff(ctx, attempt)
			continue
		}

		var details map[string]any
		if isTimeout(err) {
			details = map[string]any{
				"error":           "timeout",
				"timeout_seconds": uc.timeout.Seconds(),
				"attempt":         attempt + 1,
			}
		} else {
			details = map[string]any{
				"error":      err.Error(),
				"error_type": fmt.Sprintf("%T", err),
				"attempt":    attempt + 1,
			}
		}
		uc.store(url, now, false, details)
		return false, details
	}

	details := map[string]any{"error": "max_retries_exceeded", "retries": uc.retries}
	uc.store(url, now, false, details)
	return false, details
}

// probe tries HEAD first and falls back to GET on 404/405 (or on HEAD
// failures other than connection errors, which propagate for retry).
func (uc *URLChecker) probe(ctx context.Context, url string) (int, error) {
	status, err := uc.request(ctx, http.MethodHead, url)
	if err != nil {
		if isConnectError(err) {
			return 0, err
		}
		return uc.request(ctx, http.MethodGet, url)
	}
	if status == http.StatusNotFound || status == http.StatusMethodNotAllowed {
		return uc.request(ctx, http.MethodGet, url)
	}
	return status, nil
}

func (uc *URLChecker) request(ctx context.Context, method, url string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := uc.client.Do(req)
	if err != nil {
		return 0, err
	}
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

func (uc *URLChecker) store(url string, at time.Time, ready bool, details map[string]any) {
	uc.mu.Lock()
	uc.cache[url] = cacheEntry{at: at, ready: ready, details: details}
	uc.mu.Unlock()
}

// backoff sleeps 0.5s * 2^attempt, bailing early if the request is canceled.
func backoff(ctx context.Context, attempt int) {
	delay := 500 * time.Millisecond * (1 << attempt)
	select {
	case <-ctx.Done():
	case <-time.After(delay):
	}
}

func isTimeout(err error) bool {
	var netErr net.Error
	return errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout())
}

func isConnectError(err error) bool {
	var dnsErr *net.DNSError
	return errors.Is(err, syscall.ECONNREFUSED) || errors.As(err, &dnsErr)
}

// SubdomainResolver looks up the subdomain for a VICE analysis via terrain,
// retrying while the deployment's async data is not ready.
type SubdomainResolver struct {
	terrain    *clients.Terrain
	maxRetries int
	retryDelay time.Duration
}

// NewSubdomainResolver uses the Python defaults of 5 retries, 1s apart.
func NewSubdomainResolver(terrain *clients.Terrain) *SubdomainResolver {
	return &SubdomainResolver{terrain: terrain, maxRetries: 5, retryDelay: time.Second}
}

// NewSubdomainResolverWithRetries allows tests to shorten the retry loop.
func NewSubdomainResolverWithRetries(terrain *clients.Terrain, maxRetries int, retryDelay time.Duration) *SubdomainResolver {
	return &SubdomainResolver{terrain: terrain, maxRetries: maxRetries, retryDelay: retryDelay}
}

// Resolve returns the analysis subdomain, or "" if it cannot be determined.
// All failures are swallowed (logged upstream as needed) like the Python
// helper, since a missing subdomain just means no URL in the response.
func (r *SubdomainResolver) Resolve(ctx context.Context, token, analysisID string) string {
	externalIDResponse, err := r.terrain.GetExternalID(ctx, token, analysisID)
	if err != nil {
		return ""
	}
	externalID, _ := externalIDResponse["externalID"].(string)
	if externalID == "" {
		return ""
	}

	for attempt := range r.maxRetries {
		asyncData, err := r.terrain.GetAsyncData(ctx, token, externalID)
		if err != nil {
			// 404 means the deployment is not ready yet; retry after a delay.
			var upstream *apierror.UpstreamError
			if errors.As(err, &upstream) && upstream.Status == http.StatusNotFound {
				if attempt < r.maxRetries-1 {
					select {
					case <-ctx.Done():
						return ""
					case <-time.After(r.retryDelay):
					}
					continue
				}
			}
			return ""
		}
		if subdomain, _ := asyncData["subdomain"].(string); subdomain != "" {
			return subdomain
		}
	}
	return ""
}
