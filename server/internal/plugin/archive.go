package plugin

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"skin-nexus/server/internal/signedpkg"
)

const (
	maxZipEntries    = 4096
	maxZipEntryBytes = 32 << 20
	maxZipTotalBytes = 64 << 20
	maxZipRatio      = 200
	maxZipNameBytes  = 512
)

// extractZip safely extracts a plugin bundle into an empty staging directory.
// It rejects traversal, absolute paths, backslashes, symlinks, Windows
// reserved names, case collisions, oversized entries and zip bombs.
func extractZip(zipPath, dest string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer zr.Close()
	if len(zr.File) > maxZipEntries {
		return fmt.Errorf("zip has too many entries (%d)", len(zr.File))
	}
	var total int64
	seen := map[string]bool{}
	absDest, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		name := f.Name
		if err := validateZipName(name); err != nil {
			return err
		}
		lower := strings.ToLower(name)
		if seen[lower] {
			return fmt.Errorf("duplicate/case-colliding zip entry %q", name)
		}
		seen[lower] = true
		target := filepath.Join(dest, filepath.FromSlash(name))
		// Same backstop as signedpkg.ExtractZip: validateZipName rejects ".."
		// segments, but the containment check has to dominate every write,
		// including the directory branch below, or it protects nothing.
		absTarget, err := filepath.Abs(target)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(absTarget, absDest+string(os.PathSeparator)) {
			return fmt.Errorf("zip entry %q escapes destination directory", name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("zip entry %q is a symlink", name)
		}
		if !f.Mode().IsRegular() {
			return fmt.Errorf("zip entry %q is not a regular file", name)
		}
		if f.UncompressedSize64 > maxZipEntryBytes {
			return fmt.Errorf("zip entry %q exceeds %d bytes", name, maxZipEntryBytes)
		}
		if f.CompressedSize64 > 0 && f.UncompressedSize64 > 1<<20 &&
			f.UncompressedSize64/f.CompressedSize64 > maxZipRatio {
			return fmt.Errorf("zip entry %q has suspicious compression ratio", name)
		}
		total += int64(f.UncompressedSize64)
		if total > maxZipTotalBytes {
			return fmt.Errorf("zip exceeds %d uncompressed bytes", maxZipTotalBytes)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			rc.Close()
			return err
		}
		_, copyErr := io.Copy(out, io.LimitReader(rc, maxZipEntryBytes+1))
		out.Close()
		rc.Close()
		if copyErr != nil {
			return copyErr
		}
		if fi, err := os.Stat(target); err != nil || fi.Size() > maxZipEntryBytes {
			return fmt.Errorf("zip entry %q exceeded limits while extracting", name)
		}
	}
	return nil
}

func validateZipName(name string) error {
	return signedpkg.ValidateEntryName(name)
}

// findPluginRoot accepts either a bundle with plugin.json at the root or one
// single top-level directory containing plugin.json.
func findPluginRoot(dir string) (string, error) {
	if _, err := os.Stat(filepath.Join(dir, "plugin.json")); err == nil {
		return dir, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	if len(dirs) == 1 {
		root := filepath.Join(dir, dirs[0])
		if _, err := os.Stat(filepath.Join(root, "plugin.json")); err == nil {
			return root, nil
		}
	}
	return "", errors.New("bundle must contain plugin.json at its root (or one top-level directory)")
}
