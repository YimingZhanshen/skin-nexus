package oidc

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeKeyPEM(t *testing.T, dir, name string, block *pem.Block) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A fresh directory mints the key; a second call must LOAD it, not mint again —
// regenerating on a successful read would invalidate every issued token.
func TestLoadOrCreateKeyCreatesThenReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys", "oidc.pem")
	first, err := LoadOrCreateKey(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	second, err := LoadOrCreateKey(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if first.N.Cmp(second.N) != 0 {
		t.Fatal("reload minted a different key — existing key was replaced")
	}
	if runtime.GOOS != "windows" { // Windows ACLs do not mirror POSIX mode bits
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("key file mode = %o, want 600", perm)
		}
	}
}

// Both documented PEM shapes must load, and a file that parses to a non-RSA
// key must be rejected rather than silently replaced.
func TestLoadOrCreateKeyFormats(t *testing.T) {
	dir := t.TempDir()
	key, err := rsa.GenerateKey(rand.Reader, 2048) // 2048 keeps the table fast
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		block *pem.Block
	}{
		{"pkcs1", &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}},
		{"pkcs8", func() *pem.Block {
			der, err := x509.MarshalPKCS8PrivateKey(key)
			if err != nil {
				t.Fatal(err)
			}
			return &pem.Block{Type: "PRIVATE KEY", Bytes: der}
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeKeyPEM(t, dir, tc.name+".pem", tc.block)
			got, err := LoadOrCreateKey(path)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if got.N.Cmp(key.N) != 0 {
				t.Fatal("loaded a different key than the file holds")
			}
		})
	}

	t.Run("garbage", func(t *testing.T) {
		path := filepath.Join(dir, "garbage.pem")
		if err := os.WriteFile(path, []byte("not a pem at all"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadOrCreateKey(path); err == nil || !strings.Contains(err.Error(), "not PEM") {
			t.Fatalf("err = %v, want not-PEM rejection", err)
		}
	})

	t.Run("non-rsa", func(t *testing.T) {
		ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		der, err := x509.MarshalPKCS8PrivateKey(ec)
		if err != nil {
			t.Fatal(err)
		}
		path := writeKeyPEM(t, dir, "ec.pem", &pem.Block{Type: "PRIVATE KEY", Bytes: der})
		if _, err := LoadOrCreateKey(path); err == nil || !strings.Contains(err.Error(), "unsupported") {
			t.Fatalf("err = %v, want unsupported-format rejection", err)
		}
	})
}

// A read failure that is NOT "file missing" must surface as an error and must
// NOT touch the key on disk: the old loader fell through to regeneration on
// any read error, silently replacing the key (every issued token dies) — the
// exact failure this test pins.
func TestLoadOrCreateKeyReadErrorDoesNotResign(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oidc.pem")
	good, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(good)
	if err != nil {
		t.Fatal(err)
	}
	before := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}

	orig := readFile
	readFile = func(string) ([]byte, error) { return nil, errors.New("transient IO failure") }
	defer func() { readFile = orig }()

	if _, err := LoadOrCreateKey(path); err == nil || !strings.Contains(err.Error(), "transient IO failure") {
		t.Fatalf("err = %v, want the read failure surfaced", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("key file was rewritten after a failed read — the old key is gone")
	}
}

// The seam exists only for the read-failure test above; a missing file still
// takes the generate path (this is the behavior the mutation flips).
func TestLoadOrCreateKeyMissingFileGenerates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "oidc.pem")
	if _, err := LoadOrCreateKey(path); err != nil {
		t.Fatalf("missing file must generate: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("generated key not written: %v", err)
	}
}
