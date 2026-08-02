package fibsync

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFiberyEgressPrefixes(t *testing.T) {
	p := FiberyEgressPrefixes()
	if len(p) != len(FiberyEgressCIDRs) {
		t.Fatalf("expected %d prefixes, got %d", len(FiberyEgressCIDRs), len(p))
	}
	for _, pref := range p {
		if pref.Bits() != 32 {
			t.Fatalf("expected /32 host prefix, got %s", pref)
		}
	}
}

func TestSkipWhen(t *testing.T) {
	// A middleware that always rejects, so we can observe whether it ran.
	deny := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			respondWithError(w, Forbidden("denied"))
		})
	}
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	call := func(h http.Handler, path string) int {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		return res.Result().StatusCode
	}

	skip := SkipWhen(deny, func(r *http.Request) bool {
		return r.URL.Path == "/exempt"
	})(ok)

	if code := call(skip, "/exempt"); code != http.StatusOK {
		t.Fatalf("expected exempt path to bypass middleware (200), got %d", code)
	}
	if code := call(skip, "/guarded"); code != http.StatusForbidden {
		t.Fatalf("expected guarded path to run middleware (403), got %d", code)
	}

	// nil skip => middleware always runs.
	always := SkipWhen(deny, nil)(ok)
	if code := call(always, "/exempt"); code != http.StatusForbidden {
		t.Fatalf("expected nil skip to always apply middleware (403), got %d", code)
	}
}

func TestIPAllowlistMiddleware(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	allowed := mustCIDRs(t, "203.0.113.0/24")

	call := func(h http.Handler, remote, xff string) int {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.RemoteAddr = remote
		if xff != "" {
			req.Header.Set("X-Forwarded-For", xff)
		}
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		return res.Result().StatusCode
	}

	t.Run("in-range allowed", func(t *testing.T) {
		h := IPAllowlistMiddleware(IPAllowlist{Allowed: allowed})(ok)
		if code := call(h, "203.0.113.5:1", ""); code != http.StatusOK {
			t.Fatalf("expected 200 for allowed IP, got %d", code)
		}
	})

	t.Run("out-of-range denied", func(t *testing.T) {
		h := IPAllowlistMiddleware(IPAllowlist{Allowed: allowed})(ok)
		if code := call(h, "8.8.8.8:1", ""); code != http.StatusForbidden {
			t.Fatalf("expected 403 for disallowed IP, got %d", code)
		}
	})

	t.Run("empty allowlist denies all", func(t *testing.T) {
		h := IPAllowlistMiddleware(IPAllowlist{})(ok)
		if code := call(h, "203.0.113.5:1", ""); code != http.StatusForbidden {
			t.Fatalf("expected 403 with empty allowlist, got %d", code)
		}
	})

	t.Run("client IP via trusted proxy", func(t *testing.T) {
		h := IPAllowlistMiddleware(IPAllowlist{
			Allowed: allowed,
			Trusted: mustCIDRs(t, "10.0.0.0/8"),
		})(ok)
		// Peer is a trusted proxy; the real client (in XFF) is allowed.
		if code := call(h, "10.0.0.1:1", "203.0.113.9"); code != http.StatusOK {
			t.Fatalf("expected 200 for allowed client behind trusted proxy, got %d", code)
		}
		// Same proxy, disallowed real client.
		if code := call(h, "10.0.0.1:1", "8.8.8.8"); code != http.StatusForbidden {
			t.Fatalf("expected 403 for disallowed client behind trusted proxy, got %d", code)
		}
	})
}
