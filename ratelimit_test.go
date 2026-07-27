package fibery

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

func TestRateLimiterAllowBurstAndRefill(t *testing.T) {
	clock := time.Unix(0, 0).UTC()
	rl := &ipRateLimiter{
		rps:     1,
		burst:   3,
		idleTTL: time.Minute,
		now:     func() time.Time { return clock },
		buckets: make(map[netip.Addr]*bucket),
	}
	ip := netip.MustParseAddr("1.2.3.4")

	// Full bucket allows Burst requests immediately.
	for i := 0; i < 3; i++ {
		if !rl.allow(ip) {
			t.Fatalf("expected allow on burst request %d", i)
		}
	}
	if rl.allow(ip) {
		t.Fatalf("expected deny once burst is exhausted")
	}

	// After 2 seconds at 1 rps, 2 tokens are available again.
	clock = clock.Add(2 * time.Second)
	if !rl.allow(ip) || !rl.allow(ip) {
		t.Fatalf("expected 2 allows after refill")
	}
	if rl.allow(ip) {
		t.Fatalf("expected deny after refilled tokens consumed")
	}

	// A different IP has an independent, full bucket.
	if !rl.allow(netip.MustParseAddr("5.6.7.8")) {
		t.Fatalf("expected independent bucket for a different IP to allow")
	}
}

func TestRateLimiterSweep(t *testing.T) {
	clock := time.Unix(0, 0).UTC()
	rl := &ipRateLimiter{
		rps:     1,
		burst:   1,
		idleTTL: time.Minute,
		now:     func() time.Time { return clock },
		buckets: make(map[netip.Addr]*bucket),
	}
	rl.allow(netip.MustParseAddr("1.2.3.4"))
	if len(rl.buckets) != 1 {
		t.Fatalf("expected 1 bucket, got %d", len(rl.buckets))
	}

	clock = clock.Add(2 * time.Minute) // past IdleTTL
	rl.sweep()
	if len(rl.buckets) != 0 {
		t.Fatalf("expected idle bucket to be evicted, got %d", len(rl.buckets))
	}
}

func TestRateLimitMiddleware429(t *testing.T) {
	h := RateLimitMiddleware(RateLimit{RPS: 1, Burst: 1})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)

	call := func() *http.Response {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.RemoteAddr = "9.9.9.9:1111"
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		return res.Result()
	}

	if got := call().StatusCode; got != http.StatusOK {
		t.Fatalf("expected first request 200, got %d", got)
	}
	second := call()
	if second.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected second request 429, got %d", second.StatusCode)
	}
	if ra := second.Header.Get("Retry-After"); ra != "1" {
		t.Fatalf("expected Retry-After 1, got %q", ra)
	}
}

func TestRateLimitMiddlewareDisabled(t *testing.T) {
	h := RateLimitMiddleware(RateLimit{RPS: 0})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)
	for i := 0; i < 100; i++ {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.RemoteAddr = "9.9.9.9:1111"
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if res.Result().StatusCode != http.StatusOK {
			t.Fatalf("expected disabled limiter to pass all requests, got %d on %d", res.Result().StatusCode, i)
		}
	}
}
