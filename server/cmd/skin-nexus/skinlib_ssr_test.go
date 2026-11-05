package main

// The claim under test is narrow and load-bearing: a crawler that does not run
// JavaScript must still find the library's content and the links into it. Bing
// asks for critical content not to live behind client-side rendering, and AI
// crawlers (GPTBot, ClaudeBot, PerplexityBot) never execute JS at all — to them
// an empty #app is an empty page.

import (
	"context"
	"errors"
	"html/template"
	"net/http/httptest"
	"strings"
	"testing"

	"skin-nexus/server/internal/store"
)

type fakeSkinlib struct {
	rows  []store.SkinlibRow
	total int
	err   error
	opt   store.SkinlibOpts
}

func (f *fakeSkinlib) SkinlibList(_ context.Context, o store.SkinlibOpts) ([]store.SkinlibRow, int, error) {
	f.opt = o
	if f.err != nil {
		return nil, 0, f.err
	}
	return f.rows, f.total, nil
}

const shellFixture = `<!DOCTYPE html><html lang="zh-CN"><head><!--skin-seo--></head><body>` +
	`<div id="app"><!--skin-app-start--><div class="boot-static"><p>…</p></div><!--skin-app-end--></div>` +
	`<script type="module" src="/src/main.js"></script></body></html>`

func TestSkinlibPrerenderShipsContentInHTML(t *testing.T) {
	st := &fakeSkinlib{
		rows: []store.SkinlibRow{
			{TID: 7, Name: "夜行者", Nickname: "steve", Type: "skin"},
			{TID: 8, Name: `Cape "quoted"`, Nickname: "alex", Type: "cape"},
		},
		total: 61,
	}
	r := httptest.NewRequest("GET", "/skinlib?hl=en", nil)
	block, pm, ok := prerenderSkinlib(r.Context(), r, st, "Rig Station")
	if !ok {
		t.Fatal("prerender refused with a healthy store")
	}
	if pm.NoIndex {
		t.Error("a page carrying real content must not be noindexed")
	}
	if pm.Lang != "en" || pm.Path != "/skinlib" {
		t.Errorf("PageMeta = %+v, want the served locale on /skinlib", pm)
	}
	if !strings.Contains(pm.Description, "61") {
		t.Errorf("description should state the size of the library: %q", pm.Description)
	}
	for _, want := range []string{
		`<a href="/skinlib/7">`, `href="/skinlib/8"`,
		`<img src="/preview/7"`,
		`alt="夜行者 — skin"`, // the name is text, not an image-only tile
		"61 public textures",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("prerendered block missing %q:\n%s", want, block)
		}
	}
	// A name with quotes cannot break out of the attribute it lands in.
	if strings.Contains(block, `alt="Cape "quoted`) {
		t.Errorf("texture name escaped poorly:\n%s", block)
	}
	// The list page only asks for public rows, at the server slice size.
	if !st.opt.PublicOnly || st.opt.PerPage != ssrSkinlibPerPage || st.opt.Page != 1 {
		t.Errorf("query opts = %+v, want public-only first page", st.opt)
	}

	merged := injectAppContent(shellFixture, block)
	if strings.Contains(merged, "boot-static") {
		t.Error("the boot spinner survived: users would see two lists")
	}
	if !strings.Contains(merged, `<div id="app">`) || !strings.Contains(merged, `href="/skinlib/7"`) {
		t.Errorf("prerender did not land inside #app:\n%s", merged)
	}
	if strings.Contains(merged, skinlibAppStart) != strings.Contains(merged, skinlibAppEnd) {
		t.Error("one marker was eaten; a later swap would silently stop working")
	}
}

// Filters and locale have to survive into the pagination links: a `?page=2` that
// drops `hl` or `q` sends the crawler somewhere else than the page it is on.
func TestSkinlibPaginationKeepsQueryAndLocale(t *testing.T) {
	st := &fakeSkinlib{rows: make([]store.SkinlibRow, ssrSkinlibPerPage), total: 200}
	r := httptest.NewRequest("GET", "/skinlib?hl=en&sort=likes&type=cape&page=2&q=hero", nil)
	block, _, ok := prerenderSkinlib(r.Context(), r, st, "Rig")
	if !ok {
		t.Fatal("prerender refused")
	}
	for _, want := range []string{
		`href="/skinlib?page=1&amp;sort=likes&amp;type=cape&amp;q=hero&amp;hl=en"`,
		`href="/skinlib?page=3&amp;sort=likes&amp;type=cape&amp;q=hero&amp;hl=en"`,
	} {
		if !strings.Contains(block, want) {
			t.Errorf("pager missing %q:\n%s", want, block)
		}
	}
	if st.opt.Sort != "likes" || st.opt.Type != "cape" || st.opt.Keyword != "hero" || st.opt.Page != 2 {
		t.Errorf("query not carried into the store call: %+v", st.opt)
	}
}

// A store failure must degrade to the plain shell, not to a broken page: the SPA
// can still answer once it boots, and the list has never been login-gated.
func TestSkinlibPrerenderFallsBackToTheShell(t *testing.T) {
	st := &fakeSkinlib{err: errors.New("db down")}
	r := httptest.NewRequest("GET", "/skinlib", nil)
	if _, _, ok := prerenderSkinlib(r.Context(), r, st, "Rig"); ok {
		t.Fatal("a failing store should mean " + `"leave the shell alone", not a half-rendered page`)
	}
	if _, _, ok := prerenderSkinlib(r.Context(), r, nil, "Rig"); ok {
		t.Fatal("no lister wired should behave like a failing store")
	}
}

// Empty libraries are a real state on a fresh install; the page should say so
// instead of rendering an empty grid with no words to index.
func TestSkinlibEmptyStateStillSaysSomething(t *testing.T) {
	st := &fakeSkinlib{}
	r := httptest.NewRequest("GET", "/skinlib", nil)
	block, pm, ok := prerenderSkinlib(r.Context(), r, st, "Rig")
	if !ok {
		t.Fatal("an empty library is a page, not a failure")
	}
	if !strings.Contains(block, "还没有公开材质") {
		t.Errorf("empty state missing (zh is the negotiated default):\n%s", block)
	}
	if !strings.Contains(pm.Description, "0") {
		t.Errorf("description should still be honest about an empty library: %q", pm.Description)
	}
}

// The prerendered block is built with html/template over fields that include a
// user-chosen texture name; this asserts the one place where a template.CSS and
// a plain string meet does not become an injection point.
func TestSkinlibPrerenderEscapesNames(t *testing.T) {
	st := &fakeSkinlib{rows: []store.SkinlibRow{{TID: 1, Name: "</a><script>x</script>", Nickname: "e", Type: "skin"}}}
	r := httptest.NewRequest("GET", "/skinlib", nil)
	block, _, ok := prerenderSkinlib(r.Context(), r, st, "Rig")
	if !ok {
		t.Fatal("prerender refused")
	}
	if strings.Contains(block, "<script>x</script>") {
		t.Errorf("a texture name opened a script tag:\n%s", block)
	}
	if template.HTMLEscapeString("</a>") != "&lt;/a&gt;" {
		t.Fatal("sanity: escaping helper changed")
	}
}
