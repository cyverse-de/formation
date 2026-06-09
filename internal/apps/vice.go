package apps

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// VICEConfig configures subdomain resolution and URL-readiness probing.
type VICEConfig struct {
	ViceDomain          string
	MaxSubdomainRetries int
	SubdomainRetryDelay time.Duration
	URLCheckRetries     int
	URLCheckTimeout     time.Duration
	URLCheckCacheTTL    time.Duration
}

// ProbeDetails describes the outcome of a VICE URL readiness probe.
type ProbeDetails struct {
	StatusCode     int    `json:"status_code,omitempty"`
	ResponseTimeMs int    `json:"response_time_ms,omitempty"`
	Attempt        int    `json:"attempt,omitempty"`
	Error          string `json:"error,omitempty"`
}

type cacheEntry struct {
	at      time.Time
	ready   bool
	details ProbeDetails
}

// VICEResolver resolves analysis subdomains via app-exposer and probes VICE
// URLs for readiness, caching probe results for a short TTL and resolved
// subdomains (immutable once assigned) for the life of the process.
type VICEResolver struct {
	exposer     *AppExposerClient
	cfg         VICEConfig
	logger      *slog.Logger
	probeClient *http.Client

	mu         sync.Mutex
	cache      map[string]cacheEntry
	subdomains map[string]string // analysisID -> subdomain

	// now and sleep are injectable for testing. sleep reports false when the
	// context was cancelled before the delay elapsed.
	now   func() time.Time
	sleep func(context.Context, time.Duration) bool
}

