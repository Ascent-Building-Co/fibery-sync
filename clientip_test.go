package fibery

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

func mustCIDRs(t *testing.T, entries ...string) []netip.Prefix {
	t.Helper()
	p, err := ParseCIDRs(entries...)
	if err != nil {
		t.Fatalf("ParseCIDRs(%v): %v", entries, err)
	}
	return p
}

func TestParseCIDRs(t *testing.T) {
	p, err := ParseCIDRs("10.0.0.0/8", "192.168.1.1", "::1", "2001:db8::/32", "  ", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(p) != 4 {
		t.Fatalf("expected 4 prefixes (blanks skipped), got %d: %v", len(p), p)
	}
	// Bare IPv4 becomes /32, bare IPv6 becomes /128.
	if p[1].Bits() != 32 {
		t.Fatalf("expected /32 for bare IPv4, got %d", p[1].Bits())
	}
	if p[2].Bits() != 128 {
		t.Fatalf("expected /128 for bare IPv6, got %d", p[2].Bits())
	}

	if _, err := ParseCIDRs("not-an-ip"); err == nil {
		t.Fatalf("expected error for invalid IP")
	}
	if _, err := ParseCIDRs("10.0.0.0/99"); err == nil {
		t.Fatalf("expected error for invalid CIDR")
	}
}

func TestClientIP(t *testing.T) {
	trusted := mustCIDRs(t, "10.0.0.0/8")

	tests := []struct {
		name    string
		remote  string
		xff     string
		xRealIP string
		trusted []netip.Prefix
		want    string
	}{
		{"direct peer, no trusted set", "203.0.113.5:1234", "", "", nil, "203.0.113.5"},
		{"untrusted peer ignores XFF (spoof attempt)", "203.0.113.5:1234", "1.2.3.4", "", trusted, "203.0.113.5"},
		{"trusted peer uses rightmost non-trusted XFF", "10.0.0.1:1234", "1.2.3.4, 10.0.0.9", "", trusted, "1.2.3.4"},
		{"trusted peer, chained proxies", "10.0.0.1:1234", "9.9.9.9, 10.0.0.5, 10.0.0.9", "", trusted, "9.9.9.9"},
		{"trusted peer, all XFF trusted, falls back to X-Real-IP", "10.0.0.1:1234", "10.0.0.9", "8.8.8.8", trusted, "8.8.8.8"},
		{"trusted peer, no forwarding headers", "10.0.0.1:1234", "", "", trusted, "10.0.0.1"},
		{"ipv4-in-ipv6 is unmapped", "[::ffff:203.0.113.7]:1234", "", "", nil, "203.0.113.7"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tt.remote
			if tt.xff != "" {
				req.Header.Set("X-Forwarded-For", tt.xff)
			}
			if tt.xRealIP != "" {
				req.Header.Set("X-Real-IP", tt.xRealIP)
			}
			got := ClientIP(req, tt.trusted)
			if got.String() != tt.want {
				t.Fatalf("ClientIP = %q, want %q", got.String(), tt.want)
			}
		})
	}
}

func TestClientIPInvalidRemote(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "garbage"
	if got := ClientIP(req, nil); got.IsValid() {
		t.Fatalf("expected invalid Addr for unparseable RemoteAddr, got %q", got.String())
	}
}
