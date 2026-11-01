package main

import (
	"path/filepath"
	"testing"
)

// TestStaticPathIn pins both halves of the containment rule: a real file inside
// the SPA directory resolves (including through a relative dir, which is how it
// is configured on every deployment), and anything that climbs out does not.
func TestStaticPathIn(t *testing.T) {
	cases := []struct {
		dir, want string
		urlPath   string
	}{
		{"web/dist", filepath.Join("web", "dist", "assets", "main.js"), "assets/main.js"},
		{"./web/dist", filepath.Join("web", "dist", "index.html"), "index.html"},
		{filepath.Join("web", "dist") + "/", filepath.Join("web", "dist", "favicon.ico"), "favicon.ico"},
		{"web/dist", "", "../server/internal/config/config.go"},
		// A leading slash is folded by Join into a path still inside dir,
		// which is the containment we want: it can never reach /etc/passwd.
		{"web/dist", filepath.Join("web", "dist", "etc", "passwd"), "/etc/passwd"},
	}
	for _, c := range cases {
		if got := staticPathIn(c.dir, c.urlPath); got != c.want {
			t.Errorf("staticPathIn(%q, %q) = %q, want %q", c.dir, c.urlPath, got, c.want)
		}
	}
}
