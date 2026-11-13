package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The verification link is single-use, and mail gateways (exmail, Office365
// SafeLinks) plus WeChat/QQ built-in browsers prefetch URLs before a human
// clicks. Consuming the token on page load therefore burns it for the player:
// they open the mail and are told the link is invalid. The landing page must
// only SHOW a button; the POST happens on click. Nothing but this test pins
// that, because the web app has no test runner — same source-reading discipline
// as spa_plugin_test.go.
func TestEmailVerifyLandingDoesNotConsumeOnLoad(t *testing.T) {
	script := appVueScript(t)
	for _, name := range []string{"applyInitialRoute", "onPopState"} {
		body := funcBody(script, name)
		if len(body) < 40 {
			t.Fatalf("%s body not extracted (%d bytes) — the gate would be vacuous", name, len(body))
		}
		if strings.Contains(body, "doEmailVerify(") {
			t.Errorf("%s consumes the verification token on route entry — a prefetching mail client burns it before the player clicks", name)
		}
		if !strings.Contains(body, "prepareEmailVerify(") {
			t.Errorf("%s must hand the landing to prepareEmailVerify (read the URL, zero network)", name)
		}
	}
	prep := funcBody(script, "prepareEmailVerify")
	if !strings.Contains(prep, "URLSearchParams") {
		t.Error("prepareEmailVerify must read the token off the URL")
	}
	if strings.Contains(prep, "api.") {
		t.Error("prepareEmailVerify must not touch the network — landing is display-only")
	}
}

func TestEmailVerifyConfirmIsClickWired(t *testing.T) {
	// The verify card owns its markup since W4-1; the assertion is unchanged,
	// only the file it reads moved with the code it describes.
	card, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "src", "components", "auth", "VerifyCard.vue"))
	if err != nil {
		t.Fatalf("read VerifyCard.vue: %v", err)
	}
	tpl := string(card)
	if !strings.Contains(tpl, `@click="doEmailVerify"`) {
		t.Error("the ready branch has no button wired to doEmailVerify — removing auto-consume without wiring the click leaves verification unreachable")
	}
	body := funcBody(appVueScript(t), "doEmailVerify")
	if !strings.Contains(body, "api.confirmEmailVerification(") {
		t.Error("doEmailVerify must POST the confirmation")
	}
	if !strings.Contains(body, "verifyState.value === 'working' || verifyState.value === 'done'") {
		t.Error("doEmailVerify must ignore re-entry while working/done (double click = second POST on a spent token)")
	}
}

// The extractor reads a .vue file by brace matching; a style change could
// silently shrink every body to nothing and turn the two gates above green on
// empty strings. Pinned against an inline fixture, not the real file.
func TestEmailVerifyFuncExtractorIsNotVacuous(t *testing.T) {
	fixture := `function outer(a) {
  if (a) { return { x: 1 } }
  const s = '}'
  return s
}
function missingNever() {}
`
	body := funcBody(fixture, "outer")
	if !strings.Contains(body, "return { x: 1 }") || !strings.Contains(body, "const s = '}'") {
		t.Fatalf("brace matching lost nested braces or quoted braces: %q", body)
	}
	if funcBody(fixture, "noSuchFunction") != "" {
		t.Error("a missing function must extract to the empty string")
	}
	if len(funcBody(appVueScript(t), "applyInitialRoute")) < 200 {
		t.Error("applyInitialRoute extracted suspiciously small — extractor blind on the real file")
	}
}

func appVueSource(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "src", "App.vue"))
	if err != nil {
		t.Fatalf("read App.vue: %v", err)
	}
	return string(src)
}

func appVueScript(t *testing.T) string {
	t.Helper()
	src := appVueSource(t)
	start := strings.Index(src, "<script setup>")
	end := strings.Index(src, "</script>")
	if start < 0 || end < 0 || end < start {
		t.Fatal("App.vue script block not found")
	}
	return src[start:end]
}

// funcBody returns the brace-balanced body of `function name(...)` including
// the outer braces. Quoted braces do not count. Missing function => "".
func funcBody(src, name string) string {
	i := strings.Index(src, "function "+name+"(")
	if i < 0 {
		return ""
	}
	open := strings.Index(src[i:], "{")
	if open < 0 {
		return ""
	}
	start := i + open
	depth := 0
	var quote byte
	for j := start; j < len(src); j++ {
		c := src[j]
		if quote != 0 {
			if c == '\\' {
				j++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		// Comments are prose, not code: an apostrophe in one ("AdminPanel's
		// own popstate listener") opens a quote that then swallows everything
		// below it, so whether the body ever closes used to depend on text two
		// hundred lines away — a refactor elsewhere turned this extractor red
		// on a body it never looked at. Skip both comment forms.
		if c == '/' && j+1 < len(src) && src[j+1] == '/' {
			for j < len(src) && src[j] != '\n' {
				j++
			}
			continue
		}
		if c == '/' && j+1 < len(src) && src[j+1] == '*' {
			j += 2
			for j+1 < len(src) && !(src[j] == '*' && src[j+1] == '/') {
				j++
			}
			j++
			continue
		}
		switch c {
		case '\'', '"', '`':
			quote = c
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[start : j+1]
			}
		}
	}
	return ""
}
