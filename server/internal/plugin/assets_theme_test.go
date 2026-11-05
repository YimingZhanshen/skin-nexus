package plugin

// A plugin UI page paints with whatever its own document declares. Every
// first-party plugin ships `:root{--plugin-bg:#101825;…}` inline, so the very
// first frame is that palette; the site's theme can only arrive afterwards,
// over the JS bridge (frame posts `ready` → host reads computed styles →
// `init` → plugin sets the variables). That round trip is the flash the operator
// sees: dark defaults, then a jump to the theme a moment later.
//
// The fix is not to win the race, it is to take the theme out of the race: the
// host serves this HTML, so the active theme's root tokens and the mapping onto
// the plugin contract can travel in <head> as render-blocking CSS, exactly the
// way the SPA shell and the SSR pages already get them. Tokens only — linking
// the pack's real stylesheet also applies its bare element rules to the plugin's
// own markup, which is the regression theme.TokenStyle exists to prevent.
// The bridge stays — locale, user and `dark` still come through `init`.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// pluginThemeMap maps the site theme tokens onto the plugin variable names. The
// fallbacks are the plugin contract's defaults and must equal what the host
// reads in PluginFrame.vue — TestPluginThemeMapMatchesTheHostBridge is the seam
// that keeps the two from drifting apart.
func TestAssetHandlerInjectsThemeBeforeFirstPaint(t *testing.T) {
	root := t.TempDir()
	plug := filepath.Join(root, "demo")
	if err := os.MkdirAll(filepath.Join(plug, "ui"), 0o755); err != nil {
		t.Fatal(err)
	}
	page := `<!doctype html><html><head><style>
:root { --plugin-bg: #101825; --plugin-accent: #b0453a; }
</style></head><body><p>hi</p></body></html>`
	if err := os.WriteFile(filepath.Join(plug, "ui", "page.html"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &Manager{instances: map[string]*instance{
		"demo": {enabled: true, info: Info{Manifest: Manifest{Name: "demo", UI: &UISpec{Assets: "ui"}}, Dir: plug}},
	}}
	m.order = []string{"demo"}

	theme := func(context.Context) string {
		// What theme.TokenStyle produces: root tokens only. A linked
		// stylesheet here used to drag the theme's bare element rules
		// (body{background-image}, h1,h2{font-family}, input{font-size}) into
		// the plugin document — see internal/theme/tokens.go.
		return `<style id="skin-theme-tokens">:root{--bg:#f7f5f0;--accent:#601986;color-scheme:light}</style>` + "\n"
	}
	h := m.AssetHandler(theme)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/plugin-assets/demo/page.html", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()

	// 1) the theme arrives as CSS in <head>, not as a postMessage payload
	if !strings.Contains(body, `--bg:#f7f5f0`) {
		t.Fatalf("theme tokens not injected:\n%s", body)
	}
	if !strings.Contains(body, `--plugin-bg:var(--bg,`) {
		t.Fatalf("token mapping not injected:\n%s", body)
	}
	// 1b) tokens before the mapping, or var(--bg) resolves to its fallback
	// while the value that should have supplied it lands one rule later.
	tokens := strings.Index(body, "--bg:#f7f5f0")
	mapping := strings.Index(body, "--plugin-bg:var(--bg,")
	if tokens > mapping {
		t.Errorf("mapping injected before the token block (%d > %d)", mapping, tokens)
	}
	// 2) order is the whole fix: the injected block must come AFTER the plugin's
	// own :root, or its literal defaults keep winning at equal specificity.
	own := strings.Index(body, "--plugin-bg: #101825")
	injected := strings.Index(body, "--plugin-bg:var(--bg,")
	if own < 0 || injected < 0 {
		t.Fatalf("missing one of the two declarations (own=%d injected=%d):\n%s", own, injected, body)
	}
	if injected < own {
		t.Error("injected before the plugin's own defaults: the flash would survive")
	}
	if i := strings.Index(body, "</head>"); injected > i {
		t.Errorf("injection landed outside <head> (%d > %d)", injected, i)
	}
	// 3) the body the plugin authored is not touched
	if !strings.Contains(body, "<p>hi</p>") {
		t.Error("plugin body altered")
	}
	// 4) HTML that is revalidated each load stays revalidated: an immutable cache
	// entry would pin the old theme across a switch.
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "must-revalidate") {
		t.Errorf("Cache-Control = %q", cc)
	}
	if sb := rec.Header().Get("Sandbox"); sb != "allow-scripts allow-forms" {
		t.Errorf("Sandbox = %q, the framed-document flags must survive the rewrite", sb)
	}
}

// No theme active (or a host that passes no injector) must serve the plugin's
// file byte-for-byte: the defaults are then correct, and inventing a mapping
// would be a style change nobody asked for.
func TestAssetHandlerWithoutAThemeServesTheFileAsIs(t *testing.T) {
	root := t.TempDir()
	plug := filepath.Join(root, "demo")
	if err := os.MkdirAll(filepath.Join(plug, "ui"), 0o755); err != nil {
		t.Fatal(err)
	}
	const page = `<!doctype html><html><head></head><body><p>hi</p></body></html>`
	if err := os.WriteFile(filepath.Join(plug, "ui", "page.html"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &Manager{instances: map[string]*instance{
		"demo": {enabled: true, info: Info{Manifest: Manifest{Name: "demo", UI: &UISpec{Assets: "ui"}}, Dir: plug}},
	}}
	m.order = []string{"demo"}

	for _, tc := range []struct {
		name string
		fn   func(context.Context) string
	}{
		{"nil injector", nil},
		{"no active theme", func(context.Context) string { return "" }},
	} {
		rec := httptest.NewRecorder()
		m.AssetHandler(tc.fn).ServeHTTP(rec, httptest.NewRequest("GET", "/plugin-assets/demo/page.html", nil))
		if got := rec.Body.String(); got != page {
			t.Errorf("%s: body was rewritten:\n%q", tc.name, got)
		}
	}
}

// CSS and JS assets must not be scanned or rewritten — only documents carry the
// theme, and rewriting a stylesheet would leak the host's palette into a file
// the plugin ships and caches.
func TestAssetHandlerLeavesNonHTMLAlone(t *testing.T) {
	root := t.TempDir()
	plug := filepath.Join(root, "demo")
	if err := os.MkdirAll(filepath.Join(plug, "ui"), 0o755); err != nil {
		t.Fatal(err)
	}
	const css = `:root{--plugin-bg:#000}`
	if err := os.WriteFile(filepath.Join(plug, "ui", "s.css"), []byte(css), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &Manager{instances: map[string]*instance{
		"demo": {enabled: true, info: Info{Manifest: Manifest{Name: "demo", UI: &UISpec{Assets: "ui"}}, Dir: plug}},
	}}
	m.order = []string{"demo"}
	rec := httptest.NewRecorder()
	m.AssetHandler(func(context.Context) string { return "<link href=/theme-assets/x/style.css>" }).
		ServeHTTP(rec, httptest.NewRequest("GET", "/plugin-assets/demo/s.css", nil))
	if rec.Body.String() != css {
		t.Errorf("stylesheet was rewritten: %q", rec.Body.String())
	}
}

// The host reads the same nine tokens with its own fallbacks (PluginFrame.vue's
// themePayload), because the bridge still has to tell a plugin `dark` and a
// locale. Two lists that must agree is a drift hazard, so this compares them by
// reading both sources: same plugin variable, same theme variable, same default.
func TestPluginThemeMapMatchesTheHostBridge(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "src", "components", "plugin", "PluginFrame.vue"))
	if err != nil {
		t.Fatalf("read PluginFrame.vue: %v", err)
	}
	vue := map[string][2]string{}
	for _, m := range regexp.MustCompile(`([a-zA-Z]+):\s*cssVar\('--([a-zA-Z-]+)',\s*'([^']*)'\)`).FindAllStringSubmatch(string(src), -1) {
		vue[m[1]] = [2]string{m[2], m[3]}
	}
	// `bg` is assigned before the object literal: `bg := cssVar('--bg', '#101825')`.
	for _, m := range regexp.MustCompile(`([a-zA-Z]+)\s*(?::=|=)\s*cssVar\('--([a-zA-Z-]+)',\s*'([^']*)'\)`).FindAllStringSubmatch(string(src), -1) {
		vue[m[1]] = [2]string{m[2], m[3]}
	}
	if len(vue) != 9 {
		t.Fatalf("the host bridge exposes %d token(s), expected 9 — did themePayload change shape?", len(vue))
	}
	goMap := map[string][2]string{}
	for _, m := range regexp.MustCompile(`--plugin-([a-z]+):var\(--([a-zA-Z-]+),([^;]*)\);`).FindAllStringSubmatch(pluginThemeMap, -1) {
		goMap[m[1]] = [2]string{m[2], m[3]}
	}
	if len(goMap) != len(vue) {
		t.Fatalf("the injected map has %d entries, the bridge has %d", len(goMap), len(vue))
	}
	for key, want := range vue {
		got, ok := goMap[key]
		if !ok {
			t.Errorf("--plugin-%s is themed by the bridge but not by the injected map", key)
			continue
		}
		if got != want {
			t.Errorf("--plugin-%s: injected var(%s,%s), bridge cssVar(--%s,'%s') — the two must agree",
				key, got[0], got[1], want[0], want[1])
		}
	}
}
