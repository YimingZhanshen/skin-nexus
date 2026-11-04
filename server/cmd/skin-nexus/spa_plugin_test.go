package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestIsKnownSPAPathPluginUI(t *testing.T) {
	ui := func(p string) bool { return p == "ext/demo/page" }
	if !isKnownSPAPath("ext/demo/page", ui) {
		t.Fatal("declared plugin page should be SPA-known")
	}
	if isKnownSPAPath("ext/demo/missing", ui) {
		t.Fatal("undeclared plugin page must 404")
	}
	if !isKnownSPAPath("skinlib", nil) || isKnownSPAPath("nope", nil) {
		t.Fatal("built-in whitelist changed")
	}
}

// The paths we put in outgoing mail MUST be shell-known: an unlisted path 404s
// on the server before the SPA can load, which turns every emailed link into a
// dead end that no browser test of the API can see. Both of these were caught
// only by loading the real URL — hence the assertion.
func TestIsKnownSPAPathCoversEmailedLinks(t *testing.T) {
	for _, p := range []string{"reset-password", "email-verify"} {
		if !isKnownSPAPath(p, nil) {
			t.Fatalf("mailed link path %q is not SPA-known — the link in the mail would 404", p)
		}
	}
}

// The SPA and this whitelist are two files that must agree, and nothing but this
// test makes them: a view reachable from the nav but absent from legacyRoutes
// loses its address on refresh (the shell loads and the app falls back to the
// dashboard), and a route added only to the app 404s before the shell is ever
// served. Both directions are asserted by reading the frontend source, the same
// way the emailed-links test reads what the mailer actually sends.
func TestSPARoutesAgreeWithAppMaps(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "src", "App.vue"))
	if err != nil {
		t.Fatalf("read App.vue: %v", err)
	}
	text := string(src)
	routes := objectField(text, "legacyRoutes", 0) // the URLs the app can resolve
	urls := objectField(text, "viewURLs", 1)       // the URLs the app writes
	if len(routes) < 5 || len(urls) < 5 {
		t.Fatalf("parsed %d legacyRoutes / %d viewURLs entries — the extractor is broken", len(routes), len(urls))
	}
	for _, u := range routes {
		if !isKnownSPAPath(strings.TrimPrefix(u, "/"), nil) {
			t.Errorf("legacyRoutes resolves %q but the shell whitelist 404s it — the deep link dies on refresh", u)
		}
	}
	have := map[string]bool{}
	for _, u := range routes {
		have[u] = true
	}
	for _, u := range urls {
		if !have[u] {
			t.Errorf("view URL %q has no legacyRoutes entry — F5 on that address shows a page the app cannot resolve", u)
		}
	}
}

// The agreement above is only as good as the extractor: it reads a .vue file
// with a regex, so a style change in App.vue would silently shrink the checked
// set to nothing. Pinned here against an inline fixture rather than the real
// file, because the property being asserted is "which spellings are seen".
func TestSPARouteExtractorSeesBothQuoteStyles(t *testing.T) {
	src := `const legacyRoutes = {
  '/skinlib': 'skinlib',
  '/help': 'help',
}
const viewURLs = {
  skinlib: '/skinlib',
  help: "/help",
  detail: '/texture-detail',
}
`
	routes := objectField(src, "legacyRoutes", 0)
	urls := objectField(src, "viewURLs", 1)
	if len(routes) != 2 {
		t.Errorf("legacyRoutes keys = %v, want both quoted keys", routes)
	}
	if len(urls) != 3 {
		t.Fatalf("viewURLs values = %v, want three (bare key, single-quoted and double-quoted value)", urls)
	}
	want := map[string]bool{"/skinlib": true, "/help": true, "/texture-detail": true}
	for _, u := range urls {
		if !want[u] {
			t.Errorf("unexpected view URL %q", u)
		}
		delete(want, u)
	}
	if len(want) != 0 {
		t.Errorf("extractor missed %v — a route the gate would fail to compare", want)
	}
	if got := objectField(src, "noSuchMap", 0); got != nil {
		t.Errorf("a missing map returned %v, want nil (the caller's <5 guard depends on that)", got)
	}
}

// literal: index 0 gives the keys, index 1 the values. The path walks out of
// server/cmd/skin-nexus to the repository root — the test runs from its own package dir.
// objectField pulls one side out of a `const NAME = { 'url': 'view', … }`
// literal: index 0 gives the keys, index 1 the values. The path walks out of
// server/cmd/skin-nexus to the repository root — the test runs from its own package dir.
func objectField(src, name string, side int) []string {
	start := strings.Index(src, "const "+name+" = {")
	if start < 0 {
		return nil
	}
	end := strings.Index(src[start:], "\n}")
	if end < 0 {
		return nil
	}
	// Keys are quoted in legacyRoutes and bare in viewURLs, so both are allowed —
	// and so is either quote character on the value. Requiring a single quote
	// meant a view written as `x: "/y"` was invisible to this gate, which is the
	// one failure it exists to catch.
	pair := regexp.MustCompile(`'?([A-Za-z0-9_\-/]+)'?\s*:\s*['"]([^'"]+)['"]`)
	var out []string
	for _, m := range pair.FindAllStringSubmatch(src[start:start+end], -1) {
		out = append(out, m[side+1])
	}
	return out
}
