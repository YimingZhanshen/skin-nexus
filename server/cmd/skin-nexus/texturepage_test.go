package main

// texturepage_test.go: DB-free coverage for the server-rendered texture
// detail page — template contract, JSON-LD safety, description copy.
// The handler itself needs *store.Mysql (concrete type); its visibility
// gate is covered by the store-layer SkinlibOne integration test.

import (
	"context"
	"html/template"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

func detailFixture() texDetailData {
	return texDetailData{
		SiteName:      "测试站",
		TID:           42,
		Name:          "测试皮肤",
		Type:          "skin",
		Model:         "slim",
		Nickname:      "Alice",
		Likes:         7,
		Public:        true,
		UploadAt:      "2026-10-01",
		TextureURL:    "https://skin.example.com/textures/abc",
		PreviewURL:    "/preview/42",
		PreviewAbsURL: "https://skin.example.com/preview/42",
		Year:          2026,
		ThemeBase:     themeBaseCSS,
		HeadAnchors:   template.HTML(landingHeadAnchors),
		BodyAnchor:    template.HTML(landingBodyAnchor),
	}
}

// The detail page must ship complete, crawler-visible content without JS:
// name, uploader, model, static preview image, download link, JSON-LD.
func TestTexDetailTemplateRenders(t *testing.T) {
	d := detailFixture()
	var sb strings.Builder
	if err := texDetailTmpl.Execute(&sb, d); err != nil {
		t.Fatal(err)
	}
	body := sb.String()
	for _, want := range []string{
		"--bg: #101825",     // theme-base tokens injected (single source of truth)
		"测试皮肤",              // name in h1
		"Alice",             // uploader
		"Alex 瘦臂",           // slim model label
		"/preview/42",       // static server-rendered preview (crawler-visible)
		`data-skinview3d`,   // progressive-enhancement canvas marker
		`data-model="slim"`, // model passthrough for skinview3d
		"/assets/viewer.js", // fixed-name enhancement module
		"下载材质 PNG",          // download action
		"材质 ID",             // meta grid label
		"#42",               // tid
		"2026-10-01",        // upload date
		landingHeadAnchors,  // injection anchors preserved for SEO/theme layers
		landingBodyAnchor,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("detail page missing %q:\n%s", want, body)
		}
	}
	// anchors must NOT be stripped by html/template (2026-10-25 regression
	// class: comments in template source get eaten — anchors are injected
	// as template.HTML trusted values, assert that stays true here)
}

