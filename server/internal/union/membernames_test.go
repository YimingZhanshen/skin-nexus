package union

// The hub does not store member site names: /serverlist answers with
// {code,name,bs_root} and `name` is an empty string on all 53 entries (measured
// 2026-11-09 against the live hub, with a member key). The legacy plugin filled
// the column in the visitor's browser, fetching each member's own
// {bs_root}/api/yggdrasil and printing meta.serverName (servers.twig:22-33) —
// impossible here, because our CSP says connect-src 'self' and the browser
// blocks every one of those cross-origin reads before it leaves.
//
// So the read happens on the server, through the same guarded client the plugin
// and theme markets use: public addresses only, no redirects, one fixed path,
// capped body.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"skin-nexus/server/internal/outbound"
)

// fakeFetcher is the guard-side double: it records what the resolver asked for
// and answers per host, so a test can prove the resolver never asks for anything
// beyond the member's discovery path.
type fakeFetcher struct {
	mu      sync.Mutex
	urls    []string
	answers map[string]string // host -> serverName ("" = answer junk)
	status  int
	err     error
}

func (f *fakeFetcher) Fetch(_ context.Context, raw string) (outbound.FetchResult, error) {
	f.mu.Lock()
	f.urls = append(f.urls, raw)
	f.mu.Unlock()
	host := hostOf(raw)
	if f.err != nil {
		return outbound.FetchResult{}, f.err
	}
	name, known := f.answers[host]
	if !known {
		return outbound.FetchResult{Status: 404}, nil
	}
	if name == "" {
		return outbound.FetchResult{Status: 200, Body: []byte("<html>not json</html>")}, nil
	}
	return outbound.FetchResult{Status: 200, Body: []byte(`{"meta":{"serverName":"` + name + `"}}`)}, nil
}

func (f *fakeFetcher) asked() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.urls...)
}

func newFake(answers map[string]string) *fakeFetcher {
	return &fakeFetcher{answers: answers}
}

func TestMemberNameComesFromThePeerDiscoveryDocument(t *testing.T) {
	f := newFake(map[string]string{"skin.example.net": "Example Skin"})
	m := NewMemberNames(f, nil, MemberNamesOptions{})

	m.Fill([]ServerEntry{{Code: "EXMP", BSRoot: "https://skin.example.net"}})
	drain(m)

	got := m.Fill([]ServerEntry{{Code: "EXMP", BSRoot: "https://skin.example.net"}})
	if len(got) != 1 {
		t.Fatalf("entries = %d, want 1", len(got))
	}
	if got[0].Name != "Example Skin" {
		t.Fatalf("name = %q, want the peer's meta.serverName", got[0].Name)
	}
	want := "https://skin.example.net/api/yggdrasil"
	if asked := f.asked(); len(asked) == 0 || asked[0] != want {
		t.Fatalf("fetches = %v, want exactly %s", asked, want)
	}
}

