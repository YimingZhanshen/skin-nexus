package outbound

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

func TestScopedUserHostPathSafety(t *testing.T) {
	var upstreamHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamHits.Add(1)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	base := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)

	cases := []struct {
		name    string
		path    string
		allowed bool
	}{
		{"plain path", "/ok", true},
		{"reverse proxy subpath", "/sub/ok/...", true},
		{"dots inside segment", "/ok/a..b", true},
		{"dot-prefixed segment", "/ok/.well-known", true},
		{"encoded dot-prefixed segment", "/ok/%2Ewell-known", true},
		{"dot segments in query", "/ok/file?next=../admin", true},
		{"plain parent segment", "/ok/../admin", false},
		{"encoded parent segment", "/ok/%2e%2e/admin", false},
		{"encoded current segment", "/ok/%2E/admin", false},
		{"encoded separator", "/ok/..%2fadmin", false},
		{"backslash separators", `/ok\..\admin`, false},
		{"backslash inside allowed prefix", "/ok/..\\admin", false},
		{"encoded backslash", "/ok/%5c..%5cadmin", false},
		{"NUL", "/ok/%00/admin", false},
		{"plain current segment", "/ok/./admin", false},
		{"double encoded parent", "/ok/%252e%252e/admin", false},
		{"double encoded current", "/ok/%252E/admin", false},
		{"double encoded separator", "/ok/..%252fadmin", false},
		{"double encoded backslash", "/ok/%255c..%255cadmin", false},
		{"double encoded NUL", "/ok/%2500/admin", false},
		{"malformed double encoding", "/ok/%25zz/admin", false},
		{"double encoded dot-prefixed segment", "/ok/%252Ewell-known", true},
		{"triple encoded parent", "/ok/%25252e%25252e/admin", false},
		{"triple encoded current", "/ok/%25252E/admin", false},
		{"triple encoded separator", "/ok/..%25252fadmin", false},
		{"triple encoded backslash", "/ok/%252525c..%252525cadmin", false},
		{"triple encoded NUL", "/ok/%25252500/admin", false},
		{"quadruple encoded parent", "/ok/%2525252e%2525252e/admin", false},
		{"malformed triple encoding", "/ok/%2525zz/admin", false},
		{"deeply encoded parent", "/ok/" + encodeOutboundTestPath("..", 12) + "/admin", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := testScoped(t, 16)
			before := upstreamHits.Load()
			res, err := s.Fetch(context.Background(), base+c.path)
			if c.allowed {
				if err != nil || res.Status != 200 || string(res.Body) != "ok" {
					t.Fatalf("fetch = %+v, %v; want upstream response", res, err)
				}
				if got := upstreamHits.Load() - before; got != 1 {
					t.Fatalf("upstream hits = %d, want 1", got)
				}
				return
			}
			if err == nil {
				t.Fatal("unsafe path was allowed")
			}
			policyErr, ok := err.(*PolicyError)
			if !ok || policyErr.Code != "net.blocked_host" {
				t.Fatalf("error = %v, want net.blocked_host", err)
			}
			if got := upstreamHits.Load() - before; got != 0 {
				t.Fatalf("unsafe path reached upstream %d times", got)
			}
		})
	}
}

func TestUserHostPathSafeBoundsRepeatedDecoding(t *testing.T) {
	for _, tc := range []struct {
		name   string
		passes int
		want   bool
	}{
		{"safe residual stable at the decode bound", 8, true},
		{"safe residual still changing after the decode bound", 9, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := "/ok/" + encodeOutboundTestPath(".well-known", tc.passes)
			if got := userHostPathSafe(path); got != tc.want {
				t.Fatalf("userHostPathSafe(%q) = %v, want %v", path, got, tc.want)
			}
		})
	}
}

func encodeOutboundTestPath(value string, passes int) string {
	for i := 0; i < passes; i++ {
		value = strings.ReplaceAll(value, "%", "%25")
		value = strings.ReplaceAll(value, ".", "%2e")
	}
	return value
}

func TestScopedStaticHostKeepsPathPolicy(t *testing.T) {
	var upstreamHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamHits.Add(1)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	base := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)

	for _, allowUserHosts := range []bool{false, true} {
		s := NewScoped(Policy{
			Hosts:          []string{"localhost"},
			AllowUserHosts: allowUserHosts,
			UserHostPaths:  []string{"/ok"},
			AllowLoopback:  true, // test-only
			MaxBytes:       16,
			Timeout:        time.Second,
		})
		before := upstreamHits.Load()
		res, err := s.Fetch(context.Background(), base+"/ok/../admin")
		if err != nil || res.Status != 200 || string(res.Body) != "ok" {
			t.Fatalf("AllowUserHosts=%v: fetch = %+v, %v; static host must retain its policy", allowUserHosts, res, err)
		}
		if got := upstreamHits.Load() - before; got != 1 {
			t.Fatalf("AllowUserHosts=%v: upstream hits = %d, want 1", allowUserHosts, got)
		}
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
