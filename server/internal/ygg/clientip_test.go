package ygg

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// clientIP semantics lock (B4, 2026-10-24): regs_per_ip counting and every
// IP rate-limit window depend on this behavior. A regression here silently
// degrades EVERY caller behind nginx to one shared proxy IP (three
// registrations would lock the whole site out) or reopens the spoofing
// hole review P1 closed (2026-10-23: trusting the FIRST XFF entry let any
// caller rotate fake values to dodge the sliding window).
//
// Kept out of http_test.go (frozen contract file).

func TestClientIPTrustedProxyTakesLastXFF(t *testing.T) {
	// baota nginx: proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for
	// — client-supplied junk rides along in front, the REAL address is the
	// last entry (the hop our proxy appended).
	r := newTestRequest("172.17.0.1:53422", "")
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8, 203.0.113.9")
	if got := ClientIP(r); got != "203.0.113.9" {
		t.Fatalf("trusted proxy: ClientIP = %q, want last hop 203.0.113.9", got)
	}
}

func TestClientIPLoopbackProxyTakesLastXFF(t *testing.T) {
	// test-site topology: nginx proxies to 127.0.0.1:8080 on the same host.
	r := newTestRequest("127.0.0.1:40122", "203.0.113.9")
	if got := ClientIP(r); got != "203.0.113.9" {
		t.Fatalf("loopback proxy: ClientIP = %q, want 203.0.113.9", got)
	}
}

func TestClientIPEmptyXFFOnTrustedPeerFallsBack(t *testing.T) {
	// XFF absent (e.g. a probe hitting the container directly from the
	// docker network): fall back to the peer — never empty, never panic.
	r := newTestRequest("172.17.0.1:53422", "")
	if got := ClientIP(r); got != "172.17.0.1" {
		t.Fatalf("empty XFF: ClientIP = %q, want peer 172.17.0.1", got)
	}
}

func TestClientIPIgnoresSpoofOnUntrustedPeer(t *testing.T) {
	// Direct public connection with a forged XFF: the peer is NOT a trusted
	// proxy, so the header is dropped entirely (review P1 hole stays shut).
	r := newTestRequest("198.51.100.7:60001", "203.0.113.9")
	if got := ClientIP(r); got != "198.51.100.7" {
		t.Fatalf("untrusted peer: ClientIP = %q, want direct peer (XFF ignored)", got)
	}
}

func TestClientIPPortSplitKeepsIPv4AndBracketedIPv6(t *testing.T) {
	r := newTestRequest("198.51.100.7:12345", "")
	if got := ClientIP(r); got != "198.51.100.7" {
		t.Fatalf("ipv4 port split: ClientIP = %q", got)
	}
	r = newTestRequest("[2001:db8::1]:12345", "")
	if got := ClientIP(r); got != "[2001:db8::1]" {
		t.Fatalf("ipv6 bracket form: ClientIP = %q", got)
	}
	// bracketed IPv6 peer inside the trust zone still resolves XFF
	r = newTestRequest("[::1]:9999", "203.0.113.9")
	if got := ClientIP(r); got != "203.0.113.9" {
		t.Fatalf("loopback v6 proxy: ClientIP = %q, want 203.0.113.9", got)
	}
}

func TestClientIPCDNHeaders(t *testing.T) {
	ConfigureClientIP(true, []string{"CF-Connecting-IP", "Ali-CDN-Real-IP", "X-Forwarded-For"})
	defer ConfigureClientIP(false, nil)

	r := newTestRequest("172.17.0.1:53422", "")
	r.Header.Set("CF-Connecting-IP", "203.0.113.9")
	if got := ClientIP(r); got != "203.0.113.9" {
		t.Fatalf("cdn header: ClientIP = %q, want 203.0.113.9", got)
	}

	// Invalid first header falls through to the next configured header.
	r.Header.Set("CF-Connecting-IP", "not-an-ip")
	r.Header.Set("Ali-CDN-Real-IP", "198.51.100.7:443")
	if got := ClientIP(r); got != "198.51.100.7" {
		t.Fatalf("cdn fallback: ClientIP = %q, want 198.51.100.7", got)
	}

	// A direct public peer still wins; CDN headers are not trusted there.
	r.RemoteAddr = "198.51.100.7:60001"
	if got := ClientIP(r); got != "198.51.100.7" {
		t.Fatalf("untrusted peer: ClientIP = %q, want direct peer", got)
	}

	// XFF remains last-hop even in CDN mode (leading entries are spoofable).
	r.RemoteAddr = "172.17.0.1:53422"
	r.Header.Del("CF-Connecting-IP")
	r.Header.Del("Ali-CDN-Real-IP")
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8, 203.0.113.9")
	if got := ClientIP(r); got != "203.0.113.9" {
		t.Fatalf("cdn xff fallback: ClientIP = %q, want last hop", got)
	}
}

// newTestRequest builds a request with only RemoteAddr (+ optional XFF) set.
func newTestRequest(remote, xff string) *http.Request {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = remote
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	return r
}
