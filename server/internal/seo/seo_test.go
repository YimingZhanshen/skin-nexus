package seo

import (
	"context"
	"strings"
	"testing"
	"time"
)

// fakeOpts is a minimal in-memory OptionStore.
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

func TestSettingsDefaults(t *testing.T) {
	e := NewEngine(&fakeOpts{}, Defaults{SiteName: "SkinNexus", BaseURL: "https://skin.test"})
	s := e.Settings(context.Background())
	if s.Title != defaultTitle("SkinNexus", zhCopy) {
		t.Fatalf("title = %q", s.Title)
	}
	if len([]rune(s.Title)) < 15 {
		t.Fatalf("default title too short for search engines: %q", s.Title)
	}
	if !s.SitemapOn {
		t.Fatal("sitemap should default on")
	}
	if !strings.Contains(s.Robots, "Sitemap: https://skin.test/sitemap.xml") {
		t.Fatalf("robots = %q", s.Robots)
	}
}

func TestRenderHeadInjectsMeta(t *testing.T) {
	e := NewEngine(&fakeOpts{kv: map[string]string{
		"seo_title":       "我的皮肤站",
		"seo_description": "desc",
		"seo_keywords":    "mc,skin",
		"seo_og_image":    "https://skin.test/og.png",
	}}, Defaults{SiteName: "SkinNexus", BaseURL: "https://skin.test"})
	html := e.RenderHead(context.Background(), "<html><head><!--skin-seo--></head><body></body></html>")
	for _, want := range []string{
		"<title>我的皮肤站</title>",
		`name="description" content="desc"`,
		`name="keywords" content="mc,skin"`,
		`property="og:image" content="https://skin.test/og.png"`,
		`rel="canonical" href="https://skin.test/"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %q in %q", want, html)
		}
	}
}

func TestRenderHeadEmitsSiteGraph(t *testing.T) {
	e := NewEngine(&fakeOpts{kv: map[string]string{"seo_description": "示例描述"}},
		Defaults{SiteName: "示例站", BaseURL: "https://skin.test"})
	html := e.RenderHead(context.Background(), "<!--skin-seo-->")
	for _, want := range []string{
		`"@type":"WebSite"`,
		`"@type":"Organization"`,
		`property="og:site_name" content="示例站"`,
		`name="twitter:card"`,
		`https://skin.test/favicon.svg`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %q in %q", want, html)
		}
	}
}

func TestLLMSTxt(t *testing.T) {
	e := NewEngine(&fakeOpts{}, Defaults{SiteName: "示例站", BaseURL: "https://skin.test"})
	txt := e.LLMSTxt(context.Background())
	for _, want := range []string{
		"# 示例站",
		"https://skin.test/",
		"https://skin.test/skinlib",
		"https://skin.test/sitemap.xml",
		"launcher-api",
	} {
		if !strings.Contains(txt, want) {
			t.Fatalf("missing %q in %q", want, txt)
		}
	}
}

func TestRenderHeadEscapes(t *testing.T) {
	e := NewEngine(&fakeOpts{kv: map[string]string{
		"seo_title": `<script>alert(1)</script>`,
	}}, Defaults{SiteName: "X", BaseURL: "https://skin.test"})
	html := e.RenderHead(context.Background(), "<!--skin-seo-->")
	if strings.Contains(html, "<script>alert") {
		t.Fatalf("unescaped title: %q", html)
	}
}

func TestNoIndex(t *testing.T) {
	e := NewEngine(&fakeOpts{kv: map[string]string{"seo_noindex": "true"}},
		Defaults{SiteName: "X", BaseURL: "https://skin.test"})
	s := e.Settings(context.Background())
	if !strings.Contains(s.Robots, "Disallow: /") {
		t.Fatalf("robots = %q", s.Robots)
	}
	html := e.RenderHead(context.Background(), "<!--skin-seo-->")
	if !strings.Contains(html, `content="noindex, nofollow"`) {
		t.Fatalf("html = %q", html)
	}
}

