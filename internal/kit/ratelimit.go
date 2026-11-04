package kit

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type visitor struct {
	lim      *rate.Limiter
	lastSeen time.Time
}

// RateLimiter is a per-key token-bucket limiter suitable for gateway and
// public-endpoint abuse protection. It is process-local; a distributed limiter
// (Redis) can replace it without changing call sites.
type RateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	perMin   rate.Limit
	burst    int
	ttl      time.Duration
	lastGC   time.Time
}

// NewRateLimiter allows perMinute requests per key with the given burst.
func NewRateLimiter(perMinute, burst int) *RateLimiter {
	if perMinute < 1 {
		perMinute = 1
	}
	if burst < 1 {
		burst = 1
	}
	return &RateLimiter{
		visitors: make(map[string]*visitor),
		perMin:   rate.Limit(float64(perMinute) / 60.0),
		burst:    burst,
		ttl:      10 * time.Minute,
	}
}

func (rl *RateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	if now.Sub(rl.lastGC) > time.Minute {
		rl.gc(now)
		rl.lastGC = now
	}

	v, ok := rl.visitors[key]
	if !ok {
		v = &visitor{lim: rate.NewLimiter(rl.perMin, rl.burst)}
		rl.visitors[key] = v
	}
	v.lastSeen = now
	return v.lim.Allow()
}

func (rl *RateLimiter) gc(now time.Time) {
	for key, v := range rl.visitors {
		if now.Sub(v.lastSeen) > rl.ttl {
			delete(rl.visitors, key)
		}
	}
}

// ClientKey derives a stable per-client key from the request.
func ClientKey(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if idx := strings.IndexByte(xff, ','); idx > 0 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// RateLimit returns middleware rejecting requests over the configured rate
// with 429 and a Retry-After header.
func RateLimit(rl *RateLimiter) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !rl.allow(ClientKey(r)) {
				w.Header().Set("Retry-After", "60")
				Error(w, http.StatusTooManyRequests, "rate_limited", "Too many requests. Please slow down.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
