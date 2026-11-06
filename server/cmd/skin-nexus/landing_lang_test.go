package main

// The landing page is the first thing a crawler and a brand-new visitor see,
// and it is the one page a reader reaches before the SPA has had a chance to
// set anything. So its language comes from the request, and the Chinese copy
// must not survive into an English render.

import (
	"html/template"
	"strings"
	"testing"

	"skin-nexus/server/internal/webi18n"
)

func renderLandingLang(t *testing.T, lang string) string {
	t.Helper()
	var sb strings.Builder
	v := landingView{
		landingData: landingData{
			Lang: lang, SiteName: "Rig Station", Users: 3, Players: 4, Textures: 5,
			Featured:  []landingTexture{{TID: 1, Name: "Big HD", URL: "/preview/1"}},
			Year:      2026,
			ThemeBase: themeBaseCSS,
			Intro:     webi18n.T(lang, "landing.intro"),
			Facts:     landingFacts(lang),
			LoggedIn:  true,
			Unread:    2,
		},
		HeadAnchors: template.HTML(landingHeadAnchors),
		BodyAnchor:  template.HTML(landingBodyAnchor),
	}
	if err := landingTmpl.Execute(&sb, v); err != nil {
		t.Fatalf("execute: %v", err)
	}
	return sb.String()
}

func TestLandingRendersEnglish(t *testing.T) {
	html := renderLandingLang(t, "en")
	for _, want := range []string{
		`<html lang="en">`,
		"Open the user center",
		"Browse the skin library",
		"Registered users",
		"Game profiles",
		"From the skin library",
		"Enter the skin library",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("english landing missing %q", want)
		}
	}
	if left := visibleCJK(html); left != "" {
		t.Errorf("english landing still renders Chinese copy: %q", left)
	}
	if strings.Contains(html, "皮肤库精选") {
		t.Error("chinese heading survived the language switch")
	}
}

// The Chinese page must read exactly as it did before the catalog landed.
func TestLandingChineseUnchanged(t *testing.T) {
	html := renderLandingLang(t, "zh-CN")
	for _, want := range []string{
		`<html lang="zh-CN">`,
		"进入用户中心",
		"浏览皮肤库",
		"注册用户",
		"游戏角色",
		"皮肤材质",
		"皮肤库精选",
		"进入皮肤库",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("chinese landing missing %q", want)
		}
	}
}

// A call site that forgets to resolve a language must not print an empty page:
// T() falls back to the site language, which is what the renderer also does.
func TestLandingWithoutLangFallsBack(t *testing.T) {
	html := renderLandingLang(t, "")
	if !strings.Contains(html, "进入皮肤库") {
		t.Error("empty lang must fall back to the Chinese copy")
	}
}
