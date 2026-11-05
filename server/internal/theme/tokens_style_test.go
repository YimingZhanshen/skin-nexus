package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The bug this guards is what a full stylesheet link did to plugin pages:
// wzbc-violet repaints the host <body> with three radial highlights and
// retypes h1/h2, and those bare element rules landed inside the sandboxed
// plugin document too (measured: body background-image became a gradient,
// h2 font-weight dropped 700 → 400, input font-size 14px → 13px). A plugin
// page must inherit the palette and nothing else, so the theme travels as
// extracted root tokens — never as a <link> to the pack's own CSS.
func TestTokenStyleCarriesTokensWithoutLinkingHostRules(t *testing.T) {
	dir := t.TempDir()
	pack := filepath.Join(dir, "violet")
	if err := os.MkdirAll(pack, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(pack, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("theme.json", `{"id":"violet","name":"Violet","version":"1.2.0","inject":{"css":["style.css"],"js":["theme.js"]}}`)
	write("style.css", `
:root { --bg: #f7f5f0; --panel: #fff; color-scheme: light; }
body { background-image: radial-gradient(920px 480px at 84% -8%, rgba(178,106,222,.13), transparent 64%); }
h1, h2 { font-family: "MC Pixel CJK", Consolas; }
@media (prefers-color-scheme: dark) { :root { --bg: #14101c; color-scheme: dark; } }
`)

	reg, err := NewRegistry(dir, "0.5.0")
	if err != nil {
		t.Fatal(err)
	}
	th, err := reg.Get("violet")
	if err != nil {
		t.Fatal(err)
	}

	got := th.TokenStyle()
	for _, want := range []string{`<style id="skin-theme-tokens">`, "--bg: #f7f5f0", "--panel: #fff", "color-scheme: light", "@media (prefers-color-scheme: dark)"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, bad := range []string{"/theme-assets/", "<link", "radial-gradient", "font-family", "theme.js", "<script"} {
		if strings.Contains(got, bad) {
			t.Errorf("leaked %q into the plugin-facing fragment:\n%s", bad, got)
		}
	}
	if !strings.HasSuffix(got, "</style>\n") {
		t.Errorf("fragment must be one closed <style> block, got:\n%s", got)
	}
}

// A value containing "</style>" would close the element early and let the rest
// of the theme file out as markup. Such a declaration is dropped rather than
// escaped, because there is no reason for a token value to contain it.
func TestTokenStyleRefusesMarkupBreakingValues(t *testing.T) {
	dir := t.TempDir()
	pack := filepath.Join(dir, "evil")
	if err := os.MkdirAll(pack, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack, "theme.json"),
		[]byte(`{"id":"evil","name":"Evil","version":"1.0.0","inject":{"css":["style.css"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack, "style.css"),
		[]byte(":root { --bg: #eee; --x: \"</style><script>alert(1)</script>\"; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := NewRegistry(dir, "0.5.0")
	if err != nil {
		t.Fatal(err)
	}
	th, err := reg.Get("evil")
	if err != nil {
		t.Fatal(err)
	}
	got := th.TokenStyle()
	if strings.Contains(got, "<script") || strings.Count(got, "</style>") != 1 {
		t.Errorf("markup escaped the style element:\n%s", got)
	}
	if !strings.Contains(got, "--bg: #eee") {
		t.Errorf("the safe token must survive:\n%s", got)
	}
}

// Themes that put tokens somewhere other than the root selector get no
// fragment at all, which leaves the plugin's own defaults plus the JS bridge
// — the pre-injection behaviour, not a broken half-theme.
func TestTokenStyleEmptyWhenThemeDeclaresNoRootTokens(t *testing.T) {
	dir := t.TempDir()
	pack := filepath.Join(dir, "plain")
	if err := os.MkdirAll(pack, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack, "theme.json"),
		[]byte(`{"id":"plain","name":"Plain","version":"1.0.0","inject":{"css":["style.css"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack, "style.css"),
		[]byte("body { --bg: #123456; color: red; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := NewRegistry(dir, "0.5.0")
	if err != nil {
		t.Fatal(err)
	}
	th, err := reg.Get("plain")
	if err != nil {
		t.Fatal(err)
	}
	if got := th.TokenStyle(); got != "" {
		t.Errorf("want no fragment for a theme with no root tokens, got:\n%s", got)
	}
}
