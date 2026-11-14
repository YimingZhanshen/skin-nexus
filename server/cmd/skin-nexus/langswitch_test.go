package main

// langswitch_test.go — D7 / S2（2026-11-07 夜）.
//
// hreflang has advertised the two addressable forms of every SSR page since
// U11, and nothing on the page pointed at them: a reader who wanted English
// had to know the query parameter exists, and a crawler had no internal link
// to follow. The second half is persistence — `?hl=` did not write the
// `skin_lang` cookie, so switching language on a crawlable page and then
// walking into the SPA snapped back to the negotiated language.
//
// Both halves are pinned here. The detail page's switch is proven over real
// HTTP against the local rig (it needs a *store.Mysql, see the note in
// texturepage_test.go); what this file proves is the shared renderer, the
// cookie contract, and the landing/list surfaces that need no DB.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"skin-nexus/server/internal/appearance"
	"skin-nexus/server/internal/seo"
	"skin-nexus/server/internal/store"
	"skin-nexus/server/internal/theme"
	"skin-nexus/server/internal/webi18n"
)

func TestLangHrefReplacesHlAndKeepsTheRest(t *testing.T) {
	cases := []struct {
		path, raw, lang string
		want            []string
	}{
		{"/", "", "en", []string{"/?hl=en"}},
		{"/", "", "zh-CN", []string{"/?hl=zh-CN"}},
		{"/skinlib/42", "", "en", []string{"/skinlib/42?hl=en"}},
		// The parameter already present must be replaced, never doubled: two
		// hl= values in one href contradicts the hreflang block it mirrors.
		{"/skinlib/42", "hl=en", "zh-CN", []string{"/skinlib/42?hl=zh-CN"}},
		// Other filters are part of the same page; dropping them would send the
		// reader to a different listing while claiming it is the alternate.
		{"/skinlib", "q=cats&page=2&hl=zh-CN", "en", []string{"q=cats", "page=2", "hl=en"}},
		// An escaped value must stay escaped — decoding it here would turn an
		// encoded & into a new parameter.
		{"/skinlib", "q=a%26b", "en", []string{"q=a%26b", "hl=en"}},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, c.path+func() string {
			if c.raw == "" {
				return ""
			}
			return "?" + c.raw
		}(), nil)
		got := langHrefFor(req, c.lang)
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("langHrefFor(%q,%q,%q) = %q, want substring %q", c.path, c.raw, c.lang, got, w)
			}
		}
		if n := strings.Count(got, "hl="); n != 1 {
			t.Errorf("langHrefFor(%q,%q,%q) = %q carries %d hl= values, want exactly 1", c.path, c.raw, c.lang, got, n)
		}
	}
}

func TestLangSwitchIsCrawlableAndBilingual(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/skinlib/42?hl=en", nil)
	got := string(langSwitchFor(req, webi18n.En))
	for _, w := range []string{
		`class="langsw"`,
		`rel="alternate"`,
		`hreflang="zh-CN"`,
		`hreflang="en"`,
		`href="/skinlib/42?hl=zh-CN"`,
		`aria-current="true"`,
		"中文", "English",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("switch markup missing %q:\n%s", w, got)
		}
	}
	// A real anchor, not a JS handler: AI crawlers never execute scripts.
	if strings.Contains(got, "onclick") || strings.Contains(got, "javascript:") {
		t.Errorf("switch must be a plain link, found script coupling:\n%s", got)
	}
	// Labels come from the catalog — a bare key rendered on the page is the
	// failure mode U13 was written to catch.
	for _, leak := range []string{"ui.langZh", "ui.langEn", "ui.langSwitch"} {
		if strings.Contains(got, leak) {
			t.Errorf("bare catalog key %s leaked into markup:\n%s", leak, got)
		}
	}
	// Exactly one link is the current page; aria-current on both would tell a
	// screen reader nothing.
	if n := strings.Count(got, `aria-current="true"`); n != 1 {
		t.Errorf("want exactly one aria-current=\"true\", got %d:\n%s", n, got)
	}
	// The nav needs a name, or a reader hears "link link".
	if !strings.Contains(got, "aria-label=") {
		t.Errorf("language switch has no accessible name:\n%s", got)
	}
}

