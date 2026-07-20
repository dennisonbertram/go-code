// Package tokencache provides provider-neutral caching for refreshable bearer
// credentials. It intentionally owns no network or persistence behavior.
package tokencache

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go-agent-harness/internal/provider"
)

// RefreshFunc exchanges a refresh credential for a replacement credential pair
// and its access-credential expiry. Implementations must not include credential
// values in returned errors.
type RefreshFunc func(ctx context.Context, refreshToken string) (token, nextRefreshToken string, expiresAt time.Time, err error)

// Cache is a mutex-single-flighted, expiry-margin-aware TokenSource.
type Cache struct {
	mu           sync.Mutex
	token        string
	refreshToken string
	expiresAt    time.Time
	safetyMargin time.Duration
	refresh      RefreshFunc
}

var _ provider.TokenSource = (*Cache)(nil)

// New constructs a cache from an initial credential pair. A credential is
// refreshed once its expiry is within safetyMargin. On a refresh failure, a
// still-valid cached credential is returned; an expired or absent credential
// returns the refresh error.
func New(token, refreshToken string, expiresAt time.Time, safetyMargin time.Duration, refresh RefreshFunc) *Cache {
	return &Cache{
		token:        token,
		refreshToken: refreshToken,
		expiresAt:    expiresAt,
		safetyMargin: safetyMargin,
		refresh:      refresh,
	}
}

// Token returns the cached credential until it is within its safety margin,
// then refreshes it while holding the mutex so concurrent callers share one
// refresh result.
func (c *Cache) Token(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	if c.token != "" && now.Add(c.safetyMargin).Before(c.expiresAt) {
		return c.token, nil
	}
	if c.refresh == nil {
		return "", fmt.Errorf("refresh credential is unavailable")
	}

	token, refreshToken, expiresAt, err := c.refresh(ctx, c.refreshToken)
	if err != nil {
		if c.token != "" && time.Now().Before(c.expiresAt) {
			return c.token, nil
		}
		return "", fmt.Errorf("refresh bearer credential: %w", err)
	}
	c.token = token
	c.refreshToken = refreshToken
	c.expiresAt = expiresAt
	return c.token, nil
}
