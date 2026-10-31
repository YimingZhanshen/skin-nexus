package appearance

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"skin-nexus/server/internal/theme"
)

type fakeOpts struct{ kv map[string]string }

func (f *fakeOpts) GetOption(_ context.Context, k string) (string, bool, error) {
	v, ok := f.kv[k]
	return v, ok, nil
}
func (f *fakeOpts) SetOption(_ context.Context, k, v string) error {
	if f.kv == nil {
		f.kv = map[string]string{}
	}
	f.kv[k] = v
	return nil
}

func testRegistry(t *testing.T) *theme.Registry {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "dark")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	man := `{"id":"dark","name":"Dark","version":"1.0.0","inject":{"css":["style.css"],"js":["fx.js"]}}`
	if err := os.WriteFile(filepath.Join(dir, "theme.json"), []byte(man), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := theme.NewRegistry(root, "0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

const tpl = `<html><head><!--skin-theme--><!--skin-custom-head--></head><body><!--skin-custom-body--></body></html>`

func TestRenderLayers(t *testing.T) {
	e := NewEngine(&fakeOpts{kv: map[string]string{
		"active_theme": "dark",
		"custom_css":   ".card{border-radius:20px}",
		"custom_js":    "console.log('hi')",
	}}, testRegistry(t))
	html := e.Render(context.Background(), tpl)
	for _, want := range []string{
		`/theme-assets/dark/style.css`,
		`/theme-assets/dark/fx.js`,
		`<style id="skin-custom-css">`,
		`.card{border-radius:20px}`,
		`<script id="skin-custom-js">`,
		`console.log('hi')`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %q in %q", want, html)
		}
	}
	// order: theme css must come before custom css (custom wins in cascade)
	if strings.Index(html, "/theme-assets/dark/style.css") > strings.Index(html, "skin-custom-css") {
		t.Fatal("theme must inject before custom css")
	}
}

func TestSaveInvalidThemeClears(t *testing.T) {
	e := NewEngine(&fakeOpts{}, testRegistry(t))
	if err := e.Save(context.Background(), Settings{ActiveTheme: "nope", CustomCSS: "x"}); err != nil {
		t.Fatal(err)
	}
	s := e.Settings(context.Background())
	if s.ActiveTheme != "" {
		t.Fatalf("invalid theme kept: %q", s.ActiveTheme)
	}
	if s.CustomCSS != "x" {
		t.Fatalf("custom css lost: %q", s.CustomCSS)
	}
}

func TestNoThemeNoMarkersLeft(t *testing.T) {
	e := NewEngine(&fakeOpts{}, testRegistry(t))
	html := e.Render(context.Background(), tpl)
	if strings.Contains(html, "skin-theme-->") || strings.Contains(html, "skin-custom-") {
		t.Fatalf("markers left: %q", html)
	}
}

func TestFooterDataAndHTML(t *testing.T) {
	e := NewEngine(&fakeOpts{kv: map[string]string{
		"footer_text":   "© {year} {site_name} · 浙ICP备123号\n第二行",
		"footer_links":  `[{"label":"友链","url":"https://example.com/a?b=1&c=2"},{"label":"备案","url":"https://beian.miit.gov.cn/"}]`,
		"footer_credit": "1",
	}}, testRegistry(t))
	f := e.FooterData(context.Background(), "示例站", "https://skin.example.com/", 2026)
	if f.Text != "© 2026 示例站 · 浙ICP备123号\n第二行" {
		t.Fatalf("footer text = %q", f.Text)
	}
	if len(f.Links) != 2 || f.Links[0].Label != "友链" {
		t.Fatalf("footer links = %#v", f.Links)
	}
	if !f.Credit || f.Year != 2026 || f.SiteName != "示例站" {
		t.Fatalf("footer metadata = %#v", f)
	}
	html := string(f.HTML())
	for _, want := range []string{
		`href="https://example.com/a?b=1&amp;c=2"`,
		`rel="noopener noreferrer"`,
		`浙ICP备123号<br>第二行`,
		`Powered by`,
		`© 2026 示例站`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("footer html missing %q: %s", want, html)
		}
	}
}

func TestFooterDefaultsToCredit(t *testing.T) {
	e := NewEngine(&fakeOpts{}, testRegistry(t))
	f := e.FooterData(context.Background(), "示例站", "https://skin.example.com", 2026)
	if !f.Credit || len(f.Links) != 0 {
		t.Fatalf("default footer = %#v", f)
	}
	if !strings.Contains(string(f.HTML()), "Powered by") {
		t.Fatalf("default footer must keep credit: %s", f.HTML())
	}
}

func TestFooterEscapesAndDropsUnsafeLinks(t *testing.T) {
	f := Footer{
		Text:     `<script>alert(1)</script>`,
		Links:    []FooterLink{{Label: `<b>x</b>`, URL: "javascript:alert(1)"}, {Label: "ok", URL: "https://example.com"}},
		Credit:   false,
		SiteName: `<img src=x>`,
		Year:     2026,
	}
	html := string(f.HTML())
	if strings.Contains(html, "<script>") || strings.Contains(html, "javascript:") {
		t.Fatalf("unsafe footer html: %s", html)
	}
	if !strings.Contains(html, "&lt;script&gt;") || !strings.Contains(html, `href="https://example.com"`) {
		t.Fatalf("escaped footer html missing content: %s", html)
	}
}

func TestFooterSaveValidation(t *testing.T) {
	e := NewEngine(&fakeOpts{}, testRegistry(t))
	cases := []Settings{
		{FooterText: strings.Repeat("字", maxFooterTextRune+1)},
		{FooterLinks: []FooterLink{{Label: "x", URL: "javascript:alert(1)"}}},
		{FooterLinks: []FooterLink{{Label: "", URL: "https://example.com"}}},
	}
	for i, s := range cases {
		if err := e.Save(context.Background(), s); err == nil {
			t.Fatalf("case %d: expected validation error", i)
		}
	}
	if err := e.Save(context.Background(), Settings{
		FooterText:   "© {year} {site_name}",
		FooterLinks:  []FooterLink{{Label: "友链", URL: "https://example.com"}},
		FooterCredit: false,
	}); err != nil {
		t.Fatal(err)
	}
	got := e.Settings(context.Background())
	if got.FooterText != "© {year} {site_name}" || len(got.FooterLinks) != 1 || got.FooterCredit {
		t.Fatalf("saved footer = %#v", got)
	}
}
