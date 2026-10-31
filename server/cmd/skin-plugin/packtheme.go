// pack-theme: build a deterministic signed theme bundle from a pack
// directory (theme.json + flat assets). Same trust plumbing as plugin packs
// (bundle.json carrier signature over name/version/content digest), so the
// market registry and the server installer treat both kinds uniformly.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"skin-nexus/server/internal/marketfmt"
	"skin-nexus/server/internal/signedpkg"
	"skin-nexus/server/internal/theme"
)

// themePackageView mirrors the registry-relevant fields of theme.json.
type themePackageView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Author      string `json:"author"`
	Description string `json:"description"`
	Requires    struct {
		SkinNexus string `json:"skin_nexus"`
	} `json:"requires"`
}

// themePackage builds one registry entry from a verified theme zip. Optional
// market.json (same file the plugin market uses) overrides i18n title and
// description and adds changelog/readme.
func themePackage(zipPath, fileName string, rawManifest []byte, mm *marketMeta, urlBase string) (marketfmt.Package, error) {
	var m themePackageView
	if err := json.Unmarshal(rawManifest, &m); err != nil {
		return marketfmt.Package{}, fmt.Errorf("theme.json: %w", err)
	}
	meta, err := signedpkg.ReadBundleMeta(zipPath)
	if err != nil {
		return marketfmt.Package{}, err
	}
	if m.ID == "" || m.Version == "" {
		return marketfmt.Package{}, fmt.Errorf("theme.json needs id and version")
	}
	if meta.Name != m.ID || meta.Version != m.Version {
		return marketfmt.Package{}, fmt.Errorf("bundle signs %s@%s but theme.json says %s@%s",
			meta.Name, meta.Version, m.ID, m.Version)
	}
	title := map[string]string{}
	if m.Name != "" {
		title["zh-CN"] = m.Name
	}
	description := map[string]string{}
	if m.Description != "" {
		description["zh-CN"] = m.Description
	}
	author, changelog, readme := m.Author, "", ""
	minServer := m.Requires.SkinNexus
	if mm != nil {
		if len(mm.Title) > 0 {
			title = mm.Title
		}
		if len(mm.Description) > 0 {
			description = mm.Description
		}
		if mm.Author != "" {
			author = mm.Author
		}
		changelog, readme = mm.Changelog, mm.Readme
		if mm.MinServer != "" {
			minServer = mm.MinServer
		}
	}
	sha, err := signedpkg.SHA256File(zipPath)
	if err != nil {
		return marketfmt.Package{}, err
	}
	fi, _ := os.Stat(zipPath)
	return marketfmt.Package{
		Name: m.ID, Version: m.Version, Title: title, Description: description,
		Author: author, MinServer: minServer, Changelog: changelog, Readme: readme,
		Dist: marketfmt.Dist{URL: strings.TrimRight(urlBase, "/") + "/" + fileName, Size: fi.Size(), SHA256: sha},
	}, nil
}

// hostVersionAny skips the requires.skin_nexus gate at pack time — the
// installing server re-validates against its real version.
const hostVersionAny = "9999.9999.9999"

func runPackTheme(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("pack-theme", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "", "theme pack directory")
	out := fs.String("out", "", "output zip or directory")
	key := fs.String("key", "", "Ed25519 PKCS#8 PEM private key")
	keyID := fs.String("key-id", "", "key id")
	force := fs.Bool("force", false, "overwrite existing output")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *dir == "" || *out == "" || *key == "" || *keyID == "" {
		fmt.Fprintln(stderr, "pack-theme: --dir --out --key --key-id are required")
		return 2
	}
	raw, err := os.ReadFile(filepath.Join(*dir, "theme.json"))
	if err != nil {
		fmt.Fprintf(stderr, "pack-theme: theme.json: %v\n", err)
		return 1
	}
	var m theme.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		fmt.Fprintf(stderr, "pack-theme: theme.json: %v\n", err)
		return 1
	}
	if err := theme.Validate(m, filepath.Base(filepath.Clean(*dir)), hostVersionAny); err != nil {
		fmt.Fprintf(stderr, "pack-theme: %v\n", err)
		return 1
	}
	entries, err := collectEntries(*dir, "", false)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !hasEntry(entries, "theme.json") {
		fmt.Fprintln(stderr, "pack-theme: theme.json missing")
		return 1
	}
	priv, err := loadPrivateKey(*key)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	outPath := *out
	if fi, err := os.Stat(outPath); err == nil && fi.IsDir() {
		outPath = filepath.Join(outPath, m.ID+"-"+m.Version+".zip")
	}
	if !strings.HasSuffix(strings.ToLower(outPath), ".zip") {
		outPath += ".zip"
	}
	if !*force {
		if _, err := os.Stat(outPath); err == nil {
			fmt.Fprintf(stderr, "pack-theme: %s exists (use --force)\n", outPath)
			return 1
		}
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	digest, sha, err := signedpkg.WriteNamedBundle(outPath, m.ID, m.Version, entries, priv, *keyID)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fi, _ := os.Stat(outPath)
	result := map[string]any{
		"name": m.ID, "version": m.Version, "keyId": *keyID,
		"sha256": sha, "size": fi.Size(), "digest": digest, "path": outPath,
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(result)
	} else {
		fmt.Fprintf(stdout, "%s %s\nsha256=%s size=%d\n%s\n", m.ID, m.Version, sha, fi.Size(), outPath)
	}
	return 0
}
