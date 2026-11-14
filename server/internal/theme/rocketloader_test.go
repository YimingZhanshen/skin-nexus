package theme

import (
	"strings"
	"testing"
)

// Rocket Loader (Cloudflare, in front of the demo/production deployments)
// rewrites every <script> into a deferred type so it executes AFTER first
// paint. The theme's mode script must run before the first paint or the page
// renders in the opposite light/dark mode and flips later (2026-11-15 report).
// The canonical opt-out is data-cfasync="false" on the script tag; hosts
// without Rocket Loader ignore the attribute.
func TestHeadHTMLJsOptsOutOfRocketLoader(t *testing.T) {
	root := t.TempDir()
	man := `{"id":"dark","name":"Dark","version":"1.0.0","inject":{"css":["style.css"],"js":["theme.js"]}}`
	writePack(t, root, "dark", man, map[string]string{"style.css": "body{}", "theme.js": "//x"})
	r, err := NewRegistry(root, "0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	th, err := r.Get("dark")
	if err != nil {
		t.Fatal(err)
	}
	h := th.HeadHTML()
	if !strings.Contains(h, `data-cfasync="false"`) {
		t.Fatalf("theme JS tag must carry data-cfasync=\"false\", head = %q", h)
	}
	// the attribute belongs on the script tag, not the stylesheet link
	for _, line := range strings.Split(h, "\n") {
		if strings.Contains(line, "<script") && !strings.Contains(line, `data-cfasync="false"`) {
			t.Fatalf("script tag without Rocket Loader opt-out: %q", line)
		}
	}
}