// drain waits for a background sweep to finish; the resolver never blocks a
// request, which is the point of it.
func drain(m *MemberNames) {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if m.idle() {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

// What the resolver refuses to ask is the security promise, so it is asserted
// directly rather than left to a downstream error. The last row is the opposite
// case and the important one: a peer that publishes bs_root with a path or a
// query still gets exactly one request, at our own fixed discovery path — the
// alliance list cannot be used to make this station fetch something else.
func TestMemberNamesOnlyEveryDiscoveryPath(t *testing.T) {
	f := newFake(map[string]string{"skin.example.net": "Example"})
	m := NewMemberNames(f, nil, MemberNamesOptions{})
	m.Fill([]ServerEntry{
		{Code: "PRIV", BSRoot: "http://10.0.0.5"},
		{Code: "LOCAL", BSRoot: "http://127.0.0.1:8080"},
		{Code: "META", BSRoot: "http://169.254.169.254"},
		{Code: "FILE", BSRoot: "file:///etc/passwd"},
		{Code: "CREDS", BSRoot: "https://root:secret@skin.example.net"},
		{Code: "PATHED", BSRoot: "https://skin.example.net/admin/secret?debug=1#frag"},
	})
	drain(m)

	asked := f.asked()
	if len(asked) != 1 {
		t.Fatalf("fetches = %v, want exactly one", asked)
	}
	if asked[0] != "https://skin.example.net/api/yggdrasil" {
		t.Fatalf("asked %s, want the discovery path with the caller's path, query and fragment dropped", asked[0])
	}
}

// A peer that does not answer is retried on the shorter clock, not on every page
// open: a dead member site should cost one request per retry window, per visitor
// count zero.
func TestMemberNameRefusalIsRememberedBriefly(t *testing.T) {
	now := time.Unix(1700000000, 0)
	f := newFake(map[string]string{}) // every host 404s
	m := NewMemberNames(f, nil, MemberNamesOptions{Now: func() time.Time { return now }})
	list := []ServerEntry{{Code: "GONE", BSRoot: "https://gone.example.net"}}

	m.Fill(list)
	drain(m)
	m.Fill(list)
	drain(m)
	if got := len(f.asked()); got != 1 {
		t.Fatalf("fetches inside the retry window = %d, want 1", got)
	}

	now = now.Add(defaultMemberNameFailTTL + time.Second)
	m.Fill(list)
	drain(m)
	if got := len(f.asked()); got != 2 {
		t.Fatalf("fetches after the retry window = %d, want 2", got)
	}
}

// Off means off: no traffic outward, and no leftover third-party text either.
func TestMemberNamesDisabledAsksNothingAndKeepsNothing(t *testing.T) {
	on := true
	f := newFake(map[string]string{"skin.example.net": "Example"})
	m := NewMemberNames(f, func() bool { return on }, MemberNamesOptions{})
	list := []ServerEntry{{Code: "EXMP", BSRoot: "https://skin.example.net"}}

	if got := m.Fill(list); got[0].Name != "" {
		t.Fatalf("cold fill served %q before anything was read", got[0].Name)
	}
	drain(m)
	on = false
	if got := m.Fill(list); got[0].Name != "" {
		t.Fatalf("disabled resolver still served %q", got[0].Name)
	}
	if asked := len(f.asked()); asked != 1 {
		t.Fatalf("fetches = %d, want the single warm-up read only", asked)
	}
}

// Two page opens during one sweep must not queue a second sweep.
func TestMemberNameSweepIsSingleFlight(t *testing.T) {
	release := make(chan struct{})
	f := &blockingFetcher{release: release}
	m := NewMemberNames(f, nil, MemberNamesOptions{})
	list := []ServerEntry{{Code: "SLOW", BSRoot: "https://slow.example.net"}}

	m.Fill(list)
	for len(f.entered()) < 1 { // the sweep is demonstrably inside the fetch
		time.Sleep(time.Millisecond)
	}
	m.Fill(list)
	if m.idle() {
		t.Fatal("the first sweep should still be in flight")
	}
	close(release)
	drain(m)
	if got := len(f.entered()); got != 1 {
		t.Fatalf("fetches = %d, want 1 (one sweep at a time)", got)
	}
}

// The alliance list is known to repeat a code with a different address (live:
// YITUEM ships twice), and hosts repeat across rows too — one read per host.
func TestMemberNameReadsEachHostOnce(t *testing.T) {
	f := newFake(map[string]string{"skin.example.net": "Example"})
	m := NewMemberNames(f, nil, MemberNamesOptions{})

	m.Fill([]ServerEntry{
		{Code: "A", BSRoot: "https://skin.example.net"},
		{Code: "B", BSRoot: "https://skin.example.net/"}, // trailing slash: same target
	})
	drain(m)
	if asked := f.asked(); len(asked) != 1 {
		t.Fatalf("fetches = %v, want one per host", asked)
	}
	second := m.Fill([]ServerEntry{
		{Code: "A", BSRoot: "https://skin.example.net"},
		{Code: "B", BSRoot: "https://skin.example.net/"},
	})
	for i, e := range second {
		if e.Name != "Example" {
			t.Fatalf("row %d name = %q, want the one read to serve both rows", i, e.Name)
		}
	}
	if asked := f.asked(); len(asked) != 1 {
		t.Fatalf("fetches after warming = %v, want still one", asked)
	}
}

// Peer text lands inside our page. It is escaped by the template, but it must
// not dictate the shape of a table cell either.
func TestPeerNameIsFlattenedAndBounded(t *testing.T) {
	long := strings.Repeat("皮", 300)
	if got := sanitizePeerName("  First\n\tLine  \r\n second " + long); len([]rune(got)) > memberNameMaxRunes {
		t.Fatalf("len = %d runes, want at most %d", len([]rune(got)), memberNameMaxRunes)
	} else if strings.ContainsAny(got, "\n\r\t") || strings.HasPrefix(got, " ") {
		t.Fatalf("shape = %q, want flattened text", got)
	}
}

// The chain above swaps in a fake fetcher, which cannot catch a policy written
// wrong: if the guarded client's path allowlist did not admit
// /api/yggdrasil, every read would fail and the list would quietly keep showing
// addresses forever. These two go through a real *outbound.Scoped and a real
// HTTP peer.
//
// The peer is on loopback, so the client is built with AllowLoopback — the
// test-only hole in that policy. The companion test below leaves it closed and
// asserts the same peer becomes unreadable, which is what keeps that flag out of
// production config.
func TestMemberNamesReadsARealPeerThroughTheGuardedClient(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/yggdrasil" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("nope"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"meta":{"serverName":"Peer Skin Station"}}`))
	}))
	defer peer.Close()

	// Addressed as a hostname on purpose: the resolver refuses IP literals
	// outright, and that path has its own test. Here the question is whether the
	// guarded client admits the peer at all.
	root := asLocalhost(peer.URL)
	scoped := outbound.NewScoped(outbound.Policy{
		AllowLoopback:  true, // test peer only
		AllowUserHosts: true,
		UserHostPaths:  []string{"/api/yggdrasil"},
		MaxBytes:       128 << 10,
		Timeout:        2 * time.Second,
	})
	m := NewMemberNames(scoped, nil, MemberNamesOptions{})
	m.Fill([]ServerEntry{{Code: "PEER", BSRoot: root}})
	drain(m)

	got := m.Fill([]ServerEntry{{Code: "PEER", BSRoot: root}})
	if got[0].Name != "Peer Skin Station" {
		t.Fatalf("name = %q, want the peer's meta.serverName through the real guarded client", got[0].Name)
	}
}

