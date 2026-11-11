package webi18n

import (
	"strings"
	"testing"
	"unicode"
)

func allLetters(lang string, p MailPalette) []string {
	var out []string
	for _, ttl := range []int{30, 24 * 60} {
		s, tx, h := ResetMail(lang, "Rig", "http://rig.test/reset-password?token=x", ttl, p)
		out = append(out, s, tx, h)
		s, tx, h = VerifyMail(lang, "Rig", "http://rig.test/email-verify?token=x", ttl, p)
		out = append(out, s, tx, h)
	}
	s, tx, h := TestMail(lang, TestMailConfig{SiteName: "Rig", Host: "smtp.rig.test", Port: 465, Sender: "site@rig.test", Lang: lang, ThemeID: "azure"}, p)
	return append(out, s, tx, h)
}

func TestMailCatalogParityAndPlaceholders(t *testing.T) {
	for _, lang := range []string{ZhCN, En} {
		for _, s := range allLetters(lang, DefaultMailPalette()) {
			if strings.Contains(s, "{") {
				t.Errorf("%s: unfilled placeholder in %q", lang, s)
			}
		}
	}
	for _, s := range allLetters(En, DefaultMailPalette()) {
		for _, r := range s {
			if unicode.Is(unicode.Han, r) {
				t.Errorf("en letter carries Han character %q in %q", r, s)
				break
			}
		}
	}
	// Every mail.* catalog key must be asked for by this package (the locale
	// gate's dead-copy rule); a key nobody renders is a translation nobody sees.
	for _, k := range Keys() {
		if !strings.HasPrefix(k, "mail.") {
			continue
		}
		if T(ZhCN, k) == k || T(En, k) == k {
			t.Errorf("catalog key %s missing in one language", k)
		}
	}
}

func TestMailHTMLHasNoRemoteResources(t *testing.T) {
	for _, lang := range []string{ZhCN, En} {
		for _, s := range allLetters(lang, DefaultMailPalette()) {
			if !strings.Contains(s, "<table") {
				continue // plain-text parts carry no markup at all
			}
			for _, bad := range []string{"src=", "url(", "@import", "<link", "<img", "<script", "@font-face"} {
				if strings.Contains(s, bad) {
					t.Errorf("%s: html carries remote/executable resource %q", lang, bad)
				}
			}
		}
	}
}

func TestMailHTMLIsTableAndBgcolor(t *testing.T) {
	_, _, h := VerifyMail(ZhCN, "Rig", "http://rig.test/email-verify?token=x", 1440, DefaultMailPalette())
	for _, want := range []string{"<table", `bgcolor="`, `role="presentation"`} {
		if !strings.Contains(h, want) {
			t.Errorf("html missing %q (Outlook's Word engine needs table+bgcolor)", want)
		}
	}
	for _, bad := range []string{"<html", "<!doctype", "<style"} {
		if strings.Contains(strings.ToLower(h), bad) {
			t.Errorf("html must not carry %q: clients strip it and the locale gate scans it", bad)
		}
	}
}

func TestMailBodyColorsAreConstant(t *testing.T) {
	_, _, a := VerifyMail(ZhCN, "Rig", "http://x.test/v", 60, DefaultMailPalette())
	_, _, b := VerifyMail(ZhCN, "Rig", "http://x.test/v", 60, PaletteFromTokens(map[string]string{"--accent": "#00ff00"}))
	for _, constant := range []string{`bgcolor="#f7f5f2"`, "color:#1f2733", "color:#5a6472"} {
		if !strings.Contains(a, constant) || !strings.Contains(b, constant) {
			t.Errorf("reading surface must stay constant light paper, missing %q", constant)
		}
	}
	if !strings.Contains(b, `bgcolor="#00ff00"`) {
		t.Error("the accent band must follow the theme")
	}
	if strings.Contains(a, `bgcolor="#00ff00"`) {
		t.Error("default palette leaked the test accent")
	}
}

func TestMailSnapshotIsStable(t *testing.T) {
	p := DefaultMailPalette()
	s1, t1, h1 := ResetMail(ZhCN, "Rig", "http://x.test/r", 30, p)
	s2, t2, h2 := ResetMail(ZhCN, "Rig", "http://x.test/r", 30, p)
	if s1 != s2 || t1 != t2 || h1 != h2 {
		t.Error("a letter is a snapshot: same input must build byte-identical output (no clock, no randomness in the copy)")
	}
}

func TestPaletteContrastGuard(t *testing.T) {
	cases := []struct{ accent, wantInk string }{
		{"#b0453a", mailInkLight},
		{"#000000", mailInkLight},
		{"#f5d76e", mailInk},
		{"#ffffff", mailInk},
	}
	for _, c := range cases {
		if got := PaletteFromTokens(map[string]string{"--accent": c.accent}); got.ButtonInk != c.wantInk {
			t.Errorf("accent %s: button ink = %s, want %s", c.accent, got.ButtonInk, c.wantInk)
		}
	}
	if got := PaletteFromTokens(map[string]string{"--accent": "oklch(0.7 0.1 20)"}); got != DefaultMailPalette() {
		t.Errorf("unusable accent must fall back to the default palette, got %+v", got)
	}
	if got := PaletteFromTokens(nil); got != DefaultMailPalette() {
		t.Errorf("nil tokens must fall back to the default palette, got %+v", got)
	}
}
