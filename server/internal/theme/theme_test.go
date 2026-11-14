package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePack(t *testing.T, root, id, manifest string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if manifest != "" {
		if err := os.WriteFile(filepath.Join(dir, "theme.json"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const okManifest = `{"id":"dark","name":"Dark","version":"1.0.0","inject":{"css":["style.css"]}}`

func TestRegistryScanAndValidate(t *testing.T) {
	root := t.TempDir()
	writePack(t, root, "dark", okManifest, map[string]string{"style.css": ":root{--bg:#000}"})
	// bad: manifest id mismatch -> ignored
	writePack(t, root, "other", `{"id":"not-other","name":"X","version":"1","inject":{}}`, nil)
	// bad: invalid directory name -> ignored
	writePack(t, root, "Bad Name!", okManifest, nil)
	// bad: no manifest -> ignored
	writePack(t, root, "empty", "", nil)

	r, err := NewRegistry(root, "0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if list := r.List(); len(list) != 1 || list[0].ID != "dark" {
		t.Fatalf("list = %+v", list)
	}
}

func TestResolveAssetRejectsEscapes(t *testing.T) {
	root := t.TempDir()
	writePack(t, root, "dark", okManifest, map[string]string{
		"style.css":   "body{}",
		"preview.png": "png",
	})
	r, err := NewRegistry(root, "0.1.0")
	if err != nil {
		t.Fatal(err)
	}

	// legit
	if _, err := r.ResolveAsset("dark", "preview.png"); err != nil {
		t.Fatalf("legit asset: %v", err)
	}
	// traversal / bad names / unknown pack
	for _, bad := range []struct{ id, file string }{
		{"dark", "../secret"},
		{"dark", ".."},
		{"dark", "/etc/passwd"},
		{"dark", "sub/dir.png"},
		{"../etc", "passwd"},
		{"", "style.css"},
		{"dark", ""},
		{"nope", "style.css"},
	} {
		if _, err := r.ResolveAsset(bad.id, bad.file); err == nil {
			t.Fatalf("escape accepted: %+v", bad)
		}
	}
}

func TestHeadHTML(t *testing.T) {
	root := t.TempDir()
	writePack(t, root, "dark", okManifest, map[string]string{"style.css": "body{}"})
	r, err := NewRegistry(root, "0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	th, err := r.Get("dark")
	if err != nil {
		t.Fatal(err)
	}
	h := th.HeadHTML()
	// The version goes in the PATH: CDNs that strip query strings from the
	// cache key made ?v= busting a no-op (2026-11-15 demo incident — a 3.3.0
	// stylesheet kept being served under the 4.0.0 URL for the whole TTL).
	if !strings.Contains(h, `/theme-assets/dark/1.0.0/style.css`) {
		t.Fatalf("version-in-path URL missing, head = %q", h)
	}
	if strings.Contains(h, `?v=`) {
		t.Fatalf("query-string busting is dead weight against query-stripping caches, head = %q", h)
	}
}

func TestMissingDirIsNotError(t *testing.T) {
	r, err := NewRegistry(filepath.Join(t.TempDir(), "nope"), "0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.List()) != 0 {
		t.Fatal("expected empty registry")
	}
}

func TestRequiresGate(t *testing.T) {
	root := t.TempDir()
	writePack(t, root, "too-new", `{"id":"too-new","name":"TooNew","version":"1.0.0","inject":{"css":["style.css"]},"requires":{"skin_nexus":">=9.9"}}`, map[string]string{"style.css": "body{}"})
	writePack(t, root, "ok", `{"id":"ok","name":"OK","version":"1.0.0","inject":{"css":["style.css"]},"requires":{"skin_nexus":">=0.1"}}`, map[string]string{"style.css": "body{}"})
	writePack(t, root, "broken-constraint", `{"id":"broken-constraint","name":"B","version":"1.0.0","inject":{"css":["style.css"]},"requires":{"skin_nexus":"~1"}}`, map[string]string{"style.css": "body{}"})
	r, err := NewRegistry(root, "0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	list := r.List()
	if len(list) != 1 || list[0].ID != "ok" {
		t.Fatalf("requires gate wrong: %+v", list)
	}
}