func TestPersistLangCookieContract(t *testing.T) {
	// Attribute-for-attribute mirror of web/src/i18n.js:17-20: no HttpOnly (the
	// SPA must keep reading and writing the same cookie), SameSite=Lax (a
	// launcher opening /device.html is a top-level navigation), Secure only
	// over https, one-year lifetime to match the localStorage entry.

	t.Run("explicit hl over https persists", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/skinlib?hl=en", nil)
		req.Header.Set("X-Forwarded-Proto", "https")
		rec := httptest.NewRecorder()
		persistLang(rec, req)
		c := rec.Header().Get("Set-Cookie")
		for _, w := range []string{"skin_lang=en", "Path=/", "Max-Age=31536000", "SameSite=Lax", "Secure"} {
			if !strings.Contains(c, w) {
				t.Errorf("Set-Cookie %q missing %q", c, w)
			}
		}
		if strings.Contains(c, "HttpOnly") {
			t.Errorf("the SPA has to read this cookie; HttpOnly would break it: %q", c)
		}
	})

	t.Run("http does not claim Secure", func(t *testing.T) {
		rec := httptest.NewRecorder()
		persistLang(rec, httptest.NewRequest(http.MethodGet, "/?hl=en", nil))
		if c := rec.Header().Get("Set-Cookie"); strings.Contains(c, "Secure") {
			t.Errorf("a Secure cookie over http is dropped by the browser: %q", c)
		}
	})

	t.Run("negotiated language writes nothing", func(t *testing.T) {
		// Only an explicit ?hl= is a choice. Stamping a cookie onto every
		// Accept-Language hit would make HTML cache keys per-reader for no gain.
		req := httptest.NewRequest(http.MethodGet, "/skinlib", nil)
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")
		rec := httptest.NewRecorder()
		persistLang(rec, req)
		if c := rec.Header().Get("Set-Cookie"); c != "" {
			t.Errorf("no explicit language, yet a cookie was set: %q", c)
		}
	})

	t.Run("unknown language is not persisted", func(t *testing.T) {
		rec := httptest.NewRecorder()
		persistLang(rec, httptest.NewRequest(http.MethodGet, "/?hl=fr", nil))
		if c := rec.Header().Get("Set-Cookie"); strings.Contains(c, "skin_lang") {
			t.Errorf("a language we do not ship must not pin anything: %q", c)
		}
	})

	t.Run("already matching cookie is not rewritten", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/skinlib/1?hl=en", nil)
		req.AddCookie(&http.Cookie{Name: webi18n.CookieName, Value: "en"})
		rec := httptest.NewRecorder()
		persistLang(rec, req)
		if c := rec.Header().Get("Set-Cookie"); c != "" {
			t.Errorf("cookie already says en, re-setting it is pure cache noise: %q", c)
		}
	})
}

// spaFixture builds the SPA shell handler the way main_test.go does, so the
// assertions below run through the real route assembly, not around it.
func spaFixture(t *testing.T, skinlib skinlibLister, loggedIn func(*http.Request) bool) http.Handler {
	t.Helper()
	dir := t.TempDir()
	tpl := `<!DOCTYPE html><html lang="zh-CN"><head><!--skin-seo--><!--skin-theme--><!--skin-custom-head--></head>` +
		`<body><div id="app"><!--skin-app-start--><p>b</p><!--skin-app-end--></div></body></html>`
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(tpl), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := theme.NewRegistry(t.TempDir(), "0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	opts := &fakeOpts{kv: map[string]string{"seo_title": "Rig"}}
	return spaHandler(dir,
		seo.NewEngine(opts, seo.Defaults{SiteName: "Rig", BaseURL: "https://skin.example.com"}),
		appearance.NewEngine(opts, reg),
		"Rig",
		func(r *http.Request) landingData {
			return landingData{Lang: webi18n.ResolveLang(r), SiteName: "Rig", Users: 1, Players: 2, Textures: 3}
		}, loggedIn, skinlib, nil)
}

func TestLandingShipsSwitchAndPersistsExplicitHl(t *testing.T) {
	h := spaFixture(t, nil, nil)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?hl=en", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `class="langsw"`) {
		t.Fatalf("landing ships no visible language switch:\n%s", body)
	}
	if !strings.Contains(body, `hreflang="zh-CN"`) || !strings.Contains(body, `href="/?hl=zh-CN"`) {
		t.Fatalf("landing switch must link the other language on the same path:\n%s", body)
	}
	if c := rec.Header().Get("Set-Cookie"); !strings.Contains(c, "skin_lang=en") {
		t.Fatalf("?hl=en on the landing page was not persisted (cookie: %q)", c)
	}

	// Without an explicit choice nothing is written, and the page still offers
	// both languages.
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/", nil))
	if c := rec2.Header().Get("Set-Cookie"); strings.Contains(c, "skin_lang") {
		t.Fatalf("a negotiated visit wrote a language cookie: %q", c)
	}
	if !strings.Contains(rec2.Body.String(), `class="langsw"`) {
		t.Fatalf("default-language landing ships no switch:\n%s", rec2.Body.String())
	}
}

