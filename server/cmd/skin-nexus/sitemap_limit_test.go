package main

// SitemapIDLimit is exported with a promise written into it: the route and the
// read-only fsck pass "have to speak the same number", because a limit only one
// of them knows is a limit nobody can audit. fsck reads the constant; this pins
// that the route does too, instead of carrying its own copy that can drift the
// day somebody raises the cap and `omitted=` starts describing a page count the
// crawler never sees.

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestSitemapHandlerAsksForTheSharedLimit(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	start := strings.Index(string(src), "func sitemapHandler")
	if start < 0 {
		t.Fatal("no sitemapHandler in main.go — if it was renamed, this gate has to follow it")
	}
	body := string(src[start:])
	// One function only: the next top-level `func ` ends it. Search from index 1
	// because the slice itself opens with the signature.
	if end := strings.Index(body[1:], "\nfunc "); end >= 0 {
		body = body[:end+1]
	}

	if !strings.Contains(body, "PublicTextureIDs") {
		t.Fatal("sitemapHandler no longer reads the texture list; check what fills /sitemap.xml now")
	}
	// Matched inside the call, not anywhere in the function: a comment naming the
	// constant must not be enough to keep this green. `[^\n]*` because the first
	// argument is itself a call, `r.Context()`.
	want := regexp.MustCompile(`PublicTextureIDs\([^\n]*,\s*store\.SitemapIDLimit\s*\)`)
	if !want.MatchString(body) {
		t.Errorf("sitemapHandler does not pass store.SitemapIDLimit to PublicTextureIDs; fsck's `omitted=` and the served file must come from one number:\n%s",
			regexp.MustCompile(`PublicTextureIDs\([^\n]*\)`).FindString(body))
	}
	if lit := regexp.MustCompile(`PublicTextureIDs\([^\n]*,\s*\d+\s*\)`); lit.MatchString(body) {
		t.Errorf("sitemapHandler passes a numeric literal where fsck passes the constant:\n%s", lit.FindString(body))
	}
}
