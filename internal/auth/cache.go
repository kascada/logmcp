package auth

import (
	"sync"
	"time"
)

type cacheEntry struct {
	name    string
	scopes  []string
	expires time.Time
}

// VerifyCache wraps a VerifyFunc and caches successful results for ttl.
// Failed and error results are never cached.
type VerifyCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]cacheEntry
	fn      VerifyFunc
}

// NewVerifyCache wraps fn, caching successful results for ttl.
func NewVerifyCache(fn VerifyFunc, ttl time.Duration) *VerifyCache {
	return &VerifyCache{
		ttl:     ttl,
		entries: make(map[string]cacheEntry),
		fn:      fn,
	}
}

// Verify checks the cache before calling the underlying VerifyFunc.
func (c *VerifyCache) Verify(token string) (string, []string, bool, error) {
	c.mu.Lock()
	if e, ok := c.entries[token]; ok && time.Now().Before(e.expires) {
		name, scopes := e.name, e.scopes
		c.mu.Unlock()
		return name, scopes, true, nil
	}
	c.mu.Unlock()

	name, scopes, ok, err := c.fn(token)
	if err != nil || !ok {
		return name, scopes, ok, err
	}

	c.mu.Lock()
	c.entries[token] = cacheEntry{name: name, scopes: scopes, expires: time.Now().Add(c.ttl)}
	c.mu.Unlock()

	return name, scopes, true, nil
}
