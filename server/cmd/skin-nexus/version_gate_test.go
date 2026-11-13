package main

// version_gate_test.go — the release version is written down in nine places,
// and they are allowed to disagree only until somebody notices.
//
// The constant in this package is what the binary reports (ldflags overwrite
// it with BUILD_VERSION), the Dockerfile default is what an image built
// without CI says, the workflow's build-arg is what a release image is
// tagged with, the two package.json copies are what npm and the lockfile
// claim, CITATION.cff is what a citation exports, and the two READMEs plus
// the bug-report template are what a human reads first. A release that flips
// seven of nine leaves the badge, the image tag and the binary reporting
// three different versions of the same build.
//
// So: every source must repeat the constant in this package exactly. The
// lockfile is never edited by hand (npm install rewrites it) — the gate
// reads it, and a mismatch there means the npm side was not regenerated.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// repoRoot resolves the checkout root from this file's own path, so the gate
// works whether the test is run from server/ or from the repository root.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate version_gate_test.go")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
}

func readRepoFile(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// jsonVersion returns the "version" field of a package manifest, or of the
// root package entry inside a lockfile (which also carries one version per
// dependency, none of which is ours).
func jsonVersion(t *testing.T, body, rel string) []string {
	t.Helper()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("%s is not JSON: %v", rel, err)
	}
	var out []string
	collect := func(raw json.RawMessage, label string) {
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatalf("%s %s is not a string version: %v", rel, label, err)
		}
		out = append(out, v)
	}
	if raw, ok := doc["version"]; ok {
		collect(raw, "version")
	}
	if raw, ok := doc["packages"]; ok {
		var pkgs map[string]json.RawMessage
		if err := json.Unmarshal(raw, &pkgs); err != nil {
			t.Fatalf("%s packages is not an object: %v", rel, err)
		}
		if root, ok := pkgs[""]; ok {
			var entry map[string]json.RawMessage
			if err := json.Unmarshal(root, &entry); err != nil {
				t.Fatalf("%s packages[\"\"] is not an object: %v", rel, err)
			}
			if v, ok := entry["version"]; ok {
				collect(v, "packages[\"\"].version")
			}
		}
	}
	if len(out) == 0 {
		t.Fatalf("%s carries no version field we recognise", rel)
	}
	return out
}

func TestVersionLiteralsAgree(t *testing.T) {
	root := repoRoot(t)
	want := version // the constant this binary reports

	lineSources := []struct {
		rel     string
		pattern string
		min     int
	}{
		{"Dockerfile", `ARG BUILD_VERSION=([0-9]+\.[0-9]+\.[0-9]+)`, 1},
		{".github/workflows/ci.yml", `BUILD_VERSION=([0-9]+\.[0-9]+\.[0-9]+)`, 1},
		{"CITATION.cff", `(?m)^version:\s*"([0-9]+\.[0-9]+\.[0-9]+)"`, 1},
		{"README.md", `v([0-9]+\.[0-9]+\.[0-9]+)`, 2},    // badge + status line
		{"README.en.md", `v([0-9]+\.[0-9]+\.[0-9]+)`, 2}, // badge + status line
		{".github/ISSUE_TEMPLATE/bug_report.yml", `v([0-9]+\.[0-9]+\.[0-9]+)`, 1},
	}

	for _, s := range lineSources {
		body := readRepoFile(t, root, s.rel)
		re := regexp.MustCompile(s.pattern)
		matches := re.FindAllStringSubmatch(body, -1)
		if len(matches) < s.min {
			t.Errorf("%s: found %d version literal(s), want at least %d (pattern %s)", s.rel, len(matches), s.min, s.pattern)
			continue
		}
		for _, m := range matches {
			if m[1] != want {
				t.Errorf("%s: version %q, want %q — the release flipped some sources and not others", s.rel, m[1], want)
			}
		}
	}

	jsonSources := []string{"web/package.json", "web/package-lock.json"}
	for _, rel := range jsonSources {
		for _, got := range jsonVersion(t, readRepoFile(t, root, rel), rel) {
			if got != want {
				t.Errorf("%s: version %q, want %q — regenerate the lockfile with npm, never hand-edit it", rel, got, want)
			}
		}
	}
}

// The gate must fail when a source drifts, not merely pass while everything
// happens to be equal: assert the scanner actually sees the literals it is
// supposed to compare, in every file it claims to cover.
func TestVersionGateSeesEverySource(t *testing.T) {
	root := repoRoot(t)
	seen := map[string]int{}

	re := regexp.MustCompile(`v?([0-9]+\.[0-9]+\.[0-9]+)`)
	for _, rel := range []string{"Dockerfile", ".github/workflows/ci.yml", "CITATION.cff", "README.md", "README.en.md", ".github/ISSUE_TEMPLATE/bug_report.yml"} {
		body := readRepoFile(t, root, rel)
		n := 0
		for _, m := range re.FindAllStringSubmatch(body, -1) {
			if m[1] == version {
				n++
			}
		}
		seen[rel] = n
		if n == 0 {
			t.Errorf("%s: the current version %q appears nowhere — the scanner pattern is stale, not the file", rel, version)
		}
	}
	for _, rel := range []string{"web/package.json", "web/package-lock.json"} {
		got := jsonVersion(t, readRepoFile(t, root, rel), rel)
		n := 0
		for _, v := range got {
			if v == version {
				n++
			}
		}
		if n == 0 {
			t.Errorf("%s: the current version %q appears nowhere", rel, version)
		}
	}
	if strings.TrimSpace(version) == "" {
		t.Fatal("the version constant is empty; nothing can be compared against it")
	}
}
