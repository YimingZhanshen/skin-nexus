package ygg

// skindomains_test.go — ticket slice 2 (skinDomains portless twin).
//
// The published list is built by API.skinDomains (http.go): configured
// domains ∪ ExtraSkinDomains ∪ the site_url host ∪ the request Host. A
// port-bearing entry (e.g. "site.invalid:8443") can never match on a client
// like authlib-injector, which compares against URI.getHost() — a host with NO
// port — so the launcher loads the account yet skins silently fail (see
// analysis/g10-e3-protocol-surface.md, Q2 finding 1).
//
// The change under test is deliberately additive: for every entry that carries
// an explicit port, ALSO publish the same host without the port, deduplicated,
// with a stable order, and with every pre-existing entry left untouched and in
// its original position. These tests assert that shape against what the code
// actually produces: r.Host is echoed verbatim (a proxied Host header may carry
// a port) and the site_url host comes from url.Parse(Homepage).Host, which
// keeps a port when the URL was written with one.
//
// The JSON key is part of the frozen /api/yggdrasil contract, so test (e) pins
// that only the VALUES changed, never the field name.

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// domainsFor builds the list for a fixed configuration and request host, so a
// test reads the exact slice the metadata handler would serialise.
func domainsFor(t *testing.T, configured []string, homepage, reqHost string) []string {
	t.Helper()
	api, _ := newTestAPI(t)
	api.cfg.SkinDomains = configured
	api.cfg.Homepage = homepage
	api.cfg.ExtraSkinDomains = nil // isolate the static formula from union state
	r := httptest.NewRequest("GET", "/api/yggdrasil/", nil)
	r.Host = reqHost
	return api.skinDomains(r)
}

func countOf(list []string, want string) int {
	n := 0
	for _, d := range list {
		if d == want {
			n++
		}
	}
	return n
}

// (a) a port-bearing entry produces its portless twin, alongside the original.
func TestSkinDomainsPortEntryGainsPortlessTwin(t *testing.T) {
	got := domainsFor(t, []string{"a.invalid:8443"}, "", "h.example")
	if countOf(got, "a.invalid:8443") != 1 {
		t.Fatalf("the port-bearing entry must survive untouched: %v", got)
	}
	if countOf(got, "a.invalid") != 1 {
		t.Fatalf("a port-bearing entry must ALSO publish its portless twin exactly once: %v", got)
	}
}

// (b) a host without a port is published exactly once — no phantom twin.
func TestSkinDomainsPortlessHostNotDuplicated(t *testing.T) {
	got := domainsFor(t, []string{"a.invalid"}, "", "h.example")
	if countOf(got, "a.invalid") != 1 {
		t.Fatalf("a portless host must appear exactly once, got %v", got)
	}
}

// (c) when both the port-bearing and the bare host are present, each appears
// exactly once — the twin collapses onto the entry that was already there.
func TestSkinDomainsBothFormsEachOnce(t *testing.T) {
	got := domainsFor(t, []string{"site.invalid:8443", "site.invalid"}, "", "h.example")
	if countOf(got, "site.invalid:8443") != 1 {
		t.Fatalf("port-bearing entry must appear exactly once: %v", got)
	}
	if countOf(got, "site.invalid") != 1 {
		t.Fatalf("bare host must appear exactly once (twin must not double it): %v", got)
	}
}

// Order stability / additive guarantee: the pre-existing entries keep their
// exact positions and only the twin is appended after them.
func TestSkinDomainsExistingEntriesKeepPosition(t *testing.T) {
	got := domainsFor(t, []string{"p.invalid:8443", "q.invalid"}, "", "h.example")
	// p.invalid:8443 and q.invalid come from config; h.example from the request.
	if got[0] != "p.invalid:8443" || got[1] != "q.invalid" || got[2] != "h.example" {
		t.Fatalf("existing entries must be unchanged and in order, got %v", got)
	}
	if got[len(got)-1] != "p.invalid" {
		t.Fatalf("the portless twin must be appended after the untouched entries, got %v", got)
	}
}

// (d) IPv6 hosts and empty / ":" malformed values neither crash the emitter nor
// introduce an empty-string entry, and an IPv6 host:port gains a bracketed
// portless twin rather than a bracket-stripped or empty one.
func TestSkinDomainsIPv6AndMalformed(t *testing.T) {
	got := domainsFor(t, []string{"[::1]:8443", "[fe80::1]", "", ":", "ok.invalid"}, "", "h.example")
	for _, d := range got {
		if d == "" {
			t.Fatalf("a malformed value must never yield an empty-string entry: %v", got)
		}
	}
	// IPv6 with a port keeps its original and gains the bracketed portless form.
	if countOf(got, "[::1]:8443") != 1 {
		t.Fatalf("IPv6 port-bearing entry must survive: %v", got)
	}
	if countOf(got, "[::1]") != 1 {
		t.Fatalf("IPv6 port-bearing entry must gain a bracketed portless twin: %v", got)
	}
	// An IPv6 literal with no port and a lone ":" produce no bogus extra entry;
	// a stray colon must not invent an empty host either.
	if countOf(got, "[fe80::1]") != 1 {
		t.Fatalf("portless IPv6 entry must appear exactly once: %v", got)
	}
	// ":" is passed through verbatim (existing behaviour) but must not spawn a
	// "" twin — covered by the no-empty-entry loop above.
}

// (e) the response field is still the same JSON key the frozen contract uses.
func TestSkinDomainsKeyUnchangedInResponse(t *testing.T) {
	api, _ := newTestAPI(t)
	api.cfg.SkinDomains = []string{"a.invalid:8443"}
	api.cfg.Homepage = ""
	api.cfg.ExtraSkinDomains = nil
	rec := do(t, api.Handler(), "GET", "/api/yggdrasil/", "", nil)
	body := rec.Body.String()
	if !strings.Contains(body, `"skinDomains"`) {
		t.Fatalf("the frozen metadata key skinDomains must still be emitted, got %s", body)
	}
	var doc struct {
		SkinDomains []string `json:"skinDomains"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if countOf(doc.SkinDomains, "a.invalid") != 1 || countOf(doc.SkinDomains, "a.invalid:8443") != 1 {
		t.Fatalf("twin must reach the serialised payload unchanged in key: %v", doc.SkinDomains)
	}
}
