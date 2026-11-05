package seo

// The site serves two languages out of ONE url: the language is chosen from a
// cookie or Accept-Language, and the canonical was locale-blind, so a crawler
// indexed whichever variant it happened to fetch and the other language did not
// exist as a page. Google's get-started asks for localized versions to be
// declared explicitly; Bing's guidelines read the same links. Declaring them
// costs one query parameter per locale and a set of <link rel=alternate> tags —
// and it makes the sitemap able to name both.

import (
	"context"
	"strings"
	"testing"
	"time"
)

func localeEngine() *Engine {
	return NewEngine(&fakeOpts{kv: map[string]string{
		"seo_title":       "SkinNexus 皮肤站",
		"seo_description": "Minecraft skins",
	}}, Defaults{SiteName: "SkinNexus", BaseURL: "https://skin.test"})
}

func TestLocalePageDeclaresBothLanguages(t *testing.T) {
	e := localeEngine()
	html := e.RenderHeadWith(context.Background(), "<!--skin-seo-->", PageMeta{
		Path: "/skinlib/42", Lang: "en",
	})
	for _, want := range []string{
		`rel="canonical" href="https://skin.test/skinlib/42?hl=en"`,
		`rel="alternate" hreflang="en" href="https://skin.test/skinlib/42?hl=en"`,
		`rel="alternate" hreflang="zh-CN" href="https://skin.test/skinlib/42?hl=zh-CN"`,
		`rel="alternate" hreflang="x-default" href="https://skin.test/skinlib/42"`,
		`property="og:locale" content="en"`,
		`property="og:locale:alternate" content="zh-CN"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q\n%s", want, html)
		}
	}
	// inLanguage used to be pinned to zh-CN whatever the page served.
	if !strings.Contains(html, `"inLanguage":"en"`) {
		t.Errorf("the site graph still hard-codes the language: %s", html)
	}
}

// A Chinese page is the mirror image: the served locale is the self-canonical,
// the other one is an alternate, and x-default stays the bare url.
func TestLocalePageDeclaresChineseAsServed(t *testing.T) {
	e := localeEngine()
	html := e.RenderHeadWith(context.Background(), "<!--skin-seo-->", PageMeta{
		Path: "/skinlib/42", Lang: "zh-CN",
	})
	if !strings.Contains(html, `rel="canonical" href="https://skin.test/skinlib/42?hl=zh-CN"`) {
		t.Errorf("canonical does not name the served locale:\n%s", html)
	}
	if !strings.Contains(html, `"inLanguage":"zh-CN"`) {
		t.Errorf("inLanguage = want zh-CN:\n%s", html)
	}
}

// Private textures carry noindex. Declaring them in hreflang/sitemap would ask a
// crawler to index a page the same response tells it to drop.
func TestNoIndexPageDeclaresNoAlternates(t *testing.T) {
	e := localeEngine()
	html := e.RenderHeadWith(context.Background(), "<!--skin-seo-->", PageMeta{
		Path: "/skinlib/42", Lang: "en", NoIndex: true,
	})
	if strings.Contains(html, "hreflang") {
		t.Errorf("a noindex page must not advertise locale alternates:\n%s", html)
	}
	if !strings.Contains(html, `content="noindex, nofollow"`) {
		t.Error("the noindex signal itself went missing")
	}
}

func TestSitemapCarriesLocaleAlternates(t *testing.T) {
	xml := SitemapXML("https://skin.test", "", []SitemapEntry{
		{Path: "/skinlib/42", LastMod: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)},
	})
	for _, want := range []string{
		`xmlns:xhtml="http://www.w3.org/1999/xhtml"`,
		"<loc>https://skin.test/skinlib/42?hl=zh-CN</loc>",
		`rel="alternate" hreflang="en"`,
		`rel="alternate" hreflang="x-default"`,
	} {
		if !strings.Contains(xml, want) {
			t.Errorf("missing %q\n%s", want, xml)
		}
	}
}

// The list page is listed because its first page now ships in the HTML — see
// cmd/skin-nexus/skinlib_ssr.go and its test, which is the half that proves the
// page has crawlable content. Panel routes and login-gated shells stay out
// whatever else changes: an entry a crawler cannot read is soft-404 poison.
func TestSitemapListsOnlyWhatACrawlerCanRead(t *testing.T) {
	xml := SitemapXML("https://skin.test", "", nil)
	if !strings.Contains(xml, `<loc>https://skin.test/skinlib?hl=zh-CN</loc>`) {
		t.Errorf("the server-rendered list page should be listed:\n%s", xml)
	}
	if !strings.Contains(xml, `<loc>https://skin.test/?hl=zh-CN</loc>`) {
		t.Error("the landing page must stay listed")
	}
	for _, banned := range []string{"/dashboard", "/admin", "/profile/", "/user/", "/login", "/register"} {
		if strings.Contains(xml, banned) {
			t.Errorf("%s must not be listed (login-gated or noindexed shell):\n%s", banned, xml)
		}
	}
}
