package ygg

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const fallbackCanned = `{"id":"000000000000000000000000000000ff","name":"Official","properties":[{"name":"textures","value":"dmFsdWU="}]}`

// stubMojang records every request and answers per the given handler.
type stubMojang struct {
	hits atomic.Int32
	last atomic.Value // string, the last RawPath+RawQuery
	srv  *httptest.Server
}

func newStubMojang(t *testing.T, h http.HandlerFunc) *stubMojang {
	t.Helper()
	s := &stubMojang{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits.Add(1)
		s.last.Store(r.URL.RequestURI())
		h(w, r)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *stubMojang) fallback() *Fallback {
	return &Fallback{
		BaseURL: s.srv.URL,
		// Same client policy as production (deadline + no-follow); plain
		// transport because the stub is loopback — the guarded dialer is
		// covered by TestPublicDialContextRefusesNonPublic.
		Client:  newFallbackClient(http.DefaultTransport),
		Enabled: func(context.Context) bool { return true },
	}
}

func okJSON(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(fallbackCanned))
}

func TestFallbackProfilePassthrough(t *testing.T) {
	stub := newStubMojang(t, okJSON)
	f := stub.fallback()

	body, ok := f.profile(context.Background(), "00000000-0000-0000-0000-0000000000ff", true)
	if !ok || string(body) != fallbackCanned {
		t.Fatalf("passthrough = (%q, %v)", body, ok)
	}
	// Hyphenated input normalizes; signed request adds no unsigned hint.
	if got := stub.last.Load().(string); got != "/session/profile/000000000000000000000000000000ff" {
		t.Errorf("request path = %q", got)
	}
	// Unsigned request must strip signatures upstream.
	if _, ok := f.profile(context.Background(), strings.Repeat("0", 32), false); !ok {
		t.Fatal("unsigned fetch failed")
	}
	if got := stub.last.Load().(string); !strings.Contains(got, "unsigned=true") {
		t.Errorf("unsigned request missing hint: %q", got)
	}
}

func TestFallbackProfileRejectsNonHexUUID(t *testing.T) {
	stub := newStubMojang(t, okJSON)
	f := stub.fallback()
	for _, bad := range []string{
		"../../etc/passwd",
		"zz000000000000000000000000000000ff", // non-hex (NormalizeUUID keeps z)
		"short",
		"",
		strings.Repeat("0", 31),
	} {
		if _, ok := f.profile(context.Background(), bad, true); ok {
			t.Errorf("uuid %q must not fetch", bad)
		}
	}
	if n := stub.hits.Load(); n != 0 {
		t.Errorf("stub hit %d times, want 0", n)
	}
}

func TestFallbackHasJoinedParams(t *testing.T) {
	stub := newStubMojang(t, okJSON)
	f := stub.fallback()

	if _, ok := f.hasJoined(context.Background(), "Notch", "server-id-1", "203.0.113.9"); !ok {
		t.Fatal("valid hasJoined fetch failed")
	}
	uri := stub.last.Load().(string)
	for _, want := range []string{"username=Notch", "serverId=server-id-1", "ip=203.0.113.9"} {
		if !strings.Contains(uri, want) {
			t.Errorf("query missing %q: %s", want, uri)
		}
	}
	// Malformed IP hint is dropped (would make upstream 400 the join).
	if _, ok := f.hasJoined(context.Background(), "Notch", "sid", "not-an-ip"); !ok {
		t.Fatal("fetch with bad ip hint failed")
	}
	if uri := stub.last.Load().(string); strings.Contains(uri, "ip=") {
		t.Errorf("bad ip hint forwarded: %s", uri)
	}
	// Site-local names (CJK / over-long) never leave the box.
	before := stub.hits.Load()
	for _, name := range []string{"史蒂夫", "a b", strings.Repeat("x", 17), ""} {
		if _, ok := f.hasJoined(context.Background(), name, "sid", ""); ok {
			t.Errorf("name %q must not fetch", name)
		}
	}
	if stub.hits.Load() != before {
		t.Errorf("invalid names hit the stub %d times", stub.hits.Load()-before)
	}
}

func TestFallbackDisabled(t *testing.T) {
	stub := newStubMojang(t, okJSON)
	var nilF *Fallback
	if _, ok := nilF.profile(context.Background(), strings.Repeat("0", 32), true); ok {
		t.Error("nil fallback must answer no")
	}
	f := stub.fallback()
	f.Enabled = nil
	if _, ok := f.profile(context.Background(), strings.Repeat("0", 32), true); ok {
		t.Error("nil Enabled must answer no")
	}
	f.Enabled = func(context.Context) bool { return false }
	if _, ok := f.hasJoined(context.Background(), "Notch", "sid", ""); ok {
		t.Error("disabled toggle must answer no")
	}
	if stub.hits.Load() != 0 {
		t.Errorf("disabled paths hit the stub %d times", stub.hits.Load())
	}
}

