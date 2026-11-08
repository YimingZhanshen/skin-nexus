package seo

// Site-level copy in the language of the page. The fallback sentences used to
// live only inside this package, in Chinese, so a site whose operator never
// touched the SEO fields served an English <title> and description to every
// crawler — while the page body around them was English.
//
// The expected strings below are typed out, not produced by the code under
// test: the point is what a crawler reads, and a test that recomputed them
// from SiteCopy would only re-run the implementation.

import (
	"context"
	"strings"
	"testing"
)

type memOptions map[string]string

func (m memOptions) GetOption(_ context.Context, k string) (string, bool, error) {
	v, ok := m[k]
	return v, ok, nil
}

func (m memOptions) SetOption(_ context.Context, k, v string) error {
	m[k] = v
	return nil
}

var enCopy = SiteCopy{
	TitleDescriptor:     "Minecraft skin site · skins, capes and profile management",
	DescriptionWithSite: "{site}: upload and share Minecraft skins and capes with Yggdrasil login support.",
	Description:         "Minecraft skin site: upload and share skins and capes with Yggdrasil login support.",
}

func TestGeneratedTitleFollowsTheInjectedLanguage(t *testing.T) {
	ctx := context.Background()
	e := NewEngine(memOptions{}, Defaults{SiteName: "Rig", BaseURL: "https://rig.example"})

	out := e.RenderHeadWith(ctx, "<html><head><!--skin-seo--></head></html>", PageMeta{Lang: "en", Copy: enCopy})
	if !strings.Contains(out, "<title>Rig｜"+enCopy.TitleDescriptor+"</title>") {
		t.Fatalf("English page shipped no English title; head was:\n%s", out)
	}
	if strings.Contains(out, titleDescriptor) {
		t.Fatalf("Chinese descriptor survived on the English page: %q", titleDescriptor)
	}
	if !strings.Contains(out, `<meta name="description" content="Rig: upload and share Minecraft skins and capes with Yggdrasil login support.">`) {
		t.Fatalf("description did not carry the injected site name: %s", out)
	}
	if !strings.Contains(out, `<meta property="og:description" content="Rig: upload and share`) {
		t.Fatalf("og:description disagreed with the description: %s", out)
	}

	// A page served in Chinese arrives with the zero-value copy from any caller
	// that does not resolve a language, and that has to be the old Chinese.
	zh := e.RenderHeadWith(ctx, "<html><head><!--skin-seo--></head></html>", PageMeta{Lang: "zh-CN"})
	if !strings.Contains(zh, "<title>Rig｜"+titleDescriptor+"</title>") {
		t.Fatalf("the short site name should still pair with the descriptor: %s", zh)
	}
}

// An operator who wrote a title wrote it once, for every language: injecting
// copy must never rewrite what the panel stores.
func TestOperatorWrittenCopyIsNotTranslated(t *testing.T) {
	ctx := context.Background()
	opts := memOptions{"seo_title": "我的皮肤站", "seo_description": "中国人气皮肤站"}
	e := NewEngine(opts, Defaults{SiteName: "Rig", BaseURL: "https://rig.example"})

	out := e.RenderHeadWith(ctx, "<html><head><!--skin-seo--></head></html>", PageMeta{Lang: "en", Copy: enCopy})
	if !strings.Contains(out, "<title>我的皮肤站</title>") {
		t.Fatalf("an operator's own title was replaced: %s", out)
	}
	if !strings.Contains(out, `content="中国人气皮肤站"`) {
		t.Fatalf("an operator's own description was replaced: %s", out)
	}
}

// The zero value of SiteCopy is what every caller that says nothing about
// language gets, and it must be the Chinese this package always shipped —
// byte for byte, or the change silently moved text crawlers already index.
func TestZeroCopyKeepsThePreviousChineseOutput(t *testing.T) {
	siteName := "Rig"
	if got, want := defaultTitle(siteName, SiteCopy{}), siteName+"｜"+titleDescriptor; got != want {
		t.Fatalf("defaultTitle with no copy = %q, want %q", got, want)
	}
	wantDesc := "Rig：上传与分享 Minecraft 皮肤和披风，管理角色、衣柜与皮肤库，支持 Yggdrasil 外置登录与启动器接入。"
	if got := defaultDescription(siteName, SiteCopy{}); got != wantDesc {
		t.Fatalf("defaultDescription with no copy = %q, want the sentence shipped before: %q", got, wantDesc)
	}
	if got := defaultDescription("", SiteCopy{}); got != descBareZh {
		t.Fatalf("nameless description = %q, want %q", got, descBareZh)
	}
}

// The site entity in the JSON-LD describes the site too; it used to ignore the
// page language even after inLanguage stopped doing so.
func TestSiteJSONLDUsesTheInjectedDescription(t *testing.T) {
	ctx := context.Background()
	e := NewEngine(memOptions{}, Defaults{SiteName: "Rig", BaseURL: "https://rig.example"})
	out := e.RenderHeadWith(ctx, "<html><head><!--skin-seo--></head></html>", PageMeta{Lang: "en", Copy: enCopy})
	want := `"description":"Rig: upload and share Minecraft skins and capes with Yggdrasil login support."`
	if !strings.Contains(out, want) {
		t.Fatalf("the JSON-LD site description did not follow the injected copy; wanted %s in:\n%s", want, out)
	}
	if strings.Contains(out, "上传与分享 Minecraft 皮肤和披风") {
		t.Fatalf("the JSON-LD still carried the Chinese sentence on an English page: %s", out)
	}
}
