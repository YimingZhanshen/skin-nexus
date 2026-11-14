package main

// hash_plate_gate_test.go: the mono nameplate is the W6 signature — the same
// 12-char short hash appears on the skinlib card and the detail page, each
// drawn from its platform's shortHash helper and styled with the --mono token.
// The detail page is asserted through the template render test; the SPA files
// are checked at source level the way the other wiring gates read .vue files.
//
// 2026-11-14 拍板「只收列表面」: player LIST surfaces (the my-textures table,
// the closet table, the wear picker rows) must NOT carry a fingerprint column —
// normal players do not need it. That absence is pinned here as firmly as the
// presence: the name cell's title tooltip may still hold the full hash for
// support, but no visible hash column returns to a player list.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func webAsset(t *testing.T, rel ...string) string {
	t.Helper()
	p := append([]string{"..", "..", ".."}, rel...)
	b, err := os.ReadFile(filepath.Join(p...))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSkinlibCardHashPlate(t *testing.T) {
	src := webAsset(t, "web", "src", "pages", "SkinlibPage.vue")
	for _, want := range []string{
		"import { shortHash } from '../textureLabel.js'",
		"shortHash(it.hash)",
		".hash-plate {",
		"var(--mono",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("SkinlibPage.vue missing %q", want)
		}
	}
}

// Player list surfaces carry no visible fingerprint (拍板 2026-11-14). The
// name cell keeps the full hash in its title tooltip — that is a fallback for
// support, not a column.
func TestPlayerListsCarryNoFingerprint(t *testing.T) {
	tex := webAsset(t, "web", "src", "pages", "TexturesCard.vue")
	if strings.Contains(tex, `shortHash(tx.hash)`) {
		t.Error("TexturesCard.vue shows a hash cell again — the player list must not carry a fingerprint column")
	}
	if strings.Contains(tex, "dashboard.colHash") {
		t.Error("TexturesCard.vue references dashboard.colHash — the column is gone, the key belongs to admin only")
	}
	if !strings.Contains(tex, `:title="tx.description || tx.hash"`) {
		t.Error("TexturesCard.vue name cell lost its hash title fallback — orphan rows still need an identifier")
	}
	closet := webAsset(t, "web", "src", "pages", "ClosetPage.vue")
	if strings.Contains(closet, "closet.colHash") {
		t.Error("ClosetPage.vue shows a hash column again — player closet carries no fingerprint")
	}
	if !strings.Contains(closet, "shortHash(c.hash)") {
		t.Error("ClosetPage.vue lost the orphan-name fallback — legacy rows without a name must stay identifiable")
	}
}

// code elements follow the theme's mono stack; the UA default monospace does
// not track theme packs, which is how the list cells drifted apart.
func TestCodeUsesMonoToken(t *testing.T) {
	src := webAsset(t, "web", "src", "style.css")
	if !strings.Contains(src, "code, pre { font-family: var(--mono") {
		t.Error("style.css must route code/pre through var(--mono ...)")
	}
}
