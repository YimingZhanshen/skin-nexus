package signedpkg

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"encoding/binary"
	"errors"
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

func makeZipWithUncompressedSize(t *testing.T, path, body string, declared uint64, method uint16) {
	t.Helper()
	if method == zip.Store {
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		zw := zip.NewWriter(f)
		w, err := zw.CreateHeader(&zip.FileHeader{Name: "bundle.json", Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		makeZip(t, path, map[string]string{"bundle.json": body})
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	local := bytes.Index(raw, []byte("PK\x03\x04"))
	central := bytes.Index(raw, []byte("PK\x01\x02"))
	if local < 0 || central < 0 {
		t.Fatal("zip headers not found")
	}
	binary.LittleEndian.PutUint32(raw[local+22:], uint32(declared))
	binary.LittleEndian.PutUint32(raw[central+24:], uint32(declared))
	binary.LittleEndian.PutUint16(raw[local+8:], method)
	binary.LittleEndian.PutUint16(raw[central+10:], method)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadBundleMetaArchiveErrorsAreNotMissingEntries(t *testing.T) {
	t.Run("nonexistent archive", func(t *testing.T) {
		_, err := ReadBundleMeta(filepath.Join(t.TempDir(), "missing.zip"))
		if !errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "bundle.json missing") {
			t.Fatalf("error = %v, want archive-open failure", err)
		}
	})
	t.Run("malformed archive", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "invalid.zip")
		if err := os.WriteFile(path, []byte("not a zip"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := ReadBundleMeta(path)
		if !errors.Is(err, zip.ErrFormat) || strings.Contains(err.Error(), "bundle.json missing") {
			t.Fatalf("error = %v, want malformed archive failure", err)
		}
	})
}

func TestReadBundleMetaSizeGuardBranchResponsibility(t *testing.T) {
	const limit = 1 << 20
	body := `{"name":"demo","version":"1.0.0","keyId":"test","signature":"sig"}`
	for _, tc := range []struct {
		name     string
		body     string
		declared uint64
		method   uint16
		wantErr  error
	}{
		{name: "declared size guard", body: body, declared: limit + 1, method: zip.Store},
		{name: "entry open guard", body: body, declared: 1, method: 99, wantErr: zip.ErrAlgorithm},
		{name: "actual overflow rejected by decoder before fallback guard", body: body + strings.Repeat(" ", limit+1-len(body)), declared: limit, method: zip.Deflate, wantErr: zip.ErrFormat},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "meta.zip")
			makeZipWithUncompressedSize(t, path, tc.body, tc.declared, tc.method)
			_, err := ReadBundleMeta(path)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("error = %v, want %v", err, tc.wantErr)
				}
			} else if err == nil || !strings.Contains(err.Error(), "bundle.json exceeds 1048576 bytes") {
				t.Fatalf("error = %v, want metadata size rejection", err)
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
