package auth

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"time"
)

// impersonationExpiryMargin is subtracted from a token's lifetime so a cached
// token is never handed out moments before it expires mid-request.
const impersonationExpiryMargin = 30 * time.Second

// ImpersonationToken exchanges a service account's token for an access token
// issued to username via the RFC 8693 token-exchange grant, mirroring
// terrain's get-impersonation-token. It returns the token and its lifetime.
func (k *Keycloak) ImpersonationToken(ctx context.Context, subjectToken, username string) (string, time.Duration, error) {
	response, err := k.tokenGrant(ctx, url.Values{
		"grant_type":           {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"subject_token":        {subjectToken},
		"requested_token_type": {"urn:ietf:params:oauth:token-type:access_token"},
		"requested_subject":    {username},
	})
	if err != nil {
		return "", 0, err
	}

	token, _ := response["access_token"].(string)
	if token == "" {
		return "", 0, fmt.Errorf("Keycloak token-exchange response for %s has no access_token", username)
	}
	expiresIn, _ := response["expires_in"].(float64)
	return token, time.Duration(expiresIn) * time.Second, nil
}

// Impersonator mints and caches per-user impersonation tokens so service
// account requests can act as their mapped user when calling terrain.
type Impersonator struct {
	kc *Keycloak

	mu    sync.Mutex
	cache map[string]impersonatedToken
	// locks serializes exchanges per username so a burst of cache misses
	// (e.g. workshop-scale concurrent launches at a token-expiry boundary)
	// results in one Keycloak round trip instead of one per request.
	locks map[string]*sync.Mutex
}

type impersonatedToken struct {
	token   string
	expires time.Time
}

// NewImpersonator builds an Impersonator backed by the Keycloak client.
func NewImpersonator(kc *Keycloak) *Impersonator {
	return &Impersonator{
		kc:    kc,
		cache: make(map[string]impersonatedToken),
		locks: make(map[string]*sync.Mutex),
	}
}

// userLock returns the per-username exchange lock; the lock map is bounded by
// the configured service-account username mappings.
func (i *Impersonator) userLock(username string) *sync.Mutex {
	i.mu.Lock()
	defer i.mu.Unlock()
	lock, ok := i.locks[username]
	if !ok {
		lock = &sync.Mutex{}
		i.locks[username] = lock
	}
	return lock
}

// cachedToken returns a still-valid cached token, pruning an expired entry.
func (i *Impersonator) cachedToken(username string) (string, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	cached, ok := i.cache[username]
	if !ok {
		return "", false
	}
	if time.Now().Before(cached.expires) {
		return cached.token, true
	}
	delete(i.cache, username)
	return "", false
}

// TokenFor returns a cached or freshly exchanged access token for username,
// using subjectToken (the service account's own token) as the exchange subject.
func (i *Impersonator) TokenFor(ctx context.Context, subjectToken, username string) (string, error) {
	lock := i.userLock(username)
	lock.Lock()
	defer lock.Unlock()

	if token, ok := i.cachedToken(username); ok {
		return token, nil
	}

	token, lifetime, err := i.kc.ImpersonationToken(ctx, subjectToken, username)
	if err != nil {
		return "", fmt.Errorf("exchanging service-account token for user %q: %w "+
			"(this usually means token exchange is not enabled for formation's Keycloak client "+
			"or the mapped user does not exist)", username, err)
	}

	if lifetime > impersonationExpiryMargin {
		i.mu.Lock()
		i.cache[username] = impersonatedToken{token: token, expires: time.Now().Add(lifetime - impersonationExpiryMargin)}
		i.mu.Unlock()
	}
	return token, nil
}
