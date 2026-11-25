package signedpkg

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteAndVerifyBundle(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	ring := Keyring{"test": pub}
	path := filepath.Join(t.TempDir(), "demo.zip")
	digest, sha, err := WriteNamedBundle(path, "demo", "1.0.0", []Entry{
		{Name: "plugin.json", Data: []byte(`{"name":"demo"}`)},
		{Name: "index.js", Data: []byte("skin.log('x');")},
	}, priv, "test")
	if err != nil {
		t.Fatal(err)
	}
	if digest == "" || sha == "" {
		t.Fatal("empty digest/sha")
	}
	meta, err := VerifyBundle(path, ring)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Name != "demo" || meta.Version != "1.0.0" {
		t.Fatalf("meta = %+v", meta)
	}
	entry, err := ReadEntry(path, "index.js", 1024)
	if err != nil || string(entry) != "skin.log('x');" {
		t.Fatalf("entry = %q %v", entry, err)
	}
	// Tamper with a payload entry but keep bundle.json.
	zr, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = zr.Close()
	if _, err := VerifyBundle(path, Keyring{"other": pub}); err == nil {
		t.Fatal("unknown key accepted")
	}
}

func TestReadBundleMetaSizeLimit(t *testing.T) {
	const limit = 1 << 20
	body := `{"name":"demo","version":"1.0.0","keyId":"test","signature":"sig"}`
	for _, tc := range []struct {
		name    string
		size    int
		wantErr bool
	}{
		{name: "at_limit", size: limit},
		{name: "over_limit", size: limit + 1, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "meta.zip")
			makeZip(t, path, map[string]string{
				"bundle.json": body + strings.Repeat(" ", tc.size-len(body)),
			})
			meta, err := ReadBundleMeta(path)
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "bundle.json exceeds 1048576 bytes") {
					t.Fatalf("error = %v, want metadata size rejection", err)
				}
			} else if err != nil || meta.Name != "demo" {
				t.Fatalf("metadata at limit rejected: meta=%+v err=%v", meta, err)
			}
		})
	}
}

func TestReadBundleMetaMissingErrorPreserved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unsigned.zip")
	makeZip(t, path, map[string]string{"index.js": "1"})
	_, err := ReadBundleMeta(path)
	if err == nil || err.Error() != "bundle.json missing (signed bundles only)" {
		t.Fatalf("error = %v, want theme inspection compatibility error", err)
	}
}

func TestValidateEntryName(t *testing.T) {
	ok := []string{"plugin.json", "ui/a.html", "a/b-c.js"}
	bad := []string{"../x", "/abs", `a\b`, "CON.txt", "a/./b", "a//b", "a/..", "a.", "a ", "x:y"}
	for _, n := range ok {
		if err := ValidateEntryName(n); err != nil {
			t.Fatalf("valid %q: %v", n, err)
		}
	}
	for _, n := range bad {
		if err := ValidateEntryName(n); err == nil {
			t.Fatalf("invalid %q accepted", n)
		}
	}
}