func TestFallbackResponseGates(t *testing.T) {
	cases := []struct {
		name string
		h    http.HandlerFunc
		want bool
	}{
		{"200 valid", okJSON, true},
		{"404", func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) }, false},
		{"500", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }, false},
		{"204", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }, false},
		{"html captive portal", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("<html><body>login</body></html>"))
		}, false},
		{"json without id/name", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"error":"gone"}`))
		}, false},
		{"oversized", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"id":"` + strings.Repeat("a", fallbackMaxBody+16) + `","name":"x"}`))
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stub := newStubMojang(t, c.h)
			got, ok := stub.fallback().profile(context.Background(), strings.Repeat("0", 32), true)
			if ok != c.want {
				t.Fatalf("ok = %v, want %v (body %q)", ok, c.want, got)
			}
		})
	}
}

func TestFallbackDoesNotFollowRedirects(t *testing.T) {
	var internalHits atomic.Int32
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		internalHits.Add(1)
	}))
	t.Cleanup(internal.Close)
	stub := newStubMojang(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL+"/secret", http.StatusFound)
	})
	if _, ok := stub.fallback().profile(context.Background(), strings.Repeat("0", 32), true); ok {
		t.Fatal("redirect must not count as a hit")
	}
	if internalHits.Load() != 0 {
		t.Errorf("redirect target hit %d times", internalHits.Load())
	}
}

func TestPublicDialContextRefusesNonPublic(t *testing.T) {
	for _, addr := range []string{
		"127.0.0.1:443",
		"10.1.2.3:443",
		"192.168.0.1:443",
		"172.16.9.9:443",
		"100.64.0.1:443", // CGNAT
		"169.254.1.1:443",
		"[::1]:443",
		"[fe80::1]:443",
		"[fc00::1]:443",
	} {
		conn, err := publicDialContext(context.Background(), "tcp", addr)
		if err == nil {
			conn.Close()
			t.Errorf("dial %s must be refused", addr)
		}
	}
}

func TestIsPublicIP(t *testing.T) {
	cases := map[string]bool{
		"8.8.8.8":         true,
		"1.1.1.1":         true,
		"2606:4700::1111": true,
		"127.0.0.1":       false,
		"10.0.0.1":        false,
		"172.20.0.5":      false,
		"192.168.1.1":     false,
		"100.64.5.5":      false,
		"169.254.0.1":     false,
		"0.0.0.0":         false,
		"224.0.0.1":       false, // multicast
		"::1":             false,
		"fc00::1":         false,
		"fe80::1":         false,
	}
	for s, want := range cases {
		if got := isPublicIP(net.ParseIP(s)); got != want {
			t.Errorf("isPublicIP(%s) = %v, want %v", s, got, want)
		}
	}
}

// ---- handler-level wiring ----

func TestHasJoinedFallbackFailsClosedForLocalAccountDenial(t *testing.T) {
	for _, tc := range []struct {
		name       string
		permission int
		lookupErr  error
	}{
		{name: "banned", permission: -1},
		{name: "permission lookup failure", lookupErr: errors.New("permission lookup failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, st := newTestAPI(t)
			st.permissions[1] = tc.permission
			st.permissionErr = tc.lookupErr
			stub := newStubMojang(t, okJSON)
			api.SetFallback(stub.fallback())

			rec := do(t, api.Handler(), http.MethodGet,
				"/api/yggdrasil/sessionserver/session/minecraft/hasJoined?username=Steve&serverId=abc", "", nil)
			if rec.Code != http.StatusNoContent {
				t.Fatalf("denied local account hasJoined = %d %s, want 204", rec.Code, rec.Body.String())
			}
			if hits := stub.hits.Load(); hits != 0 {
				t.Fatalf("denied local account consulted official fallback %d times", hits)
			}
		})
	}
}

func TestProfileFallbackViaHandler(t *testing.T) {
	api, _ := newTestAPI(t)
	stub := newStubMojang(t, okJSON)
	api.SetFallback(stub.fallback())

	// Unknown-to-us but valid hex UUID → official body,200 (was 204).
	rec := do(t, api.Handler(), "GET",
		"/api/yggdrasil/sessionserver/session/minecraft/profile/000000000000000000000000000000ff", "", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"name":"Official"`) {
		t.Fatalf("fallback profile = %d %s", rec.Code, rec.Body.String())
	}
	// The LOCAL hit must never consult upstream (local wins).
	before := stub.hits.Load()
	rec = do(t, api.Handler(), "GET",
		"/api/yggdrasil/sessionserver/session/minecraft/profile/"+testUUID, "", nil)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "Official") {
		t.Fatalf("local profile changed: %d %s", rec.Code, rec.Body.String())
	}
	if stub.hits.Load() != before {
		t.Errorf("local hit consulted upstream %d times", stub.hits.Load()-before)
	}
	// Unknown player with an official-shaped name → forwarded join check.
	rec = do(t, api.Handler(), "GET",
		"/api/yggdrasil/sessionserver/session/minecraft/hasJoined?username=Notch&serverId=abc", "", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"name":"Official"`) {
		t.Fatalf("fallback hasJoined = %d %s", rec.Code, rec.Body.String())
	}
	// Toggle off → back to the legacy empty answers.
	api.SetFallback(&Fallback{BaseURL: stub.srv.URL, Client: stub.srv.Client()})
	rec = do(t, api.Handler(), "GET",
		"/api/yggdrasil/sessionserver/session/minecraft/profile/000000000000000000000000000000ff", "", nil)
	if rec.Code != 204 {
		t.Fatalf("disabled fallback must 204, got %d", rec.Code)
	}
}
