package middleware

import (
	"sync"
	"time"

	"x-ui-bot/internal/errors"
)

// RateLimitEntry represents rate limit tracking for a user
type RateLimitEntry struct {
	count     int
	resetTime time.Time
}

// RateLimiter handles rate limiting
type RateLimiter struct {
	limits               map[int64]*RateLimitEntry
	mu                   sync.Mutex
	maxRequestsPerMinute int
	window               time.Duration
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(maxRequests int, windowSeconds int) *RateLimiter {
	// Default values if not configured
	if maxRequests <= 0 {
		maxRequests = 10
	}
	if windowSeconds <= 0 {
		windowSeconds = 60
	}

	return &RateLimiter{
		limits:               make(map[int64]*RateLimitEntry),
		maxRequestsPerMinute: maxRequests,
		window:               time.Duration(windowSeconds) * time.Second,
	}
}

// Check checks if a user is within rate limits
func (r *RateLimiter) Check(userID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()

	entry, exists := r.limits[userID]
	if !exists || now.After(entry.resetTime) {
		r.limits[userID] = &RateLimitEntry{
			count:     1,
			resetTime: now.Add(r.window),
		}
		return nil
	}

	if entry.count >= r.maxRequestsPerMinute {
		return errors.RateLimitExceeded("too many requests")
	}

	entry.count++
	return nil
}

// Cleanup removes expired rate limit entries
func (r *RateLimiter) Cleanup() {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	for userID, entry := range r.limits {
		if now.After(entry.resetTime) {
			delete(r.limits, userID)
		}
	}
}
