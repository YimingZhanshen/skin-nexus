package main

// spa_admin_test.go — the admin panel must own a real URL.
//
// Before this, /admin and /admin/<tab> were not in the shell whitelist, so the
// server answered a true 404 before the SPA could load; and the panel kept its
// active tab only in memory, so refresh / share / back all landed on the panel
// dashboard. The panel is the one surface an operator bookmarks and sends to a
// colleague ("look at 用户管理"), so losing the address is a real defect, not a
// nicety. These tests pin the server half; the SPA half is pinned by the
// existing TestSPARoutesAgreeWithAppMaps plus the source assertions below.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdminDeepLinkWhitelist(t *testing.T) {
	for _, p := range []string{"admin", "admin/users", "admin/settings", "admin/plugin/demo"} {
		if !isKnownSPAPath(p, nil) {
			t.Fatalf("%q is not SPA-known — a shared panel link would 404 before the shell loads", p)
		}
	}
	// The prefix must be a path boundary, not a substring: /administrator is
	// not a panel route and must stay a true 404.
	if isKnownSPAPath("administrator", nil) || isKnownSPAPath("adminx/users", nil) {
		t.Fatal("admin prefix leaked past a path boundary")
	}
}

// The SPA must resolve the admin prefix on both cold boot and back/forward, and
// the panel must write its tab into the URL (otherwise the whitelist alone only
// fixes the 404, not the lost state).
func TestAdminDeepLinkSpaWiring(t *testing.T) {
	app, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "src", "App.vue"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(app), "startsWith('/admin/')"); n < 2 {
		t.Fatalf("App.vue handles the /admin/ prefix %d times, want >=2 (boot + popstate)", n)
	}
	panel, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "src", "components", "AdminPanel.vue"))
	if err != nil {
		t.Fatal(err)
	}
	ps := string(panel)
	if !strings.Contains(ps, "history.pushState") {
		t.Fatal("AdminPanel never writes its tab into the URL — refresh loses the active view")
	}
	if !strings.Contains(ps, "popstate") {
		t.Fatal("AdminPanel ignores popstate — back/forward would not move the tab")
	}
}
