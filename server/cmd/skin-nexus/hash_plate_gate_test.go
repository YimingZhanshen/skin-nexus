package main

// hash_plate_gate_test.go: the mono nameplate is the W6 signature — the same
// 12-char short hash must appear on all three surfaces (skinlib card, detail
// page, texture list), each drawn from its platform's shortHash helper and
// styled with the --mono token. The detail page is asserted through the
// template render test; these are the two SPA files plus the shared font rule,
// checked at source level the way the other wiring gates read .vue files.

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

func TestTextureListHashPlate(t *testing.T) {
	src := webAsset(t, "web", "src", "pages", "TexturesCard.vue")
	if !strings.Contains(src, "<code>{{ shortHash(tx.hash) }}</code>") {
		t.Error(`TexturesCard.vue hash cell must be "<code>{{ shortHash(tx.hash) }}</code>"`)
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
