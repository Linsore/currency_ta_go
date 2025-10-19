// internal/ratelimit/limiter.go
//
// Package ratelimit provides a simple, in-process IP-based rate limiter.
// Implementation: fixed-size sliding window per remote IP. Within each
// window, only `limit` requests are allowed. Exceeding requests receive
// HTTP 429 with a basic Retry-After header.
//
// Notes:
//   - This is a best-effort limiter for a single process. For multi-instance
//     deployments, consider a shared store (e.g., Redis) and a token bucket.
//   - The client's IP is determined by X-Forwarded-For (first value) if present,
//     otherwise by the connection's RemoteAddr.

package ratelimit

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// requestBucket tracks request count within a time window for a single IP.
type requestBucket struct {
	count       int
	windowStart time.Time
}

// IPLimiter enforces a limit of `limit` requests per `window` for each IP.
type IPLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	ipMap  map[string]*requestBucket
}

// NewIPLimiter constructs an IP limiter using the provided limit and window size.
func NewIPLimiter(limit int, window time.Duration) *IPLimiter {
	return &IPLimiter{
		limit:  limit,
		window: window,
		ipMap:  make(map[string]*requestBucket),
	}
}

// Middleware wraps an http.Handler and rejects requests with HTTP 429 when the
// per-IP allowance is exceeded for the current window.
func (l *IPLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		client := clientIP(r)
		if !l.allow(client) {
			// A minimal hint. For more accuracy, track remaining window time.
			w.Header().Set("Retry-After", "60")
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// allow returns true if the given IP is allowed to proceed within the current
// window; otherwise false. It also updates the per-IP bucket.
func (l *IPLimiter) allow(ip string) bool {
	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	bucket, exists := l.ipMap[ip]
	if !exists || now.Sub(bucket.windowStart) >= l.window {
		// Start a new window for this IP.
		l.ipMap[ip] = &requestBucket{
			count:       1,
			windowStart: now,
		}
		return true
	}

	// Existing active window: check count.
	if bucket.count >= l.limit {
		return false
	}

	bucket.count++
	return true
}

// clientIP attempts to determine the client IP.
// Preference order:
//   1) X-Forwarded-For (first value, if present)
//   2) RemoteAddr from the connection
// If neither is available, returns "unknown".
func clientIP(r *http.Request) string {
	// Respect the first X-Forwarded-For IP when present.
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Extract substring until the first comma without allocating a slice.
		if i := indexByte(xff, ','); i >= 0 {
			return xff[:i]
		}
		return xff
	}

	// Fallback to the remote address of the connection.
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil && host != "" {
		return host
	}

	return "unknown"
}

// indexByte finds the first index of c in s, or -1 if not found.
// Kept minimal to avoid importing strings just for this operation.
func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
