package fibsync

import (
	"log/slog"
	"net/http"
	"net/netip"
)

// FiberyEgressCIDRs lists Fibery's known synchronizer egress IP addresses.
// They are stable in practice but not contractually guaranteed; if Fibery
// introduces additional ranges, extend the allowlist via configuration rather
// than editing this list. Note these cover Fibery's synchronizer traffic —
// third-party-facing endpoints (for example a webhook relay hit directly by the
// upstream provider) must be exempted from the allowlist, e.g. with SkipWhen.
var FiberyEgressCIDRs = []string{
	"18.198.169.41",
	"18.196.77.165",
	"3.124.79.168",
}

// FiberyEgressPrefixes returns FiberyEgressCIDRs parsed as prefixes, suitable
// for IPAllowlist.Allowed. It panics only if FiberyEgressCIDRs has been
// modified to contain an invalid entry (a programmer error).
func FiberyEgressPrefixes() []netip.Prefix {
	prefixes, err := ParseCIDRs(FiberyEgressCIDRs...)
	if err != nil {
		panic("fibery: invalid FiberyEgressCIDRs: " + err.Error())
	}
	return prefixes
}

// IPAllowlist configures IPAllowlistMiddleware.
type IPAllowlist struct {
	// Allowed lists the CIDRs permitted to reach the wrapped handler. An empty
	// list fails closed: every request is denied.
	Allowed []netip.Prefix
	// Trusted lists proxy CIDRs whose forwarding headers may be believed when
	// resolving the client IP. See ClientIP.
	Trusted []netip.Prefix
}

// IPAllowlistMiddleware returns a Middleware that permits only requests whose
// resolved client IP (see ClientIP) falls within one of the Allowed prefixes.
// All other requests receive 403 Forbidden. An empty allowlist denies every
// request (fail closed).
func IPAllowlistMiddleware(cfg IPAllowlist) Middleware {
	if len(cfg.Allowed) == 0 {
		slog.Warn("fibery: IPAllowlistMiddleware allowlist is empty; all requests will be denied")
	}
	allowed := cfg.Allowed
	trusted := cfg.Trusted

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !prefixesContain(allowed, ClientIP(r, trusted)) {
				respondWithError(w, Forbidden("forbidden"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