func TestSkinlibPrerenderShipsSwitchWithFiltersKept(t *testing.T) {
	st := &fakeSkinlib{rows: []store.SkinlibRow{
		{TID: 7, Name: "夜行者", Nickname: "steve", Type: "skin"},
		{TID: 8, Name: " cape", Nickname: "alex", Type: "cape"},
		{TID: 9, Name: "third", Nickname: "steve", Type: "skin"},
	}, total: 3}
	req := httptest.NewRequest(http.MethodGet, "/skinlib?q=cats&page=2&hl=en", nil)
	block, _, ok := prerenderSkinlib(req.Context(), req, st, "Rig")
	if !ok {
		t.Fatal("prerender declined a request that should ship content")
	}
	for _, w := range []string{`class="langsw"`, `hreflang="zh-CN"`, "q=cats", "page=2", "hl=zh-CN"} {
		if !strings.Contains(block, w) {
			t.Errorf("prerendered list switch missing %q:\n%s", w, block)
		}
	}
	// Same page, other language: the pager is pagination, this is locale, and
	// the two must not be confused for one control.
	if !strings.Contains(block, `class="ssr-pager"`) {
		t.Errorf("pager disappeared: %s", block)
	}
}

func Test404ShipsNoSwitch(t *testing.T) {
	// A switch on a not-found page links to the other language of a URL that
	// does not exist, and noindex pages get no alternates in the head anyway
	// (seo.go suppresses them). Declared here so the absence is a decision.
	rec := httptest.NewRecorder()
	writeTexNotFound(context.Background(), rec, "Rig", nil, webi18n.En, "notfound.titlePage", "notfound.page")
	if strings.Contains(rec.Body.String(), `class="langsw"`) {
		t.Errorf("404 shipped a language switch:\n%s", rec.Body.String())
	}
}

// Every server-rendered HTML surface must wire the switch, and the detail page
// is the one that cannot be exercised without a database (texturePageHandler
// takes a concrete *store.Mysql — see the note in texturepage_test.go). This
// gate closes that gap in the same shape sitemap_limit_test.go uses: read the
// source, demand the call. It is not a proxy for behaviour — the behaviour is
// covered above for the two surfaces that need no DB, and the rig curl covers
// the detail page once a texture exists. What it prevents is a future render
// path shipping bilingual copy with no way to switch.
func TestEverySSRRenderPathWiresTheSwitch(t *testing.T) {
	cases := []struct {
		file       string
		wantCall   string
		wantMarkup string
	}{
		{"landing.go", "langSwitchFor(r, d.Lang)", `{{.LangSwitch}}`},
		{"texturepage.go", "langSwitchFor(r, d.Lang)", `{{.LangSwitch}}`},
		{"skinlib_ssr.go", "langSwitchFor(r, lang)", `{{.LangSwitch}}`},
	}
	for _, c := range cases {
		b, err := os.ReadFile(c.file)
		if err != nil {
			t.Fatalf("read %s: %v", c.file, err)
		}
		s := string(b)
		for _, w := range []string{c.wantCall, c.wantMarkup} {
			if !strings.Contains(s, w) {
				t.Errorf("%s no longer wires the language switch (%q missing)", c.file, w)
			}
		}
		// Persistence lives in the render path, not at the mux: a route that
		// forgets it is exactly the bug this file was written for. The landing
		// page is served by spaHandler (main.go), which persists for it.
		if c.file == "texturepage.go" && !strings.Contains(s, "persistLang(w, r)") {
			t.Errorf("%s dropped persistLang — ?hl= would stop sticking", c.file)
		}
	}
	// The list page ships inside the SPA shell, whose handler persists already;
	// saying so here keeps the exception from being read as an oversight.
	if !strings.Contains(mustRead(t, "main.go"), "persistLang(w, r)") {
		t.Error("spaHandler no longer persists an explicit ?hl=")
	}
}

func mustRead(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}
