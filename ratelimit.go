package fibery

import (
	"log/slog"
	"math"
	"net/http"
	"net/netip"
	"sync"
	"time"
)

// RateLimit configures RateLimitMiddleware. Rate limiting is scoped per client
// IP (resolved via ClientIP using Trusted). Each IP gets an independent token
// bucket refilling at RPS tokens per second up to Burst tokens.
type RateLimit struct {
	// RPS is the sustained request rate per client IP. Values <= 0 disable the
	// middleware (it becomes a pass-through, logged once at construction).
	RPS float64
	// Burst is the bucket capacity (maximum instantaneous burst). Defaults to
	// ceil(RPS), minimum 1, when <= 0.
	Burst int
	// Trusted lists proxy CIDRs whose forwarding headers may be believed when
	// resolving the client IP. See ClientIP.
	Trusted []netip.Prefix
	// IdleTTL is how long a per-IP bucket is retained after its last request
	// before eviction. Defaults to 10 minutes when <= 0.
	IdleTTL time.Duration
}

type bucket struct {
	tokens float64
	last   time.Time
	seen   time.Time
}

// ipRateLimiter holds per-IP token buckets. The now field is injectable so
// refill behaviour is deterministically testable without sleeping.
type ipRateLimiter struct {
	rps     float64
	burst   float64
	idleTTL time.Duration
	trusted []netip.Prefix

	now func() time.Time

	mu      sync.Mutex
	buckets map[netip.Addr]*bucket
	janitor sync.Once
}

// RateLimitMiddleware returns a Middleware that limits requests per client IP
// using a token bucket. On limit it responds 429 with a Retry-After header.
func RateLimitMiddleware(cfg RateLimit) Middleware {
	if cfg.RPS <= 0 {
		slog.Warn("fibery: RateLimitMiddleware disabled (RPS <= 0)")
		return func(next http.Handler) http.Handler { return next }
	}

	burst := cfg.Burst
	if burst <= 0 {
		burst = int(math.Ceil(cfg.RPS))
		if burst < 1 {
			burst = 1
		}
	}
	idleTTL := cfg.IdleTTL
	if idleTTL <= 0 {
		idleTTL = 10 * time.Minute
	}

	rl := &ipRateLimiter{
		rps:     cfg.RPS,
		burst:   float64(burst),
		idleTTL: idleTTL,
		trusted: cfg.Trusted,
		now:     time.Now,
		buckets: make(map[netip.Addr]*bucket),
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rl.startJanitor()
			if !rl.allow(ClientIP(r, rl.trusted)) {
				w.Header().Set("Retry-After", "1")
				respondWithError(w, RateLimited("rate limit exceeded", true))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// allow consumes a token for ip, refilling its bucket first. It reports whether
// the request is permitted.
func (rl *ipRateLimiter) allow(ip netip.Addr) bool {
	now := rl.now()

	rl.mu.Lock()
	defer rl.mu.Unlock()

	b, ok := rl.buckets[ip]
	if !ok {
		b = &bucket{tokens: rl.burst, last: now}
		rl.buckets[ip] = b
	}
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = min(rl.burst, b.tokens+elapsed*rl.rps)
		b.last = now
	}
	b.seen = now

	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

func (rl *ipRateLimiter) startJanitor() {
	rl.janitor.Do(func() {
		go func() {
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for range ticker.C {
				rl.sweep()
			}
		}()
	})
}

// sweep evicts buckets not seen within IdleTTL, bounding memory.
func (rl *ipRateLimiter) sweep() {
	cutoff := rl.now().Add(-rl.idleTTL)
	rl.mu.Lock()
	for ip, b := range rl.buckets {
		if b.seen.Before(cutoff) {
			delete(rl.buckets, ip)
		}
	}
	rl.mu.Unlock()
}
