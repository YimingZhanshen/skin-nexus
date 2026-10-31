package thememkt

import (
	"archive/zip"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"skin-nexus/server/internal/marketfmt"
	"skin-nexus/server/internal/signedpkg"
	"skin-nexus/server/internal/theme"
)

type keys struct {
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
	ring signedpkg.Keyring
}

func newKeys(t *testing.T) keys {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return keys{pub: pub, priv: priv, ring: signedpkg.Keyring{"theme-test": pub}}
}

func manifestJSON(id, version string) string {
	m := map[string]any{
		"id": id, "name": "测试主题 " + id, "version": version,
		"inject": map[string]any{"css": []string{"style.css"}},
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// signedPack builds a deterministic signed zip (bundle.json carrier inside).
func signedPack(t *testing.T, dir, id, version string, k keys) []byte {
	t.Helper()
	path := filepath.Join(dir, id+"-"+version+".zip")
	_, _, err := signedpkg.WriteNamedBundle(path, id, version, []signedpkg.Entry{
		{Name: "theme.json", Data: []byte(manifestJSON(id, version))},
		{Name: "style.css", Data: []byte(":root{--bg:#fff}")},
	}, k.priv, "theme-test")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// plainPack builds an unsigned zip; extra names add raw entries (e.g. "../evil").
func plainPack(t *testing.T, dir, id, version string, extra map[string]string) []byte {
	t.Helper()
	path := filepath.Join(dir, "plain-"+id+".zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	add := func(name, body string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	add("theme.json", manifestJSON(id, version))
	add("style.css", ":root{--bg:#eee}")
	for n, b := range extra {
		add(n, b)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func newSvc(t *testing.T, k keys) (*Service, *theme.Registry, *string) {
	t.Helper()
	dir := t.TempDir()
	active := ""
	reg, err := theme.NewRegistry(dir, "0.5.0")
	if err != nil {
		t.Fatal(err)
	}
	s := New(Config{
		ThemesDir: dir, HostVersion: "0.5.0", TrustKeys: k.ring,
		MarketURL: "https://themes.example/registry.json",
		IsActive:  func(id string) bool { return id == active },
	}, reg)
	ap := &active
	return s, reg, ap
}

func setActive(p *string, id string) { *p = id }

func TestUploadSignedInstallAndHotReload(t *testing.T) {
	k := newKeys(t)
	s, reg, _ := newSvc(t, k)
	raw := signedPack(t, t.TempDir(), "demo", "1.0.0", k)

	insp := s.Inspect(raw)
	if insp.Status != "verified" || insp.Name != "demo" || insp.ZipSHA256 == "" {
		t.Fatalf("inspect = %+v", insp)
	}
	res, err := s.UploadInstall(raw, false, insp.ZipSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if res.Trust != "verified" || res.Version != "1.0.0" {
		t.Fatalf("result = %+v", res)
	}
	if _, err := reg.Get("demo"); err != nil {
		t.Fatalf("hot reload must see the pack without restart: %v", err)
	}
}

func TestUploadUnsignedGateAndReplace(t *testing.T) {
	k := newKeys(t)
	s, reg, _ := newSvc(t, k)
	raw := plainPack(t, t.TempDir(), "hand", "0.1.0", nil)

	insp := s.Inspect(raw)
	if insp.Status != "unsigned" {
		t.Fatalf("status = %s", insp.Status)
	}
	if _, err := s.UploadInstall(raw, false, ""); err != ErrUnverified {
		t.Fatalf("want ErrUnverified, got %v", err)
	}
	if _, err := s.UploadInstall(raw, true, "deadbeef"); err != ErrDigestStale {
		t.Fatalf("want ErrDigestStale, got %v", err)
	}
	if _, err := s.UploadInstall(raw, true, ""); err != nil {
		t.Fatal(err)
	}
	// upgrade in place (same id, new version) — the old pack is replaced
	if _, err := s.UploadInstall(plainPack(t, t.TempDir(), "hand", "0.2.0", nil), true, ""); err != nil {
		t.Fatal(err)
	}
	th, err := reg.Get("hand")
	if err != nil || th.Manifest.Version != "0.2.0" {
		t.Fatalf("after replace: %+v %v", th, err)
	}
	entries, _ := os.ReadDir(filepath.Join(s.cfg.ThemesDir))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".replaced-") || strings.Contains(e.Name(), ".install-") {
			t.Fatalf("staging/backup left behind: %s", e.Name())
		}
	}
}

func TestUploadRejectsBadPacks(t *testing.T) {
	k := newKeys(t)
	s, reg, _ := newSvc(t, k)

	if insp := s.Inspect(plainPack(t, t.TempDir(), "bad", "", nil)); insp.Status != "invalid" {
		t.Fatalf("missing version accepted: %+v", insp)
	}
	traversal := plainPack(t, t.TempDir(), "trav", "1.0.0", map[string]string{"../evil.txt": "x"})
	if insp := s.Inspect(traversal); insp.Status != "invalid" && insp.Status != "unsigned" {
		t.Fatalf("unexpected status %+v", insp)
	}
	if _, err := s.UploadInstall(traversal, true, ""); err == nil || !strings.Contains(err.Error(), "zip entry") {
		t.Fatalf("traversal pack must fail extraction: %v", err)
	}
	if _, err := reg.Get("trav"); err == nil {
		t.Fatal("failed pack must not be installed")
	}
	// tampered pack: content changed after signing (bundle.json kept as-is)
	good := signedPack(t, t.TempDir(), "sig", "1.0.0", k)
	tampered := retamper(t, good, "style.css", ":root{--bg:#hacked}")
	insp := s.Inspect(tampered)
	if insp.Status == "verified" {
		t.Fatal("tampered pack inspected as verified")
	}
	if _, err := s.UploadInstall(tampered, true, ""); err == nil {
		t.Fatal("tampered pack must not install")
	}
}

// retamper rewrites one entry of a signed zip WITHOUT touching bundle.json,
// so the recorded signature no longer covers the content digest.
func retamper(t *testing.T, raw []byte, wantName, wantBody string) []byte {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "in.zip")
	if err := os.WriteFile(src, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(src)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	dst := filepath.Join(dir, "out.zip")
	f, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, f := range zr.File {
		w, err := zw.Create(f.Name)
		if err != nil {
			t.Fatal(err)
		}
		if f.Name == wantName {
			_, _ = w.Write([]byte(wantBody))
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(w, rc)
		rc.Close()
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	out, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCatalogFromSignedRegistry(t *testing.T) {
	k := newKeys(t)
	s, _, _ := newSvc(t, k)
	reg := &marketfmt.Registry{
		Schema: 1, GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		KeyID:     "theme-test",
		Packages: []marketfmt.Package{
			{Name: "demo", Version: "2.0.0", Dist: marketfmt.Dist{URL: "https://themes.example/a.zip", Size: 123, SHA256: strings.Repeat("a", 64)}},
			{Name: "fresh", Version: "1.0.0", Dist: marketfmt.Dist{URL: "https://themes.example/b.zip", Size: 123, SHA256: strings.Repeat("b", 64)}},
		},
	}
	payload, _ := marketfmt.Payload(*reg)
	reg.Signature = base64.StdEncoding.EncodeToString(signedpkg.Sign(k.priv, payload))
	if _, err := marketfmt.VerifyRegistry(mustJSON(t, reg), k.ring, time.Now()); err != nil {
		t.Fatal(err)
	}
	s.cache, s.fetched = reg, time.Now()

	// install "demo" locally first so the catalog can mark update state
	raw := signedPack(t, t.TempDir(), "demo", "1.0.0", k)
	if _, err := s.UploadInstall(raw, false, ""); err != nil {
		t.Fatal(err)
	}
	list, err := s.Catalog(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("list = %+v", list)
	}
	var demo, fresh *Entry
	for i := range list {
		if list[i].Name == "demo" {
			demo = &list[i]
		} else {
			fresh = &list[i]
		}
	}
	if demo.Installed != "1.0.0" || !demo.CanUpdate {
		t.Fatalf("demo entry = %+v", demo)
	}
	if fresh.Installed != "" || fresh.CanUpdate {
		t.Fatalf("fresh entry = %+v", fresh)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestUninstallGuardsActiveTheme(t *testing.T) {
	k := newKeys(t)
	s, reg, active := newSvc(t, k)
	raw := signedPack(t, t.TempDir(), "guard", "1.0.0", k)
	if _, err := s.UploadInstall(raw, false, ""); err != nil {
		t.Fatal(err)
	}
	setActive(active, "guard")
	if err := s.Uninstall("guard"); err != ErrActiveTheme {
		t.Fatalf("active uninstall must be refused, got %v", err)
	}
	setActive(active, "")
	if err := s.Uninstall("guard"); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Get("guard"); err == nil {
		t.Fatal("pack still visible after uninstall")
	}
	if err := s.Uninstall("ghost"); err == nil {
		t.Fatal("uninstall of unknown id must fail")
	}
}
