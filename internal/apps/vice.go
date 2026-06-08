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
// URLs for readiness, caching probe results for a short TTL.
type VICEResolver struct {
	exposer     *AppExposerClient
	cfg         VICEConfig
	logger      *slog.Logger
	probeClient *http.Client

	mu    sync.Mutex
	cache map[string]cacheEntry

	// now and sleep are injectable for testing.
	now   func() time.Time
	sleep func(time.Duration)
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
		cache: map[string]cacheEntry{},
		now:   time.Now,
		sleep: time.Sleep,
	}
}

// URLFor composes the public VICE URL for a subdomain.
func (r *VICEResolver) URLFor(subdomain string) string {
	return "https://" + subdomain + r.cfg.ViceDomain
}

// ResolveSubdomain returns the subdomain for an analysis, retrying on 404 while
// the deployment becomes ready. It is best-effort: an empty string indicates the
// subdomain could not be resolved (errors are logged, not returned).
func (r *VICEResolver) ResolveSubdomain(ctx context.Context, analysisID string) string {
	externalID, err := r.exposer.GetExternalID(ctx, analysisID)
	if err != nil {
		r.logf("could not get external id for analysis; VICE URL will be omitted", "analysis_id", analysisID, "error", err)
		return ""
	}
	if externalID == "" {
		return ""
	}

	for attempt := 0; attempt < r.cfg.MaxSubdomainRetries; attempt++ {
		data, err := r.exposer.GetAsyncData(ctx, externalID)
		if err == nil {
			if data.Subdomain != "" {
				return data.Subdomain
			}
			// 200 but no subdomain yet: wait and retry.
			if attempt < r.cfg.MaxSubdomainRetries-1 {
				r.sleep(r.cfg.SubdomainRetryDelay)
			}
			continue
		}

		var se *StatusError
		if errors.As(err, &se) && se.Code == http.StatusNotFound {
			// Deployment not ready yet; this is expected shortly after launch.
			if attempt < r.cfg.MaxSubdomainRetries-1 {
				r.sleep(r.cfg.SubdomainRetryDelay)
				continue
			}
			return ""
		}
		r.logf("error getting async data for analysis; VICE URL will be omitted", "external_id", externalID, "error", err)
		return ""
	}
	return ""
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
			if attempt < r.cfg.URLCheckRetries-1 {
				r.sleep(backoff(attempt))
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
	r.cache[url] = cacheEntry{at: at, ready: ready, details: details}
	r.mu.Unlock()
}

func (r *VICEResolver) logf(msg string, args ...any) {
	if r.logger != nil {
		r.logger.Warn(msg, args...)
	}
}

// backoff returns 0.5s * 2^attempt.
func backoff(attempt int) time.Duration {
	return 500 * time.Millisecond << uint(attempt)
}
