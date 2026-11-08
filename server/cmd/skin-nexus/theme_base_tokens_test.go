package main

import (
	"regexp"
	"strings"
	"testing"
)

// Two invariants the 2026-11-09 unslop pass leaves behind, both of which fail
// quietly (no error, no broken layout — just a screen that looks generated).
//
//  1. Depth must not be painted with brand colour. --glow-* and --shadow* were
//     cinnabar/red-stone halos at .6 alpha, so every hovered button glowed the
//     accent and every card leaked a red smudge onto a #101825 field. On a dark
//     theme separation comes from surface steps, hairlines and a top highlight;
//     a chromatic shadow is the one treatment that always reads as slop.
//  2. --mono has to exist. Six admin surfaces ask the root for a monospace
//     stack for ids, hashes and timestamps; they each carried their own
//     `var(--mono, monospace)` fallback, so the token's absence was invisible
//     and every surface rendered figures in a different face.
func themeBaseDecls(t *testing.T) map[string]string {
	t.Helper()
	decls := map[string]string{}
	for _, m := range regexp.MustCompile(`(?m)^\s*(--[A-Za-z0-9_-]+)\s*:\s*([^;]+);`).FindAllStringSubmatch(string(themeBaseCSS), -1) {
		decls[m[1]] = strings.TrimSpace(m[2])
	}
	if len(decls) < 40 {
		t.Fatalf("only %d token declarations parsed — the parser is blind", len(decls))
	}
	return decls
}

func TestThemeBaseDepthTokensCarryNoBrandChroma(t *testing.T) {
	decls := themeBaseDecls(t)
	// The rgb triples of --accent (#b0453a) and --danger (#d9564c).
	banned := []string{"176, 69, 58", "217, 86, 76"}
	for _, key := range []string{"--glow-cinnabar", "--glow-danger", "--shadow", "--shadow-lift"} {
		v, ok := decls[key]
		if !ok {
			t.Fatalf("%s is no longer defined; packs override it, so it cannot be dropped", key)
		}
		for _, rgb := range banned {
			if strings.Contains(strings.ReplaceAll(v, " ", " "), rgb) {
				t.Errorf("%s paints depth with brand colour (%s): %s", key, rgb, v)
			}
		}
	}
}

func TestThemeBaseDefinesMono(t *testing.T) {
	v, ok := themeBaseDecls(t)["--mono"]
	if !ok || v == "" {
		t.Fatal("--mono undefined: the admin surfaces that set font-family: var(--mono, …) fall back per-file, so ids and hashes stop sharing a face")
	}
	if !strings.Contains(v, "monospace") {
		t.Errorf("--mono must end in a generic family, got %q", v)
	}
}
