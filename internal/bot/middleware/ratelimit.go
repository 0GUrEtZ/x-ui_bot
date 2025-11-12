package middleware

import (
	"sync"
	"time"

	"x-ui-bot/internal/errors"
)

const (
	// RateLimit configuration
	MaxRequestsPerMinute = 10
	RateLimitWindow      = time.Minute
)

// RateLimitEntry represents rate limit tracking for a user
type RateLimitEntry struct {
	count     int
	resetTime time.Time
}

// RateLimiter handles rate limiting
type RateLimiter struct {
	limits map[int64]*RateLimitEntry
	mu     sync.Mutex
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{
		limits: make(map[int64]*RateLimitEntry),
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
			resetTime: now.Add(RateLimitWindow),
		}
		return nil
	}

	if entry.count >= MaxRequestsPerMinute {
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
