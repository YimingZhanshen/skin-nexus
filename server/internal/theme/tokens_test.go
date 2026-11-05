package theme

import (
	"strings"
	"testing"
)

// The plugin UI injection must carry a theme's tokens and nothing else. A full
// theme stylesheet is written for the host's DOM, so its bare element rules
// (body{background-image}, h1,h2{font-family}, input{font-size}, table{})
// would restyle a plugin page's own markup — the tokens are the only part a
// plugin document is supposed to inherit.
func TestRootTokensKeepCustomPropertiesAndDropElementRules(t *testing.T) {
	css := `
:root {
  --bg: #f7f5f0;
  --accent: #601986;
  color-scheme: light;
}
html { overflow-x: clip; scrollbar-color: #cfc3d4 transparent; }
body { background-color: var(--bg); background-image: radial-gradient(900px 480px at 84% -8%, rgba(178,106,222,.13), transparent 64%); }
h1, h2 { font-family: "MC Pixel CJK", Consolas; font-weight: 400; }
input, select, textarea { font-size: 13px; }
`
	got := ExtractRootTokens(css)

	for _, want := range []string{"--bg: #f7f5f0", "--accent: #601986", "color-scheme: light"} {
		if !strings.Contains(got, want) {
			t.Errorf("token %q missing from:\n%s", want, got)
		}
	}
	for _, bad := range []string{"background-image", "radial-gradient", "overflow-x", "font-family", "MC Pixel CJK", "scrollbar-color", "font-size", "body", "h1", "input"} {
		if strings.Contains(got, bad) {
			t.Errorf("leaked host-only rule fragment %q into:\n%s", bad, got)
		}
	}
}

// Bundled themes switch their palette inside @media (prefers-color-scheme: …)
// blocks. Dropping the wrapper would hand a plugin document one palette for
// both modes, which is a different bug than the one the injection fixes.
func TestRootTokensKeepMediaWrappedRootBlocks(t *testing.T) {
	css := `
:root { --bg: #14101c; color-scheme: dark; }
@media (prefers-color-scheme: light) {
  :root { --bg: #f7f5f0; color-scheme: light; }
  body { background-image: radial-gradient(#fff, transparent); }
  input { font-size: 16px; }
}
@font-face { font-family: "MC Pixel CJK"; src: url("font.woff2"); }
@keyframes spin { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }
`
	got := ExtractRootTokens(css)
	for _, want := range []string{"@media (prefers-color-scheme: light)", "--bg: #14101c", "--bg: #f7f5f0"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, bad := range []string{"radial-gradient", "font-size", "@font-face", "MC Pixel CJK", "@keyframes", "rotate("} {
		if strings.Contains(got, bad) {
			t.Errorf("leaked %q into:\n%s", bad, got)
		}
	}
}

// The shape the bundled themes actually have: a comment above the block and a
// comment after every token. Comments are not part of a selector or a property
// name, so they must not decide whether a token survives — when they did,
// wzbc-violet's whole palette block was dropped and the plugin page kept its
// own dark defaults while the map silently fell back.
func TestRootTokensIgnoreCommentsAroundSelectorsAndDeclarations(t *testing.T) {
	css := `
/* 紫域令牌 */
:root {
  --bg: #f7f5f0;          /* 纸感暖白：页底 */
  --panel: #ffffff;       /* 面板纯白 */
  --accent: #601986;      /* 主紫 */
}
`
	got := ExtractRootTokens(css)
	for _, want := range []string{"--bg: #f7f5f0", "--panel: #ffffff", "--accent: #601986"} {
		if !strings.Contains(got, want) {
			t.Errorf("token %q lost to a comment, got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "/*") || strings.Contains(got, "纸感") {
		t.Errorf("comment text survived into:\n%s", got)
	}
}
