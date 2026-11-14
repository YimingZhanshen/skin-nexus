package main

// landing_hero_test.go — W6-2c hero composition.
//
// Three assertions, one per unslop finding the W6 direction adopted:
//   C1: the tagline must be the claim only SkinNexus holds (self-hosted, data on
//       the operator's machine), not a sentence any skin site could print.
//   L1: the hero carries one primary button and one quiet text link, not two
//       same-weight bordered buttons.
//   Moments②: the featured skins sit on the first screen (before the stats),
//       over the fixed scene — not below the fold.

import (
	"regexp"
	"strings"
	"testing"

	"skin-nexus/server/internal/webi18n"
)

func TestLandingTaglineIsSpecific(t *testing.T) {
	zh := renderLandingLang(t, webi18n.ZhCN)
	if !strings.Contains(zh, "自托管的 Minecraft 皮肤站") {
		t.Error("zh tagline must lead with the self-hosted claim")
	}
	if strings.Contains(zh, "Minecraft 皮肤站，支持 Yggdrasil 外置登录") {
		t.Error("zh tagline is still the generic any-site sentence")
	}
	en := renderLandingLang(t, webi18n.En)
	if !strings.Contains(en, "A self-hosted Minecraft skin site") {
		t.Error("en tagline must lead with the self-hosted claim")
	}
	if strings.Contains(en, "A Minecraft skin site with Yggdrasil external login") {
		t.Error("en tagline is still the generic any-site sentence")
	}
}

var ctaAltRuleRE = regexp.MustCompile(`(?s)\.cta \.alt\s*\{([^}]*)\}`)
var ctaAltHoverRE = regexp.MustCompile(`(?s)\.cta \.alt:hover\s*\{([^}]*)\}`)

func TestLandingCtaOnePrimaryOneQuietLink(t *testing.T) {
	html := renderLandingLang(t, webi18n.ZhCN)
	alt := ctaAltRuleRE.FindStringSubmatch(html)
	if alt == nil {
		t.Fatal("no .cta .alt rule — gate went blind")
	}
	if strings.Contains(alt[1], "border") {
		t.Error(".cta .alt still carries a border — it must be a quiet text link, not a second button")
	}
	if !strings.Contains(alt[1], "var(--jade)") {
		t.Error(".cta .alt must use the link color (jade)")
	}
	hover := ctaAltHoverRE.FindStringSubmatch(html)
	if hover == nil || !strings.Contains(hover[1], "text-decoration: underline") {
		t.Error(".cta .alt:hover must underline like a link")
	}
	if !strings.Contains(html, ".cta .primary { background: var(--cinnabar)") {
		t.Error("the primary button lost its cinnabar fill")
	}
}

func TestLandingFeaturedOnFirstScreen(t *testing.T) {
	html := renderLandingLang(t, webi18n.ZhCN)
	feat := strings.Index(html, `class="feat"`)
	stats := strings.Index(html, `class="stats"`)
	if feat < 0 {
		t.Fatal("featured section missing from the render")
	}
	if stats < 0 {
		t.Fatal("stats section missing from the render")
	}
	if feat > stats {
		t.Error("featured skins must come before the stats so they land on the first screen over the scene")
	}
}
