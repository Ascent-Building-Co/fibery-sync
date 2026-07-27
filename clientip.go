package fibery

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ParseCIDRs parses CIDR strings or bare IP addresses into prefixes. A bare
// IPv4 address becomes a /32 and a bare IPv6 address becomes a /128. Empty
// entries are skipped. It is intended to be called once at startup, where the
// returned error can be handled, and the result passed to the IP-based
// middlewares (IPAllowlistMiddleware, RateLimitMiddleware) or ClientIP.
func ParseCIDRs(entries ...string) ([]netip.Prefix, error) {
	prefixes := make([]netip.Prefix, 0, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			p, err := netip.ParsePrefix(entry)
			if err != nil {
				return nil, fmt.Errorf("invalid CIDR %q: %w", entry, err)
			}
			prefixes = append(prefixes, p.Masked())
			continue
		}
		addr, err := netip.ParseAddr(entry)
		if err != nil {
			return nil, fmt.Errorf("invalid IP %q: %w", entry, err)
		}
		addr = addr.Unmap()
		prefixes = append(prefixes, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return prefixes, nil
}

// ClientIP resolves the originating client IP for a request.
//
// The peer address (from RemoteAddr) is trusted by default. Forwarding headers
// are consulted only when the peer is within one of the trusted prefixes,
// preventing a direct client from spoofing its address:
//
//   - Peer not in trusted: the peer address is returned; X-Forwarded-For and
//     X-Real-IP are ignored.
//   - Peer in trusted: X-Forwarded-For is walked right-to-left and the first
//     address that is not itself a trusted proxy is returned (the real client
//     as seen by the outermost trusted hop). If that yields nothing, X-Real-IP
//     is used, and failing that the peer address.
//
// The returned address is IPv4-unmapped. An invalid RemoteAddr yields the zero
// netip.Addr (which no prefix contains, so allowlists fail closed).
func ClientIP(r *http.Request, trusted []netip.Prefix) netip.Addr {
	peer := peerAddr(r.RemoteAddr)
	if !peer.IsValid() || !prefixesContain(trusted, peer) {
		return peer
	}

	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		for i := len(parts) - 1; i >= 0; i-- {
			addr, err := netip.ParseAddr(strings.TrimSpace(parts[i]))
			if err != nil {
				continue
			}
			addr = addr.Unmap()
			if !prefixesContain(trusted, addr) {
				return addr
			}
		}
	}

	if xr := strings.TrimSpace(r.Header.Get("X-Real-IP")); xr != "" {
		if addr, err := netip.ParseAddr(xr); err == nil {
			return addr.Unmap()
		}
	}

	return peer
}

// peerAddr extracts the IP from a net/http RemoteAddr ("host:port" or bare
// host), returning the zero Addr if it cannot be parsed.
func peerAddr(remoteAddr string) netip.Addr {
	host := remoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = h
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	return addr.Unmap()
}

// prefixesContain reports whether addr falls within any of the prefixes.
func prefixesContain(prefixes []netip.Prefix, addr netip.Addr) bool {
	for _, p := range prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}
