package outbound

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testScoped(t *testing.T, maxBytes int64) *Scoped {
	t.Helper()
	return NewScoped(Policy{
		AllowUserHosts: true,
		UserHostPaths:  []string{"/ok", "/large", "/redirect"},
		AllowLoopback:  true, // test-only
		MaxBytes:       maxBytes,
		Timeout:        time.Second,
	})
}

func TestScopedFetchAndErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			_, _ = w.Write([]byte("png"))
		case "/large":
			_, _ = w.Write([]byte(strings.Repeat("x", 64)))
		case "/redirect":
			http.Redirect(w, r, "/ok", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	base := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)

	s := testScoped(t, 16)
	res, err := s.Fetch(context.Background(), base+"/ok")
	if err != nil || res.Status != 200 || string(res.Body) != "png" {
		t.Fatalf("fetch ok = %+v %v", res, err)
	}
	if _, err := s.Fetch(context.Background(), base+"/large"); err == nil || err.(*PolicyError).Code != "net.too_large" {
		t.Fatalf("large error = %v", err)
	}
	if _, err := s.Fetch(context.Background(), base+"/redirect"); err == nil || err.(*PolicyError).Code != "net.blocked_redirect" {
		t.Fatalf("redirect error = %v", err)
	}
	if _, err := s.Fetch(context.Background(), base+"/blocked"); err == nil || err.(*PolicyError).Code != "net.blocked_host" {
		t.Fatalf("path error = %v", err)
	}
	if _, err := s.Fetch(context.Background(), "http://user:pass@localhost/ok"); err == nil || err.(*PolicyError).Code != "net.invalid_url" {
		t.Fatalf("userinfo error = %v", err)
	}
}

func TestScopedRejectsPrivatePeerInProduction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	s := NewScoped(Policy{
		Hosts: []string{"localhost"}, UserHostPaths: []string{"/ok"},
		AllowUserHosts: true, MaxBytes: 16, Timeout: time.Second,
	})
	url := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1) + "/ok"
	if _, err := s.Fetch(context.Background(), url); err == nil {
		t.Fatal("loopback peer should be rejected without test override")
	}
}

func TestScopedRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	s := testScoped(t, 16)
	base := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1) + "/ok"
	for i := 0; i < 4; i++ {
		if _, err := s.Fetch(context.Background(), base); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	if _, err := s.Fetch(context.Background(), base); err == nil || err.(*PolicyError).Code != "net.rate_limited" {
		t.Fatalf("fifth request error = %v", err)
	}
}

func TestPathAllowedSegments(t *testing.T) {
	cases := []struct {
		path     string
		prefixes []string
		want     bool
		why      string
	}{
		{"/textures/a.png", []string{"/textures/"}, true, "the plain case the importer needs"},
		{"/sub/textures/a.png", []string{"/textures/"}, true, "a reverse-proxied install under /sub/"},
		{"/textures", []string{"/textures/"}, true, "the directory itself"},
		{"/nottextures/a", []string{"/textures/"}, false, "a longer word that merely ends in the prefix"},
		{"/a/btextures/c", []string{"textures/"}, false, "a prefix that is not absolute is ignored"},
		{"/a/btextures/c", []string{"/textures/"}, false, "wildcards inside a segment do not count"},
		{"/anything", []string{"/"}, true, "an explicit root rule means any path"},
		{"/textures/a", nil, false, "no rule, no access"},
	}
	for _, c := range cases {
		if got := pathAllowed(c.path, c.prefixes); got != c.want {
			t.Errorf("pathAllowed(%q, %v) = %v, want %v — %s", c.path, c.prefixes, got, c.want, c.why)
		}
	}
}

func TestPublicIPTunnelsAreNotPublic(t *testing.T) {
	// 6to4 embeds the v4 destination in the address, so 2002:0a00:0001:: is
	// 10.0.0.1 wearing a global prefix; Teredo encodes the peer the same way.
	for _, s := range []string{"2002:0a00:0001::", "2002:4008:0808::", "2001:0000:4136:e000:0:0:0:1"} {
		if isPublicIP(net.ParseIP(s)) {
			t.Errorf("%s must not count as public — it is a transition address that can reach a private peer", s)
		}
	}
}
