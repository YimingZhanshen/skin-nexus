package main

// spacing_grid_gate_test.go — the host styles sit on the 4px grid.
//
// W6-1 swept style.css; W6-2c swept the two server-rendered style hosts
// (landing.go, texturepage.go); W6-2d extends the same gate to the player-side
// pages. Spacing-like properties must use multiples of 4px. Exemptions, each
// deliberate:
//   - negative values: off-screen parking (skip-link left:-9999px), not spacing
//   - 2px: the micro-step W6-1 kept (e.g. .meta dt margin-bottom)
//   - font-size on the type scale (12/14/16/20/24/32, plus the 48px hero display):
//     14px is a scale step, not a grid stray — a literal off both scale and grid
//     (the old 23px) still fails
//   - border / outline / border-radius: hairlines and the radius scale (6/10/14)
//     are not spacing and are excluded by the property allowlist
// Comments are stripped first so prose numbers (the 379px/222px measurement
// note) never trip the gate.

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var gridProps = map[string]bool{
	"margin": true, "margin-top": true, "margin-right": true, "margin-bottom": true, "margin-left": true,
	"padding": true, "padding-top": true, "padding-right": true, "padding-bottom": true, "padding-left": true,
	"padding-inline": true, "padding-block": true,
	"gap": true, "height": true,
	"min-height": true, "max-height": true,
	"top": true, "left": true, "bottom": true, "right": true,
	"font-size": true, "line-height": true,
}

// width / min-width / max-width are deliberately absent: table column and form
// input widths are content-driven layout dimensions, not the margin/padding/gap/
// fixed-height spacing the 4px grid governs. Rounding them would re-flow tables.

var fsScale = map[float64]bool{12: true, 14: true, 16: true, 20: true, 24: true, 32: true, 48: true}

var gridFiles = []string{
	"landing.go", "texturepage.go",
	"../../../web/src/pages/ClosetPage.vue",
	"../../../web/src/pages/DashboardPage.vue",
	"../../../web/src/pages/HelpPage.vue",
	"../../../web/src/pages/NotificationsPage.vue",
	"../../../web/src/pages/PlayersPage.vue",
	"../../../web/src/pages/ProfilePage.vue",
	"../../../web/src/pages/SetupPage.vue",
	"../../../web/src/pages/SkinlibPage.vue",
	"../../../web/src/pages/TexturesCard.vue",
	"../../../web/src/components/AdminPanel.vue",
	"../../../web/src/components/admin/AccessLogsView.vue",
	"../../../web/src/components/admin/AuditView.vue",
	"../../../web/src/components/admin/ClosetView.vue",
	"../../../web/src/components/admin/JobsView.vue",
	"../../../web/src/components/admin/PlayersView.vue",
	"../../../web/src/components/admin/PluginsView.vue",
	"../../../web/src/components/admin/ReportsView.vue",
	"../../../web/src/components/admin/SettingsView.vue",
	"../../../web/src/components/admin/StatusView.vue",
	"../../../web/src/components/admin/TexturesView.vue",
	"../../../web/src/components/admin/ThemeMarketSection.vue",
	"../../../web/src/components/admin/UnionView.vue",
	"../../../web/src/components/admin/UsersView.vue",
	"../../../web/src/components/admin/DashboardView.vue",
	"../../../web/src/components/admin/InvitationView.vue",
	"../../../web/src/components/admin/MarketView.vue",
	"../../../web/src/components/admin/OidcClientsCard.vue",
	"../../../web/src/components/admin/SeoSection.vue",
	"../../../web/src/components/admin/UnionBlacklistSection.vue",
}

var (
	gridDeclRE  = regexp.MustCompile(`([a-z-]+)\s*:\s*([^;{}]+)`)
	gridPxRE    = regexp.MustCompile(`(-?\d+(?:\.\d+)?)px`)
	gridComment = regexp.MustCompile(`(?s)/\*.*?\*/|<!--.*?-->`)
	gridGoCmtRE = regexp.MustCompile(`(?m)^\s*//.*$`)
)

func TestSpacingOnFourGrid(t *testing.T) {
	for _, f := range gridFiles {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := gridComment.ReplaceAllString(string(raw), "")
		src = gridGoCmtRE.ReplaceAllString(src, "")
		for _, m := range gridDeclRE.FindAllStringSubmatch(src, -1) {
			prop, val := m[1], m[2]
			if !gridProps[prop] {
				continue
			}
			for _, pm := range gridPxRE.FindAllStringSubmatch(val, -1) {
				v, _ := strconv.ParseFloat(pm[1], 64)
				if v < 0 || v == 2 {
					continue
				}
				if prop == "font-size" && fsScale[v] {
					continue
				}
				if int(v)%4 != 0 {
					t.Errorf("%s: %s: %s — %spx is off the 4px grid", f, prop, strings.TrimSpace(val), pm[1])
				}
			}
		}
	}
}
