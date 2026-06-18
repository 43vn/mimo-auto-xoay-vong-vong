package proxy

import (
	"sync"
	"time"
)

// RateLimiter uses a sliding window to limit request rates.
type RateLimiter struct {
	mu          sync.Mutex
	requests    []time.Time
	maxRequests int
	window      time.Duration
	minInterval time.Duration
}

// NewRateLimiter creates a RateLimiter with the given constraints.
// maxRequests is the maximum number of requests allowed in the window.
// window is the sliding window duration.
// minInterval is the minimum time between consecutive requests.
func NewRateLimiter(maxRequests int, window, minInterval time.Duration) *RateLimiter {
	return &RateLimiter{
		maxRequests: maxRequests,
		window:      window,
		minInterval: minInterval,
	}
}

// Allow returns true if the request is allowed by the rate limiter.
func (l *RateLimiter) Allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()

	// Remove expired entries
	cutoff := now.Add(-l.window)
	i := 0
	for i < len(l.requests) && l.requests[i].Before(cutoff) {
		i++
	}
	l.requests = l.requests[i:]

	// Check window limit
	if len(l.requests) >= l.maxRequests {
		return false
	}

	// Check minimum interval
	if len(l.requests) > 0 {
		last := l.requests[len(l.requests)-1]
		if now.Sub(last) < l.minInterval {
			return false
		}
	}

	l.requests = append(l.requests, now)
	return true
}
