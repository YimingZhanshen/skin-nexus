package main

// theme-spec §10: <meta name="theme-color"> is the one literal browser
// colour allowed outside the token block, and it must equal theme-base's
// --bg — on a default install the literal IS the chrome colour, so a token
// edit that forgets the three copies ships a browser bar that disagrees
// with the page background (unslop 2026-11-09 #4). appearance.Render
// rewrites the meta when a pack or custom CSS overrides --bg, which leaves
// the no-override case as the only place the literal stands on its own;
// this gate pins all three carriers to the token block.

import (
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"skin-nexus/server/internal/webi18n"
)

var themeColorRE = regexp.MustCompile(`theme-color" content="([^"]*)"`)

func TestThemeColorLiteralMatchesThemeBase(t *testing.T) {
	want := themeBaseDecls(t)["--bg"]
	if want == "" {
		t.Fatal("--bg undefined in theme-base.css; the gate has nothing to compare against")
	}

	// The two SSR templates, asserted on rendered output so a template that
	// stops emitting the meta fails here rather than in a browser.
	var landing strings.Builder
	if err := landingTmpl.Execute(&landing, landingView{
		landingData: landingData{
			Lang: webi18n.ZhCN, SiteName: "Rig", Year: 2026,
			ThemeBase: themeBaseCSS,
		},
		HeadAnchors: template.HTML(landingHeadAnchors),
		BodyAnchor:  template.HTML(landingBodyAnchor),
	}); err != nil {
		t.Fatal(err)
	}
	var detail strings.Builder
	if err := texDetailTmpl.Execute(&detail, detailFixture()); err != nil {
		t.Fatal(err)
	}

	// The SPA shell is a static file, not a template: read it where it lives.
	shell, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "index.html"))
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		where string
		html  string
	}{
		{"landing", landing.String()},
		{"texture detail", detail.String()},
		{"web/index.html", string(shell)},
	} {
		m := themeColorRE.FindStringSubmatch(tc.html)
		if m == nil {
			t.Errorf("%s: no theme-color meta — the browser falls back to its own default", tc.where)
			continue
		}
		if m[1] != want {
			t.Errorf("%s: theme-color content=%q, want theme-base --bg %q", tc.where, m[1], want)
		}
	}
}
