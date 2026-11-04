package ygg

import (
	"net/http/httptest"
	"testing"
)

// The peer-trust rule decides whether X-Forwarded-For is believed at all. It
// used to be "loopback or any RFC1918 address", which is right for a same-host
// nginx and wrong for anything else that can reach the port: a peer on the
// container network could then pick the IP every rate limiter and audit row
// blames. The list is now configurable, and the default keeps the old behaviour
// so no deployment changes shape by accident.
func TestTrustedProxiesDefaultKeepsLocalTrust(t *testing.T) {
	ConfigureTrustedProxies(nil)
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.5:43210"
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.5")
	if got := clientIP(r); got != "10.0.0.5" {
		t.Fatalf("default trust: clientIP = %q, want the last XFF hop", got)
	}
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.RemoteAddr = "198.51.100.7:1234"
	r2.Header.Set("X-Forwarded-For", "203.0.113.9")
	if got := clientIP(r2); got != "198.51.100.7" {
		t.Fatalf("a public peer must never be trusted, got %q", got)
	}
}

func TestTrustedProxiesNarrowTheTrustZone(t *testing.T) {
	if err := ConfigureTrustedProxies([]string{"127.0.0.1/32", "::1/128"}); err != nil {
		t.Fatalf("configure: %v", err)
	}
	t.Cleanup(func() { ConfigureTrustedProxies(nil) })

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.5:43210"
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.5")
	if got := clientIP(r); got != "10.0.0.5" {
		t.Fatalf("narrowed trust: clientIP = %q, want the raw peer (XFF must be ignored)", got)
	}

	r2 := httptest.NewRequest("GET", "/", nil)
	r2.RemoteAddr = "127.0.0.1:8080"
	r2.Header.Set("X-Forwarded-For", "203.0.113.9, 127.0.0.1")
	if got := clientIP(r2); got != "127.0.0.1" {
		t.Fatalf("loopback proxy still trusted: clientIP = %q", got)
	}
}

func TestTrustedProxiesRejectGarbage(t *testing.T) {
	// A blank entry means "not configured" and falls back to the default zone —
	// that is the documented spelling of SKIN_TRUSTED_PROXIES="", so it must not
	// be an error. What must never pass is something that looks like an address
	// but is not a CIDR.
	for _, bad := range [][]string{{"10.0.0.0"}, {"not-a-cidr"}, {"10.0.0.0/33"}} {
		if err := ConfigureTrustedProxies(bad); err == nil {
			t.Fatalf("ConfigureTrustedProxies(%v) accepted a non-CIDR; a typo would silently widen trust", bad)
		}
	}
}