func TestSitemapXML(t *testing.T) {
	xml := SitemapXML("https://skin.test", "", []SitemapEntry{
		{Path: "/skinlib/42", LastMod: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)},
		{Path: "/skinlib/43"},
	})
	// The sitemap lists the URLs that are their own canonical: one per shipped
	// language. The bare path only appears as hreflang="x-default", because a
	// visit to it canonicalises to whichever locale the negotiation picked — a
	// <loc> for it would be a third copy of the same page, which is exactly the
	// duplicate cluster the search engines ask us to consolidate.
	for _, want := range []string{
		"<loc>https://skin.test/?hl=zh-CN</loc>",
		"<loc>https://skin.test/?hl=en</loc>",
		"<loc>https://skin.test/skinlib/42?hl=zh-CN</loc>",
		"<loc>https://skin.test/skinlib/42?hl=en</loc>",
		"<loc>https://skin.test/skinlib/43?hl=en</loc>",
		`hreflang="x-default" href="https://skin.test/skinlib/42"`,
		"<lastmod>2026-10-01</lastmod>",
	} {
		if !strings.Contains(xml, want) {
			t.Fatalf("missing %q in %q", want, xml)
		}
	}
	// one page with a date contributes one <lastmod> per language; the entry
	// without a date contributes none at all (an empty <lastmod> is worse than
	// no <lastmod>)
	if got := strings.Count(xml, "<lastmod>"); got != 2 {
		t.Fatalf("lastmod appears %d times, want 2 (the dated page, once per language): %q", got, xml)
	}
	// player profile pages are gone from the sitemap (SPA shells, no
	// anonymous content — soft-404 poison)
	if strings.Contains(xml, "/profile/") {
		t.Fatalf("profile entries must not be listed: %q", xml)
	}
}

// Per-page JSON-LD is injected into the SEO block and can never close its
// script element early, even with adversarial texture names.
func TestRenderHeadJSONLD(t *testing.T) {
	e := NewEngine(&fakeOpts{}, Defaults{SiteName: "X", BaseURL: "https://skin.test"})
	pm := PageMeta{JSONLD: `{"@type":"CreativeWork","name":"</script><img src=x>"}`}
	html := e.RenderHeadWith(context.Background(), "<!--skin-seo-->", pm)
	if !strings.Contains(html, `type="application/ld+json"`) {
		t.Fatalf("JSON-LD block missing: %q", html)
	}
	if strings.Contains(html, "</script><img") {
		t.Fatalf("raw </ not neutralized: %q", html)
	}
	if !strings.Contains(html, `<\/script>`) {
		t.Fatalf("escaped form missing: %q", html)
	}
}

// Per-page NoIndex wins over the site-wide indexable setting (private
// texture viewed by its owner).
func TestPerPageNoIndex(t *testing.T) {
	e := NewEngine(&fakeOpts{}, Defaults{SiteName: "X", BaseURL: "https://skin.test"})
	html := e.RenderHeadWith(context.Background(), "<!--skin-seo-->", PageMeta{NoIndex: true})
	if !strings.Contains(html, `content="noindex, nofollow"`) {
		t.Fatalf("per-page noindex missing: %q", html)
	}
}

// Per-page OG image overrides the site-wide default.
func TestPerPageOGImage(t *testing.T) {
	e := NewEngine(&fakeOpts{kv: map[string]string{"seo_og_image": "https://skin.test/site.png"}},
		Defaults{SiteName: "X", BaseURL: "https://skin.test"})
	html := e.RenderHeadWith(context.Background(), "<!--skin-seo-->", PageMeta{OGImage: "https://skin.test/preview/7"})
	if !strings.Contains(html, `content="https://skin.test/preview/7"`) {
		t.Fatalf("per-page og:image missing: %q", html)
	}
}

func TestDefaultTitleKeepsLongSiteName(t *testing.T) {
	long := "这是一个足够长的站点名称不需要后缀"
	if got := defaultTitle(long, zhCopy); got != long {
		t.Fatalf("long site name got %q", got)
	}
	if got := defaultTitle("示例站", zhCopy); !strings.HasPrefix(got, "示例站｜") || len([]rune(got)) < 15 {
		t.Fatalf("short site name got %q", got)
	}
}

func TestSaveRoundTrip(t *testing.T) {
	e := NewEngine(&fakeOpts{}, Defaults{SiteName: "X", BaseURL: "https://skin.test"})
	s := e.FillDefaults(context.Background())
	s.Title = "新标题"
	if err := e.Save(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if got := e.Settings(context.Background()).Title; got != "新标题" {
		t.Fatalf("title after save = %q", got)
	}
}
