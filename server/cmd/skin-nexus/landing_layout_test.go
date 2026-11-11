package main

// landing_layout_test.go — the featured-card caption on the landing page.
//
// The caption is deliberately one truncated line (white-space: nowrap plus
// text-overflow: ellipsis), but ellipsis only engages when the element's own
// width is limited. azure-glass turns .grid figure into a centred flex column,
// and in that layout a nowrap caption's min-content width IS the whole line, so
// the caption sized itself to the text and printed over its neighbours.
//
// Measured in a real browser against the host stylesheet sliced out of
// landing.go plus the shipped azure-glass rules (tmp/azg-overflow-repro.mjs,
// tmp/azg-narrow-probe.mjs):
//
//   - 4-column desktop track: card 222px, caption 379px, 78px past the card edge
//   - 150px narrow track:     card 159px, caption 379px, 110px past the card edge,
//     grid scrollWidth 794px vs 684px laid out
//   - with `max-width: 100%` on the caption: card 159px, caption 130px, no
//     overlap, grid scrollWidth == offsetWidth
//   - with `min-width: 0` on the figure instead: identical 379px caption — the
//     track is a fixed minmax(150px, 1fr) minimum, so nothing was pushing it
//     open. That rule is deliberately NOT part of the fix.
//
// So these assertions read the rendered page for the one declaration that
// carries the fix, and for the title attribute that hands the full name back on
// hover now that the visible line is truncated.

import (
	"regexp"
	"strings"
	"testing"

	"skin-nexus/server/internal/webi18n"
)

var gridCaptionRuleRE = regexp.MustCompile(`(?s)\.grid figcaption\s*\{([^}]*)\}`)

func gridCaptionRule(t *testing.T, html string) string {
	t.Helper()
	m := gridCaptionRuleRE.FindStringSubmatch(html)
	if m == nil {
		t.Fatal(`no ".grid figcaption { … }" rule in the landing stylesheet — the gate went blind`)
	}
	return m[1]
}

func TestLandingFeaturedCaptionIsWidthLimited(t *testing.T) {
	rule := gridCaptionRule(t, renderLandingLang(t, webi18n.ZhCN))
	if !strings.Contains(rule, "white-space: nowrap") {
		t.Error(`caption rule dropped "white-space: nowrap"; the one-line policy changed and this gate needs re-measuring`)
	}
	if !strings.Contains(rule, "max-width: 100%") {
		t.Error(`caption rule has no "max-width: 100%": under a flex-column theme pack a nowrap caption sizes to the whole line and prints over the neighbouring cards (measured 379px in a 222px card)`)
	}
}

func TestLandingFeaturedCaptionCarriesFullName(t *testing.T) {
	html := renderLandingLang(t, webi18n.ZhCN)
	if !strings.Contains(html, `<figcaption title="Big HD">`) {
		t.Error(`the truncated caption does not carry the full name in title, so a reader has no way to recover it`)
	}
}
