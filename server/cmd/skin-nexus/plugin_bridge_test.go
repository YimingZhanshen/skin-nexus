package main

// The plugin bridge is a postMessage channel into a sandboxed iframe, and two of
// its properties are invisible to every other check in this repository:
//
//   - delivery: a frame sandboxed without `allow-same-origin` has an *opaque*
//     origin, so a message addressed to our own origin is dropped by the browser
//     instead of delivered. Host code then looks healthy (it still fetches the
//     CSRF token, still builds the payload) while the plugin page never receives
//     anything — which is exactly how the theme, the locale and every plugin API
//     call disappeared from /ext/* pages.
//   - confidentiality: the only targetOrigin that reaches such a frame is "*",
//     so nothing that leaves through it may be a credential.
//
// Both are asserted against the source because the failure mode is silent: a
// browser-behaviour rule cannot be caught by a Go test that does not run a page,
// but it can be caught by refusing the two edits that would reintroduce it.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func pluginFrameSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "src", "components", "plugin", "PluginFrame.vue"))
	if err != nil {
		t.Fatalf("read PluginFrame.vue: %v", err)
	}
	return string(b)
}

// topLevelFunc cuts a `<script setup>` function: they all start at column zero,
// so the next "\n}" closes it.
func topLevelFunc(t *testing.T, src, signature string) string {
	t.Helper()
	i := strings.Index(src, signature)
	if i < 0 {
		t.Fatalf("%s is gone from PluginFrame.vue — if the bridge moved, this gate has to follow it", signature)
	}
	rest := src[i:]
	if j := strings.Index(rest, "\n}"); j >= 0 {
		return rest[:j+2]
	}
	return rest
}

func TestPluginBridgeDeliversToAnOpaqueFrameWithoutLeavingCredentials(t *testing.T) {
	src := pluginFrameSource(t)

	send := topLevelFunc(t, src, "function send(")
	if !regexp.MustCompile(`postMessage\([\s\S]*?,\s*'\*'\s*\)`).MatchString(send) {
		t.Errorf("host→frame messages must target \"*\": a sandbox=\"allow-scripts allow-forms\" frame has an opaque origin, "+
			"and any other targetOrigin makes the browser drop the message silently.\n%s", send)
	}
	if strings.Contains(send, "window.location.origin)") {
		t.Error("host→frame messages are addressed to our own origin again — that is the silent-breakage variant")
	}

	if !strings.Contains(src, `sandbox="allow-scripts allow-forms"`) {
		t.Error("the plugin frame's sandbox changed; if allow-same-origin was added, plugin UI code can now read site cookies — " +
			"that is a privilege escalation, not a theming fix, and it must be argued for explicitly")
	}

	init := topLevelFunc(t, src, "async function init(")
	if !strings.Contains(init, `send('init'`) {
		t.Fatal("init() no longer sends the handshake payload; every plugin page is deaf and this test cannot see why")
	}
	if regexp.MustCompile(`\bcsrf\b`).MatchString(init) {
		t.Errorf("the init payload carries a CSRF token, and the only targetOrigin that reaches a sandboxed frame is \"*\" — "+
			"the host attaches X-CSRF-Token itself in request(), so the frame never needed to hold it.\n%s", init)
	}
	if !strings.Contains(init, "theme:") {
		t.Error("init no longer sends theme tokens; plugin UIs fall back to their own palette and stop following the active theme")
	}
}

// `navigate` is the one bridge message that moves the whole page, so its filter
// is a redirect target in its own right. Prefix rules on the raw string are the
// shape that leaks: "//host" and "/\host" both leave the site while looking like
// a path, and a browser resolves them before anything can compare origins.
func TestPluginNavigateStaysOnThisOrigin(t *testing.T) {
	src := pluginFrameSource(t)
	i := strings.Index(src, "case 'navigate':")
	if i < 0 {
		t.Fatal("the navigate case is gone; if the bridge moved, this gate has to follow it")
	}
	body := src[i:]
	if j := strings.Index(body, "break"); j >= 0 {
		body = body[:j]
	}
	if !strings.Contains(body, "new URL(") {
		t.Errorf("navigate does not resolve the target before judging it:\n%s", body)
	}
	if !strings.Contains(body, ".origin === location.origin") {
		t.Errorf("navigate does not compare the resolved origin against this site's:\n%s", body)
	}
	if strings.Contains(body, "startsWith('/')") {
		t.Error("navigate is back to a string-prefix guard, which admits /\\evil.com")
	}
}

// `request` sends a plugin page's calls under /api/plugins/<name>/, and the raw
// path is the only part the plugin controls. Guarding the raw string is the
// shape that leaks: URL parsing folds ".." segments (and turns backslashes into
// slashes) in the *resolved* path, so "/api/plugins/foo/../../admin/v1/users"
// arrives at /api/admin/v1/users carrying the user's session — the server never
// sees a plugin prefix to defend. The judgment has to be made on the parsed
// URL's pathname, after folding, against the plugin's own base.
func TestPluginRequestStaysUnderThePluginPrefix(t *testing.T) {
	src := pluginFrameSource(t)
	fn := topLevelFunc(t, src, "async function request(")
	if !strings.Contains(fn, "new URL(") {
		t.Errorf("request() does not resolve the plugin path before judging it, so \"..\" escapes /api/plugins/<name>/ and any same-source API becomes callable as the user:\n%s", fn)
	}
	if !strings.Contains(fn, "pathname") || !strings.Contains(fn, "startsWith(") {
		t.Errorf("request() does not assert the resolved pathname against the plugin prefix:\n%s", fn)
	}
	if !strings.Contains(fn, "status: 400") {
		t.Errorf("request() has no refusal branch; an escaped path must be answered with 400, not fetched:\n%s", fn)
	}
}
