package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeLintPlugin(t *testing.T, dir, version string) string {
	t.Helper()
	pluginDir := filepath.Join(dir, "demo")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"demo","version":"` + version + `","hookApi":2,"entrypoint":"index.js","permissions":[],"minServer":">=0.4.0"}`
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "index.js"), []byte("0;"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := `{"schema":1,"title":{"en":"Demo"},"description":{"en":"demo"},"minServer":">=0.4.0","capabilities":[]}`
	if err := os.WriteFile(filepath.Join(pluginDir, "market.json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	return pluginDir
}

func TestPackIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "market.pem")
	var out, errOut bytes.Buffer
	if code := runKeygen([]string{"--id", "test", "--out", keyPath}, &out, &errOut); code != 0 {
		t.Fatalf("keygen=%d %s", code, errOut.String())
	}
	pluginDir := writeLintPlugin(t, dir, "1.0.0")
	a := filepath.Join(dir, "a.zip")
	b := filepath.Join(dir, "b.zip")
	for _, target := range []string{a, b} {
		out.Reset()
		errOut.Reset()
		if code := runPack([]string{"--dir", pluginDir, "--out", target, "--key", keyPath, "--key-id", "test", "--json"}, &out, &errOut); code != 0 {
			t.Fatalf("pack=%d %s", code, errOut.String())
		}
	}
	ab, _ := os.ReadFile(a)
	bb, _ := os.ReadFile(b)
	if !bytes.Equal(ab, bb) {
		t.Fatal("pack output is not byte-identical across runs")
	}
}

func TestPackNormalizesLineEndings(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "market.pem")
	var out, errOut bytes.Buffer
	if code := runKeygen([]string{"--id", "test", "--out", keyPath}, &out, &errOut); code != 0 {
		t.Fatalf("keygen=%d %s", code, errOut.String())
	}
	pluginDir := writeLintPlugin(t, dir, "1.0.0")
	lf := filepath.Join(dir, "lf.zip")
	if code := runPack([]string{"--dir", pluginDir, "--out", lf, "--key", keyPath, "--key-id", "test", "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("pack=%d %s", code, errOut.String())
	}
	for _, name := range []string{"plugin.json", "index.js", "market.json"} {
		path := filepath.Join(pluginDir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		crlf := bytes.ReplaceAll(raw, []byte("\n"), []byte("\r\n"))
		if err := os.WriteFile(path, crlf, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	crlf := filepath.Join(dir, "crlf.zip")
	if code := runPack([]string{"--dir", pluginDir, "--out", crlf, "--key", keyPath, "--key-id", "test", "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("pack=%d %s", code, errOut.String())
	}
	a, _ := os.ReadFile(lf)
	b, _ := os.ReadFile(crlf)
	if !bytes.Equal(a, b) {
		t.Fatal("LF and CRLF checkouts must produce identical bundles")
	}
}

func TestLintVersionMonotonicAndSecrets(t *testing.T) {
	dir := t.TempDir()
	pluginDir := writeLintPlugin(t, dir, "1.0.0")
	var out, errOut bytes.Buffer
	if code := runLint([]string{"--dir", pluginDir, "--host-version", "0.4.0", "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("lint=%d %s", code, errOut.String())
	}
	reg := filepath.Join(dir, "registry.json")
	if err := os.WriteFile(reg, []byte(`{"schema":1,"packages":[{"name":"demo","version":"1.0.0"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	errOut.Reset()
	if code := runLint([]string{"--dir", pluginDir, "--registry", reg}, &out, &errOut); code == 0 {
		t.Fatal("same version should fail monotonicity")
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "leak.pem"), []byte("-----BEGIN PRIVATE KEY-----\nx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	errOut.Reset()
	if code := runLint([]string{"--dir", pluginDir}, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "private key") {
		t.Fatalf("private key not rejected: %s", errOut.String())
	}
}

func TestLintRequiresMarketJSON(t *testing.T) {
	dir := t.TempDir()
	pluginDir := writeLintPlugin(t, dir, "1.0.0")
	if err := os.Remove(filepath.Join(pluginDir, "market.json")); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := runLint([]string{"--dir", pluginDir}, &out, &errOut); code == 0 {
		t.Fatal("missing market.json should fail")
	}
}

func TestKeygenPackRegistryRoundTrip(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "market.pem")
	var out, errOut bytes.Buffer
	if code := runKeygen([]string{"--id", "test", "--out", keyPath}, &out, &errOut); code != 0 {
		t.Fatalf("keygen=%d %s", code, errOut.String())
	}
	keys := strings.TrimSpace(out.String())
	if !strings.HasPrefix(keys, "test:") {
		t.Fatalf("keygen output = %q", keys)
	}

	pluginDir := filepath.Join(dir, "demo")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), []byte(`{"name":"demo","version":"1.0.0","hookApi":2,"entrypoint":"index.js","permissions":[],"minServer":">=0.3.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "index.js"), []byte("skin.log('x');"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "market.json"), []byte(`{"schema":1,"title":{"en":"Demo"},"description":{"en":"demo"},"minServer":">=0.3.0","capabilities":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	dist := filepath.Join(dir, "dist")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	if code := runPack([]string{"--dir", pluginDir, "--out", dist, "--key", keyPath, "--key-id", "test", "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("pack=%d %s", code, errOut.String())
	}
	bundle := filepath.Join(dist, "demo-1.0.0.zip")
	if _, err := os.Stat(bundle); err != nil {
		t.Fatalf("bundle missing: %v", err)
	}
	out.Reset()
	errOut.Reset()
	if code := runVerify([]string{"--bundle", bundle, "--keys", keys, "--host-version", "0.3.0"}, &out, &errOut); code != 0 {
		t.Fatalf("verify=%d %s", code, errOut.String())
	}

	regPath := filepath.Join(dir, "registry.json")
	out.Reset()
	errOut.Reset()
	if code := runRegistry([]string{"--dist", dist, "--url-base", "https://example.com/artifacts", "--key", keyPath, "--key-id", "test", "--out", regPath, "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("registry=%d %s", code, errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := runVerifyRegistry([]string{"--registry", regPath, "--keys", keys, "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("verify-registry=%d %s", code, errOut.String())
	}
}

func TestThemePackRegistryRoundTrip(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "theme-market.pem")
	var out, errOut bytes.Buffer
	if code := runKeygen([]string{"--id", "theme-test", "--out", keyPath}, &out, &errOut); code != 0 {
		t.Fatalf("keygen=%d %s", code, errOut.String())
	}
	keys := strings.TrimSpace(out.String())

	themeDir := filepath.Join(dir, "demo-theme")
	if err := os.MkdirAll(themeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"demo-theme","name":"演示主题","version":"1.0.0","author":"tester",` +
		`"description":"x","inject":{"css":["style.css"]},"requires":{"skin_nexus":">=0.1"}}`
	if err := os.WriteFile(filepath.Join(themeDir, "theme.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(themeDir, "style.css"), []byte(":root{--bg:#fff}"), 0o644); err != nil {
		t.Fatal(err)
	}
	// pack twice: deterministic bytes (same digest)
	a := filepath.Join(dir, "a.zip")
	b := filepath.Join(dir, "b.zip")
	for _, target := range []string{a, b} {
		out.Reset()
		errOut.Reset()
		if code := runPackTheme([]string{"--dir", themeDir, "--out", target, "--key", keyPath, "--key-id", "theme-test", "--json"}, &out, &errOut); code != 0 {
			t.Fatalf("pack-theme=%d %s", code, errOut.String())
		}
	}
	ab, _ := os.ReadFile(a)
	bb, _ := os.ReadFile(b)
	if !bytes.Equal(ab, bb) {
		t.Fatal("pack-theme output is not byte-identical across runs")
	}

	dist := filepath.Join(dir, "artifacts")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "demo-theme-1.0.0.zip"), ab, 0o644); err != nil {
		t.Fatal(err)
	}
	regPath := filepath.Join(dir, "themes-registry.json")
	out.Reset()
	errOut.Reset()
	if code := runRegistry([]string{"--kind", "theme", "--dist", dist, "--url-base", "https://example.com/themes/v1/artifacts",
		"--key", keyPath, "--key-id", "theme-test", "--out", regPath, "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("registry theme=%d %s", code, errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := runVerifyRegistry([]string{"--registry", regPath, "--keys", keys, "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("verify-registry=%d %s", code, errOut.String())
	}
	raw, err := os.ReadFile(regPath)
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		Packages []struct {
			Name      string               `json:"name"`
			MinServer string               `json:"minServer"`
			Title     map[string]string    `json:"title"`
			Dist      struct{ URL string } `json:"dist"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(raw, &reg); err != nil {
		t.Fatal(err)
	}
	if len(reg.Packages) != 1 {
		t.Fatalf("packages = %d", len(reg.Packages))
	}
	p := reg.Packages[0]
	if p.Name != "demo-theme" || p.MinServer != ">=0.1" || p.Title["zh-CN"] != "演示主题" ||
		!strings.HasSuffix(p.Dist.URL, "/themes/v1/artifacts/demo-theme-1.0.0.zip") {
		t.Fatalf("entry = %+v", p)
	}
	// a plugin-kind registry run over the same dist must refuse the theme pack
	out.Reset()
	errOut.Reset()
	if code := runRegistry([]string{"--dist", dist, "--url-base", "https://example.com/artifacts",
		"--key", keyPath, "--key-id", "theme-test", "--out", filepath.Join(dir, "bad.json")}, &out, &errOut); code == 0 {
		t.Fatal("plugin-kind registry accepted a theme bundle")
	}
}
