package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"skin-nexus/server/internal/marketfmt"
	"skin-nexus/server/internal/plugin"
	"skin-nexus/server/internal/signedpkg"
)

func runRegistry(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("registry", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dist := fs.String("dist", "", "directory containing signed bundle zips")
	urlBase := fs.String("url-base", "", "https artifact base URL")
	key := fs.String("key", "", "Ed25519 PKCS#8 PEM private key")
	keyID := fs.String("key-id", "", "key id")
	keys := fs.String("keys", "", "additional trust keys")
	expiresDays := fs.Int("expires-days", 90, "registry validity in days")
	kind := fs.String("kind", "plugin", "package kind: plugin | theme")
	out := fs.String("out", "registry.json", "output registry path")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *dist == "" || *urlBase == "" || *key == "" || *keyID == "" {
		fmt.Fprintln(stderr, "registry: --dist --url-base --key --key-id are required")
		return 2
	}
	u, err := url.Parse(*urlBase)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		fmt.Fprintln(stderr, "registry: --url-base must be https")
		return 2
	}
	priv, err := loadPrivateKey(*key)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	ring, err := keyringFor(*keys, *keyID, priv)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *kind != "plugin" && *kind != "theme" {
		fmt.Fprintln(stderr, "registry: --kind must be plugin or theme")
		return 2
	}
	manifestName := "plugin.json"
	if *kind == "theme" {
		manifestName = "theme.json"
	}
	files, err := os.ReadDir(*dist)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	byName := map[string]marketfmt.Package{}
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(strings.ToLower(f.Name()), ".zip") {
			continue
		}
		path := filepath.Join(*dist, f.Name())
		meta, err := signedpkg.VerifyBundle(path, ring)
		if err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", f.Name(), err)
			return 1
		}
		rawManifest, err := signedpkg.ReadEntry(path, manifestName, 1<<20)
		if err != nil {
			fmt.Fprintf(stderr, "%s: %s: %v\n", f.Name(), manifestName, err)
			return 1
		}
		var mm *marketMeta
		if rawMeta, err := signedpkg.ReadEntry(path, "market.json", 256<<10); err == nil {
			var parsed marketMeta
			if err := json.Unmarshal(rawMeta, &parsed); err != nil {
				fmt.Fprintf(stderr, "%s: market.json: %v\n", f.Name(), err)
				return 1
			}
			mm = &parsed
		}
		if *kind == "theme" {
			pkg, err := themePackage(path, f.Name(), rawManifest, mm, *urlBase)
			if err != nil {
				fmt.Fprintf(stderr, "%s: %v\n", f.Name(), err)
				return 1
			}
			if old, ok := byName[pkg.Name]; ok {
				cmp, err := marketfmt.CompareVersions(pkg.Version, old.Version)
				if err != nil || cmp <= 0 {
					continue
				}
			}
			byName[pkg.Name] = pkg
			continue
		}
		var m manifestView
		if err := json.Unmarshal(rawManifest, &m); err != nil {
			fmt.Fprintf(stderr, "%s: plugin.json: %v\n", f.Name(), err)
			return 1
		}
		if m.Name != meta.Name || m.Version != meta.Version {
			fmt.Fprintf(stderr, "%s: manifest/bundle name/version mismatch\n", f.Name())
			return 1
		}
		info := plugin.Info{
			Manifest: plugin.Manifest{
				Name: m.Name, Version: m.Version, Permissions: m.Permissions,
				MinServer: m.MinServer, Requires: m.Requires, Conflicts: m.Conflicts,
			},
		}
		if err := validateMarketMeta(info, mm); err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", f.Name(), err)
			return 1
		}
		caps := m.Permissions
		title := map[string]string{"en": m.Title}
		description := map[string]string{"en": m.Description}
		author, changelog, readme, minServer := "", "", "", m.MinServer
		req, conflicts := m.Requires, m.Conflicts
		if mm != nil {
			if len(mm.Capabilities) > 0 {
				caps = mm.Capabilities
			}
			if len(mm.Title) > 0 {
				title = mm.Title
			}
			if len(mm.Description) > 0 {
				description = mm.Description
			}
			author, changelog, readme = mm.Author, mm.Changelog, mm.Readme
			if mm.MinServer != "" {
				minServer = mm.MinServer
			}
			if mm.Requires != nil {
				req = mm.Requires
			}
			if len(mm.Conflicts) > 0 {
				conflicts = mm.Conflicts
			}
		}
		sha, err := signedpkg.SHA256File(path)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fi, _ := os.Stat(path)
		pkg := marketfmt.Package{
			Name: m.Name, Version: m.Version, Title: title, Description: description,
			Author: author, Requires: req, Conflicts: conflicts,
			Capabilities: caps, MinServer: minServer, Changelog: changelog, Readme: readme,
			Dist: marketfmt.Dist{URL: strings.TrimRight(*urlBase, "/") + "/" + f.Name(), Size: fi.Size(), SHA256: sha},
		}
		if old, ok := byName[pkg.Name]; ok {
			cmp, err := marketfmt.CompareVersions(pkg.Version, old.Version)
			if err != nil {
				fmt.Fprintf(stderr, "%s: %v\n", f.Name(), err)
				return 1
			}
			if cmp <= 0 {
				continue
			}
		}
		byName[pkg.Name] = pkg
	}
	if len(byName) == 0 {
		fmt.Fprintln(stderr, "registry: no signed bundles found")
		return 1
	}
	pkgs := make([]marketfmt.Package, 0, len(byName))
	for _, p := range byName {
		pkgs = append(pkgs, p)
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].Name < pkgs[j].Name })
	now := time.Now().UTC()
	reg := marketfmt.Build(pkgs, *keyID, now, now.AddDate(0, 0, *expiresDays))
	if err := marketfmt.Sign(&reg, priv); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	raw, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if len(raw) > 900<<10 {
		fmt.Fprintln(stderr, "registry: exceeds 900 KiB")
		return 1
	}
	tmp := *out + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := os.Rename(tmp, *out); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result := map[string]any{
		"packages": len(pkgs), "generatedAt": reg.GeneratedAt, "expiresAt": reg.ExpiresAt,
		"keyId": reg.KeyID, "sha256": signedpkg.SHA256(raw), "size": len(raw), "path": *out,
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(result)
	} else {
		fmt.Fprintf(stdout, "registry %s: %d packages, expires %s\nsha256=%s size=%d\n", *out, len(pkgs), reg.ExpiresAt, result["sha256"], len(raw))
	}
	return 0
}

func runVerifyRegistry(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("verify-registry", flag.ContinueOnError)
	fs.SetOutput(stderr)
	file := fs.String("registry", "", "registry JSON")
	keys := fs.String("keys", "", "trust keys")
	nowStr := fs.String("now", "", "RFC3339 time override")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" {
		fmt.Fprintln(stderr, "verify-registry: --registry is required")
		return 2
	}
	if *keys == "" {
		*keys = os.Getenv("SKIN_PLUGIN_TRUST_KEYS")
	}
	ring, err := keyringFor(*keys, "", nil)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	raw, err := os.ReadFile(*file)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	now := time.Now()
	if *nowStr != "" {
		now, err = time.Parse(time.RFC3339, *nowStr)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
	}
	reg, err := marketfmt.VerifyRegistry(raw, ring, now)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	out := map[string]any{"ok": true, "packages": len(reg.Packages), "keyId": reg.KeyID, "expiresAt": reg.ExpiresAt}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(out)
	} else {
		fmt.Fprintf(stdout, "OK %d packages key=%s expires=%s\n", len(reg.Packages), reg.KeyID, reg.ExpiresAt)
	}
	return 0
}