// The same page, in English: every label the catalog carries must come from the
// catalog, and no Chinese copy may survive in the rendered output. Data (site
// name, texture name) stays whatever the uploader typed, so the fixture uses
// latin data and the CJK scan only has to strip style blocks and comments —
// the same rule scripts/check-locales.mjs applies to the templates.
func TestTexDetailTemplateRendersEnglish(t *testing.T) {
	d := detailFixture()
	d.Lang = "en"
	d.SiteName = "Rig Station"
	d.Name = "Big HD skin"
	d.Nickname = "Alice"
	d.PluginSlot = template.HTML("<!--PLUGIN-SLOT-MARKER-->")
	var sb strings.Builder
	if err := texDetailTmpl.Execute(&sb, d); err != nil {
		t.Fatal(err)
	}
	body := sb.String()
	for _, want := range []string{
		`<html lang="en">`,
		"Download the PNG",
		"Texture ID",
		"Skin (Alex slim)",
		"uploaded by",
		"Skip to main content",
		"Collect into your closet",
		`<span id="like-count">7</span> `, // the English unit is empty by design
		`window.__SKIN_I18N__={"lang":"en"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("english detail page missing %q", want)
		}
	}
	// The plugin slot anchor and the injection anchors still survive — the
	// signed skin-importer consumes the first, themes the others.
	for _, keep := range []string{"<!--PLUGIN-SLOT-MARKER-->", landingHeadAnchors, landingBodyAnchor} {
		if !strings.Contains(body, keep) {
			t.Fatalf("render dropped %q", keep)
		}
	}
	if left := visibleCJK(body); left != "" {
		t.Errorf("english page still renders Chinese copy: %q", left)
	}

	// The 404 page follows the same language — through the real helper, so the
	// title/body pairing (a missing texture and a missing page title
	// differently) is covered too.
	rec := httptest.NewRecorder()
	writeTexNotFound(context.Background(), rec, "Rig Station", nil, "en", "notfound.title", "notfound.texture")
	if rec.Code != http.StatusNotFound {
		t.Errorf("404 status = %d", rec.Code)
	}
	if got := rec.Body.String(); !strings.Contains(got, "Texture not found - Rig Station") ||
		!strings.Contains(got, "This texture does not exist") || visibleCJK(got) != "" {
		t.Errorf("english 404 = %s", got)
	}
	// The generic page 404 must not borrow the texture wording in its title.
	rec2 := httptest.NewRecorder()
	writeTexNotFound(context.Background(), rec2, "Rig Station", nil, "en", "notfound.titlePage", "notfound.page")
	if got := rec2.Body.String(); !strings.Contains(got, "Page not found - Rig Station") ||
		!strings.Contains(got, "This page does not exist") {
		t.Errorf("english page 404 = %s", got)
	}
}

// visibleCJK returns the first run of CJK runes outside <style>, CSS comments
// and HTML comments — the leftovers that mean copy was not catalogued.
var (
	styleRE       = regexp.MustCompile(`(?s)<style.*?</style>`)
	cssCommentRE  = regexp.MustCompile(`(?s)/\*.*?\*/`)
	htmlCommentRE = regexp.MustCompile(`(?s)<!--.*?-->`)
)

func visibleCJK(html string) string {
	s := styleRE.ReplaceAllString(html, "")
	s = cssCommentRE.ReplaceAllString(s, "")
	s = htmlCommentRE.ReplaceAllString(s, "")
	isCJK := func(r rune) bool { return r >= 0x4E00 && r <= 0x9FFF }
	for i, r := range s {
		if !isCJK(r) {
			continue
		}
		end := i
		for end < len(s) {
			rn, size := utf8.DecodeRuneInString(s[end:])
			if size <= 1 || !isCJK(rn) {
				break
			}
			end += size
		}
		return s[i:end]
	}
	return ""
}

func TestTexDetailConfiguredFooter(t *testing.T) {
	d := detailFixture()
	d.FooterHTML = template.HTML(`<nav class="site-footer-links"><a href="https://example.com">友链</a></nav><p class="site-footer-credit">Powered by SkinNexus</p>`)
	var sb strings.Builder
	if err := texDetailTmpl.Execute(&sb, d); err != nil {
		t.Fatal(err)
	}
	body := sb.String()
	for _, want := range []string{`class="site-footer"`, "site-footer-links", "友链", "Powered by SkinNexus"} {
		if !strings.Contains(body, want) {
			t.Fatalf("detail footer missing %q:\n%s", want, body)
		}
	}
}

// Capes have no body render: flat texture preview, no 3D canvas.
func TestTexDetailCapeTemplate(t *testing.T) {
	d := detailFixture()
	d.Type = "cape"
	d.PreviewURL = "/textures/abc"
	var sb strings.Builder
	if err := texDetailTmpl.Execute(&sb, d); err != nil {
		t.Fatal(err)
	}
	body := sb.String()
	if !strings.Contains(body, "/textures/abc") {
		t.Fatalf("cape page missing flat texture URL:\n%s", body)
	}
	if strings.Contains(body, "data-skinview3d") {
		t.Fatalf("cape page must not carry the 3D canvas:\n%s", body)
	}
	if !strings.Contains(body, "披风") {
		t.Fatalf("cape page missing cape label:\n%s", body)
	}
}

// Private pages carry noindex in their SEO head (pm.NoIndex=true path is
// asserted at the seo layer; here we verify the data flag wiring).
func TestTexDetailPrivateFlag(t *testing.T) {
	d := detailFixture()
	d.Public = false
	var sb strings.Builder
	if err := texDetailTmpl.Execute(&sb, d); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sb.String(), "私密") {
		t.Fatalf("private page missing visibility label:\n%s", sb.String())
	}
}

// Adversarial texture names must never close the JSON-LD script element
// or break out of an HTML attribute.
func TestTexJSONLDSafe(t *testing.T) {
	d := detailFixture()
	d.Name = `</script><script>alert(1)</script>`
	raw := texJSONLD(d, "https://skin.example.com/skinlib/42")
	if raw == "" {
		t.Fatal("JSON-LD empty")
	}
	// json.Marshal escapes < > & — no raw close-tag survives marshalling
	if strings.Contains(raw, "</script>") {
		t.Fatalf("raw close tag in JSON-LD: %s", raw)
	}
	// the injection layer additionally neutralizes "</" — simulate it
	neutralized := strings.ReplaceAll(raw, "</", "<\\/")
	if strings.Contains(neutralized, "</script><script>") {
		t.Fatalf("neutralization failed: %s", neutralized)
	}
}

func TestTexDescriptionCopy(t *testing.T) {
	d := detailFixture()
	got := texDescription(d)
	for _, want := range []string{"测试皮肤", "测试站", "皮肤", "Alice", "7 人收藏", "Alex 瘦臂模型"} {
		if !strings.Contains(got, want) {
			t.Fatalf("description missing %q: %s", want, got)
		}
	}
	d.Type = "cape"
	d.Name = "披风甲"
	got = texDescription(d)
	if !strings.Contains(got, "披风") {
		t.Fatalf("cape description missing 披风: %s", got)
	}
}

func TestAbsURL(t *testing.T) {
	cases := []struct{ base, rel, want string }{
		{"https://s.example", "/preview/1", "https://s.example/preview/1"},
		{"https://s.example/", "/preview/1", "https://s.example/preview/1"},
		{"https://s.example", "https://cdn.example/x.png", "https://cdn.example/x.png"},
	}
	for _, c := range cases {
		if got := absURL(c.base, c.rel); got != c.want {
			t.Errorf("absURL(%q,%q) = %q want %q", c.base, c.rel, got, c.want)
		}
	}
}
