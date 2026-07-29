package admin

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// RateLimiter is a small fixed-window limiter for authentication endpoints.
// It is intentionally local to a process; deployments with multiple replicas
// should enforce the same policy at their edge or with shared state.
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]rateLimitBucket
	max     int
	window  time.Duration
}

const maxRateLimitBuckets = 10_000

type rateLimitBucket struct {
	started time.Time
	count   int
}

// NewRateLimiter creates a fixed-window limiter that permits max failures per
// window for each key.
func NewRateLimiter(max int, window time.Duration) *RateLimiter {
	if max < 1 {
		max = 1
	}
	if window <= 0 {
		window = time.Minute
	}
	return &RateLimiter{
		buckets: make(map[string]rateLimitBucket),
		max:     max,
		window:  window,
	}
}

// Allow records one failed attempt and reports whether it is permitted.
func (l *RateLimiter) Allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	bucket, ok := l.buckets[key]
	if !ok || now.Sub(bucket.started) >= l.window {
		if !ok && len(l.buckets) >= maxRateLimitBuckets {
			l.evictOldest(now)
		}
		l.buckets[key] = rateLimitBucket{started: now, count: 1}
		return true
	}
	if bucket.count >= l.max {
		return false
	}
	bucket.count++
	l.buckets[key] = bucket
	return true
}

func (l *RateLimiter) evictOldest(now time.Time) {
	var oldestKey string
	var oldest time.Time
	for existingKey, bucket := range l.buckets {
		if now.Sub(bucket.started) >= l.window {
			delete(l.buckets, existingKey)
			continue
		}
		if oldestKey == "" || bucket.started.Before(oldest) {
			oldestKey = existingKey
			oldest = bucket.started
		}
	}
	if len(l.buckets) >= maxRateLimitBuckets && oldestKey != "" {
		delete(l.buckets, oldestKey)
	}
}

// Reset removes a key's failure history after a successful operation.
func (l *RateLimiter) Reset(key string) {
	l.mu.Lock()
	delete(l.buckets, key)
	l.mu.Unlock()
}

// RequestKey binds an authentication attempt to the client address and the
// normalized identifier, limiting both distributed and single-source abuse.
func RequestKey(r *http.Request, namespace, identifier string) string {
	remote := r.RemoteAddr
	if host, _, err := net.SplitHostPort(remote); err == nil {
		remote = host
	}
	return namespace + ":" + remote + ":" + strings.ToLower(strings.TrimSpace(identifier))
}