// Same peer, guard left as production configures it: loopback is refused, so no
// name appears. If this ever starts passing, the egress guard is off.
func TestMemberNamesRefusesTheSamePeerWhenTheGuardIsOn(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"meta":{"serverName":"Should Never Be Read"}}`))
	}))
	defer peer.Close()

	root := asLocalhost(peer.URL)
	scoped := outbound.NewScoped(outbound.Policy{
		AllowUserHosts: true,
		UserHostPaths:  []string{"/api/yggdrasil"},
		MaxBytes:       128 << 10,
		Timeout:        2 * time.Second,
	})
	m := NewMemberNames(scoped, nil, MemberNamesOptions{})
	m.Fill([]ServerEntry{{Code: "LOCAL", BSRoot: root}})
	drain(m)

	got := m.Fill([]ServerEntry{{Code: "LOCAL", BSRoot: root}})
	if got[0].Name != "" {
		t.Fatalf("name = %q, want empty: a loopback peer must not be readable", got[0].Name)
	}
}

func asLocalhost(raw string) string {
	return strings.Replace(raw, "127.0.0.1", "localhost", 1)
}

type blockingFetcher struct {
	release chan struct{}
	mu      sync.Mutex
	seen    []string
}

func (b *blockingFetcher) Fetch(_ context.Context, raw string) (outbound.FetchResult, error) {
	b.mu.Lock()
	b.seen = append(b.seen, raw)
	b.mu.Unlock()
	<-b.release
	return outbound.FetchResult{Status: 200, Body: []byte(`{"meta":{"serverName":"Slow"}}`)}, nil
}

func (b *blockingFetcher) entered() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.seen...)
}
