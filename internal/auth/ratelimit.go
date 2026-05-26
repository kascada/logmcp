package auth

import (
	"sync"
	"time"
)

// RateLimiter tracks failed auth attempts per IP using a sliding window.
// A nil RateLimiter is valid and disables rate limiting.
type RateLimiter struct {
	mu          sync.Mutex
	failures    map[string][]time.Time
	maxFailures int
	window      time.Duration
}

// NewRateLimiter creates a RateLimiter that blocks an IP after maxFailures
// failed attempts within the given window.
func NewRateLimiter(maxFailures int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		failures:    make(map[string][]time.Time),
		maxFailures: maxFailures,
		window:      window,
	}
}

// IsBlocked reports whether the IP has exceeded the failure threshold.
func (rl *RateLimiter) IsBlocked(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.prune(ip)
	return len(rl.failures[ip]) >= rl.maxFailures
}

// Record adds a failure timestamp for the IP.
func (rl *RateLimiter) Record(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.failures[ip] = append(rl.failures[ip], time.Now())
	rl.prune(ip)
}

// PruneAll removes expired entries for all tracked IPs.
func (rl *RateLimiter) PruneAll() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	for ip := range rl.failures {
		rl.prune(ip)
	}
}

// prune removes timestamps outside the window for ip. Must be called with mu held.
func (rl *RateLimiter) prune(ip string) {
	cutoff := time.Now().Add(-rl.window)
	times := rl.failures[ip]
	start := 0
	for start < len(times) && times[start].Before(cutoff) {
		start++
	}
	if start > 0 {
		rl.failures[ip] = times[start:]
	}
	if len(rl.failures[ip]) == 0 {
		delete(rl.failures, ip)
	}
}
