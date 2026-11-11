package main

// landing_scope_test.go — who the landing page is written for.
//
// The landing page is the front door for players and for the Minecraft server
// owner who has to decide whether to run this software. Until 2026-11-12 it
// also carried an answer-first fact block (AEO, 2026-11-08) answering five
// questions about the software itself: which launcher auth protocols, which
// database engines, whether a blessing-skin-server site can migrate, whether
// marketplace packs are signature-checked, what an anonymous reader can see.
//
// The webmaster took that block off the page: a player arriving to pick a skin
// and a server owner scanning for a register button do not read a software
// datasheet, and the block sat between the intro and the way into the library.
// The same five facts stay documented in the repository — docs/compat-matrix.md
// for the auth surfaces, internal/dialect and server/migrations for the
// engines, docs/migration.md for the importer, docs/theme-spec.md and the
// plugin docs for signature-checked packs — so nothing was lost, it just moved
// out of the player-facing front door.
//
// This test is the guard on that boundary: the datasheet does not come back to
// the landing page, and no half-deleted block leaves a bare catalog key on a
// crawlable page.

import (
	"strings"
	"testing"

	"skin-nexus/server/internal/webi18n"
)

func TestLandingCarriesNoSoftwareDatasheet(t *testing.T) {
	// The banned strings are written out rather than looked up in the catalog:
	// the copy is being deleted along with the block, so a catalog lookup would
	// degrade into comparing a key name against itself.
	for _, lang := range []string{webi18n.ZhCN, webi18n.En, ""} {
		html := renderLandingLang(t, lang)
		for _, banned := range []string{
			`<section class="facts">`,
			`class="fact"`,
			"关于本站的事实",
			"Facts about this site",
			"landing.fact",
		} {
			if strings.Contains(html, banned) {
				t.Errorf("the software datasheet is back on the landing page (%q, lang %q)", banned, lang)
			}
		}
	}
}
