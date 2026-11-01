package siteurl

import (
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestNoSiteBuildsItsOwnAbsoluteURL walks the sources for a component that
// concatenates a base URL with a texture/preview/avatar path instead of asking
// this package.
//
// The bug class is real and specific: Yggdrasil signs the base64 `textures`
// payload, so a second builder that formats the path slightly differently — or
// reads a base captured before the `site_url` option was applied — produces a
// URL whose signature is valid for bytes the client never fetches.
func TestNoSiteBuildsItsOwnAbsoluteURL(t *testing.T) {
	// A texture path on its own is fine — same-origin links stay relative. What
	// must not happen is a *base* being concatenated by hand anywhere but here.
	pathLiterals := []string{`"/textures/"`, `"/preview/"`, `"/avatar/hash/"`, `"/avatar/user/"`}
	baseTokens := []string{"Get()", "BaseURL", "baseURL", "base +", "base)", "%s/textures", "%s/preview", "%s/avatar"}
	root := filepath.Join("..", "..")
	nl := "\n"
	hits := []string{}
	scanned := 0 // a walk that sees no files must not read as "no violations"
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		slash := filepath.ToSlash(path)
		rel := strings.TrimPrefix(slash, filepath.ToSlash(root)+"/")
		if d.IsDir() {
			switch rel {
			case "internal/siteurl":
				return filepath.SkipDir // this package owns the builders
			case "web", "docs":
				return filepath.SkipDir // no Go of ours down there
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		scanned++
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(raw), nl) {
			c := strings.TrimSpace(line)
			if strings.HasPrefix(c, "//") {
				continue
			}
			if !anyContains(c, pathLiterals) || !anyContains(c, baseTokens) {
				continue
			}
			hits = append(hits, rel+":"+strconv.Itoa(i+1)+" "+c)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if scanned < 100 {
		t.Fatalf("walk inspected only %d Go files from %s — it is not looking at the sources", scanned, root)
	}
	if len(hits) > 0 {
		t.Errorf("these lines concatenate a base with a texture path; use siteurl.Texture/Preview/Avatar:\n  %s",
			strings.Join(hits, nl+"  "))
	}
}

func anyContains(s string, parts []string) bool {
	for _, p := range parts {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}