// NewVICEResolver constructs a VICEResolver. Defaults match the original
// implementation when fields are left zero.
func NewVICEResolver(exposer *AppExposerClient, cfg VICEConfig, logger *slog.Logger) *VICEResolver {
	if cfg.MaxSubdomainRetries == 0 {
		cfg.MaxSubdomainRetries = 5
	}
	if cfg.SubdomainRetryDelay == 0 {
		cfg.SubdomainRetryDelay = time.Second
	}
	if cfg.URLCheckRetries == 0 {
		cfg.URLCheckRetries = 3
	}
	if cfg.URLCheckTimeout == 0 {
		cfg.URLCheckTimeout = 5 * time.Second
	}
	if cfg.URLCheckCacheTTL == 0 {
		cfg.URLCheckCacheTTL = 5 * time.Second
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &VICEResolver{
		exposer: exposer,
		cfg:     cfg,
		logger:  logger,
		probeClient: &http.Client{
			Timeout: cfg.URLCheckTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		cache:      map[string]cacheEntry{},
		subdomains: map[string]string{},
		now:        time.Now,
		sleep:      sleepCtx,
	}
}

// sleepCtx waits for d, returning false early if ctx is cancelled so retry
// loops stop doing work for abandoned requests.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// URLFor composes the public VICE URL for a subdomain.
func (r *VICEResolver) URLFor(subdomain string) string {
	return "https://" + subdomain + r.cfg.ViceDomain
}

// ResolveSubdomain returns the subdomain for an analysis, retrying on 404 while
// the deployment becomes ready. It is best-effort: an empty string indicates the
// subdomain could not be resolved (errors are logged, not returned). Successful
// resolutions are cached so status polling does not re-query app-exposer.
func (r *VICEResolver) ResolveSubdomain(ctx context.Context, analysisID string) string {
	r.mu.Lock()
	cached, ok := r.subdomains[analysisID]
	r.mu.Unlock()
	if ok {
		return cached
	}

	externalID, err := r.exposer.GetExternalID(ctx, analysisID)
	if err != nil {
		r.logger.Warn("could not get external id for analysis; VICE URL will be omitted", "analysis_id", analysisID, "error", err)
		return ""
	}
	if externalID == "" {
		return ""
	}

	for attempt := 0; attempt < r.cfg.MaxSubdomainRetries; attempt++ {
		data, err := r.exposer.GetAsyncData(ctx, externalID)
		if err == nil {
			if data.Subdomain != "" {
				r.storeSubdomain(analysisID, data.Subdomain)
				return data.Subdomain
			}
			// 200 but no subdomain yet: wait and retry.
			if attempt < r.cfg.MaxSubdomainRetries-1 && !r.sleep(ctx, r.cfg.SubdomainRetryDelay) {
				return ""
			}
			continue
		}

		var se *StatusError
		if errors.As(err, &se) && se.Code == http.StatusNotFound {
			// Deployment not ready yet; this is expected shortly after launch.
			if attempt < r.cfg.MaxSubdomainRetries-1 {
				if !r.sleep(ctx, r.cfg.SubdomainRetryDelay) {
					return ""
				}
				continue
			}
			return ""
		}
		r.logger.Warn("error getting async data for analysis; VICE URL will be omitted", "external_id", externalID, "error", err)
		return ""
	}
	return ""
}

// storeSubdomain caches a resolved subdomain. Subdomains are immutable for the
// life of an analysis; the size guard only bounds memory in a very long-lived
// process, since re-resolving after a reset costs one app-exposer lookup.
func (r *VICEResolver) storeSubdomain(analysisID, subdomain string) {
	r.mu.Lock()
	if len(r.subdomains) >= 4096 {
		r.subdomains = map[string]string{}
	}
	r.subdomains[analysisID] = subdomain
	r.mu.Unlock()
}

// CheckURLReady probes a VICE URL for readiness, caching the result. A status
// in the 2xx-3xx range is considered ready. HEAD is attempted first, falling
// back to GET on 404/405.
func (r *VICEResolver) CheckURLReady(ctx context.Context, rawurl string) (bool, ProbeDetails) {
	now := r.now()
	r.mu.Lock()
	if e, ok := r.cache[rawurl]; ok && now.Sub(e.at) < r.cfg.URLCheckCacheTTL {
		r.mu.Unlock()
		return e.ready, e.details
	}
	r.mu.Unlock()

	for attempt := 0; attempt < r.cfg.URLCheckRetries; attempt++ {
		start := r.now()
		resp, err := r.probe(ctx, rawurl)
		if err != nil {
			// A cancelled request says nothing about the URL; don't cache it.
			if ctx.Err() != nil {
				return false, ProbeDetails{Error: err.Error(), Attempt: attempt + 1}
			}
			if attempt < r.cfg.URLCheckRetries-1 {
				if !r.sleep(ctx, backoff(attempt)) {
					return false, ProbeDetails{Error: err.Error(), Attempt: attempt + 1}
				}
				continue
			}
			details := ProbeDetails{Error: err.Error(), Attempt: attempt + 1}
			r.store(rawurl, now, false, details)
			return false, details
		}
		_ = resp.Body.Close()
		ready := resp.StatusCode >= 200 && resp.StatusCode < 400
		details := ProbeDetails{
			StatusCode:     resp.StatusCode,
			ResponseTimeMs: int(r.now().Sub(start).Milliseconds()),
			Attempt:        attempt + 1,
		}
		r.store(rawurl, now, ready, details)
		return ready, details
	}

	details := ProbeDetails{Error: "max_retries_exceeded", Attempt: r.cfg.URLCheckRetries}
	r.store(rawurl, now, false, details)
	return false, details
}

// probe issues a HEAD request, falling back to GET when the server does not
// support HEAD (404/405).
func (r *VICEResolver) probe(ctx context.Context, rawurl string) (*http.Response, error) {
	headReq, err := http.NewRequestWithContext(ctx, http.MethodHead, rawurl, nil)
	if err != nil {
		return nil, err
	}
	resp, err := r.probeClient.Do(headReq)
	if err == nil && (resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed) {
		_ = resp.Body.Close()
		err = errFallback
	}
	if err != nil {
		getReq, gerr := http.NewRequestWithContext(ctx, http.MethodGet, rawurl, nil)
		if gerr != nil {
			return nil, gerr
		}
		return r.probeClient.Do(getReq)
	}
	return resp, nil
}

var errFallback = fmt.Errorf("falling back to GET")

func (r *VICEResolver) store(url string, at time.Time, ready bool, details ProbeDetails) {
	r.mu.Lock()
	// Sweep expired entries so the cache doesn't grow for the process lifetime.
	for k, e := range r.cache {
		if at.Sub(e.at) >= r.cfg.URLCheckCacheTTL {
			delete(r.cache, k)
		}
	}
	r.cache[url] = cacheEntry{at: at, ready: ready, details: details}
	r.mu.Unlock()
}

// backoff returns 0.5s * 2^attempt.
func backoff(attempt int) time.Duration {
	return 500 * time.Millisecond << uint(attempt)
}
