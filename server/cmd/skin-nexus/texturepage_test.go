package main

// texturepage_test.go: DB-free coverage for the server-rendered texture
// detail page — template contract, JSON-LD safety, description copy.
// The handler itself needs *store.Mysql (concrete type); its visibility
// gate is covered by the store-layer SkinlibOne integration test.

import (
	"html/template"
	"strings"
	"testing"
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
		"测试皮肤",              // name in h1
		"Alice",             // uploader
		"Alex 瘦臂",           // slim model label
		"/preview/42",       // static server-rendered preview (crawler-visible)
		`data-skinview3d`,   // progressive-enhancement canvas marker
		`data-model="slim"`, // model passthrough for skinview3d
		"/assets/viewer.js", // fixed-name enhancement module
		"下载纹理 PNG",          // download action
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
	for _, want := range []string{"测试皮肤", "测试站", "皮肤", "Alice", "7 人喜欢", "Alex 瘦臂模型"} {
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
