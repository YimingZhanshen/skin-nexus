package main

// notice_badge_test.go: the SSR chrome must show the same unread badge the SPA
// sidebar shows (2026-11-02) — a player who is served the server-rendered
// landing or texture page must not see a stale "nothing waiting" header.
// DB-free on purpose: these assert the template contract, the count itself is
// covered by the store integration suite.

import (
	"html/template"
	"strings"
	"testing"
)

func renderTex(t *testing.T, unread int, loggedIn bool) string {
	t.Helper()
	d := detailFixture()
	d.Unread = unread
	d.LoggedIn = loggedIn
	var sb strings.Builder
	if err := texDetailTmpl.Execute(&sb, d); err != nil {
		t.Fatalf("execute: %v", err)
	}
	return sb.String()
}

func TestTexTopbarUnreadBadge(t *testing.T) {
	withBadge := renderTex(t, 3, true)
	if !strings.Contains(withBadge, `href="/user/dashboard"`) {
		t.Fatal("logged-in topbar must offer the user center")
	}
	if !strings.Contains(withBadge, `unread-badge">3<`) {
		t.Error("topbar badge missing: expected the unread count 3 in the SSR html")
	}
	if without := renderTex(t, 0, true); strings.Contains(without, `class="unread-badge"`) {
		t.Error("nothing unread: SSR page must not render an empty badge")
	}
	// A badge without a session would leak a count into an anonymous,
	// cacheable page — the template must not render one.
	if anon := renderTex(t, 5, false); strings.Contains(anon, `unread-badge">5<`) {
		t.Error("anonymous render must not carry an unread count")
	}
}

func TestLandingTopbarUnreadBadge(t *testing.T) {
	render := func(unread int, loggedIn bool) string {
		var sb strings.Builder
		v := landingView{
			landingData: landingData{
				SiteName: "测试站", Year: 2026, LoggedIn: loggedIn, Unread: unread,
				ThemeBase: themeBaseCSS,
			},
			HeadAnchors: template.HTML(landingHeadAnchors),
			BodyAnchor:  template.HTML(landingBodyAnchor),
		}
		if err := landingTmpl.Execute(&sb, v); err != nil {
			t.Fatalf("execute: %v", err)
		}
		return sb.String()
	}
	if html := render(2, true); !strings.Contains(html, `unread-badge">2<`) {
		t.Error("landing badge missing for a signed-in visitor")
	}
	if html := render(0, true); strings.Contains(html, `class="unread-badge"`) {
		t.Error("landing must not render an empty badge")
	}
	if html := render(7, false); strings.Contains(html, `unread-badge">7<`) {
		t.Error("landing CTA for anonymous visitors must not carry an unread count")
	}
}
