package main

import (
	"strings"
	"testing"
)

func TestInstallTokenHonoursOperatorValue(t *testing.T) {
	got, generated := installToken("  hunter2 ")
	if got != "hunter2" || generated {
		t.Fatalf("installToken(env value) = %q, generated %v; want the operator value, not generated", got, generated)
	}
	if got, generated := installToken(""); !generated || got == "" {
		t.Fatalf("an unset token must generate one, got %q (generated %v)", got, generated)
	}
}

func TestInstallTokenShape(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		tok, generated := installToken("")
		if !generated || len(tok) != 10 {
			t.Fatalf("token = %q (len %d), want 10 generated characters", tok, len(tok))
		}
		if strings.ContainsAny(tok, "0O1lI") {
			t.Fatalf("token %q contains a glyph people misread out of a terminal", tok)
		}
		if seen[tok] {
			t.Fatalf("token %q repeated across 200 draws", tok)
		}
		seen[tok] = true
	}
}
