package httpserver

import (
	"net"
	"net/http"
	"sync"
	"time"
)

type tokenBucket struct {
	tokens   float64
	lastSeen time.Time
}

// RateLimit returns a per-IP token-bucket rate limiter middleware.
// If rps <= 0 or burst <= 0, the middleware is a no-op.
// /healthz and /metrics are always exempt so probes and scrapers are never throttled.
func RateLimit(rps float64, burst int) func(http.Handler) http.Handler {
	if rps <= 0 || burst <= 0 {
		return func(next http.Handler) http.Handler { return next }
	}

	var (
		mu      sync.Mutex
		buckets = make(map[string]*tokenBucket)
		lastGC  = time.Now()
	)
	maxTokens := float64(burst)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" || r.URL.Path == "/metrics" {
				next.ServeHTTP(w, r)
				return
			}

			ip := extractClientIP(r)
			now := time.Now()

			mu.Lock()
			if now.Sub(lastGC) > 5*time.Minute {
				for k, b := range buckets {
					if now.Sub(b.lastSeen) > 5*time.Minute {
						delete(buckets, k)
					}
				}
				lastGC = now
			}

			b, ok := buckets[ip]
			if !ok {
				b = &tokenBucket{tokens: maxTokens, lastSeen: now}
				buckets[ip] = b
			} else {
				elapsed := now.Sub(b.lastSeen).Seconds()
				b.tokens += elapsed * rps
				if b.tokens > maxTokens {
					b.tokens = maxTokens
				}
				b.lastSeen = now
			}

			allowed := b.tokens >= 1.0
			if allowed {
				b.tokens -= 1.0
			}
			mu.Unlock()

			if !allowed {
				w.Header().Set("Retry-After", "1")
				WriteError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many requests")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func extractClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "unknown"
}
