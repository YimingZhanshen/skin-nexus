// extract.go: the safe zip extractor shared by signed-package consumers
// (theme market installs). The plugin host keeps its internal copy untouched
// (frozen zone); this export follows the same rules: traversal, absolute
// paths, backslashes, symlinks, Windows-reserved names, case collisions,
// oversized entries and zip bombs are all rejected.
package signedpkg

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func ExtractZip(zipPath, dest string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer zr.Close()
	if len(zr.File) > MaxEntries {
		return fmt.Errorf("zip has too many entries (%d)", len(zr.File))
	}
	var total int64
	seen := map[string]bool{}
	for _, f := range zr.File {
		name := f.Name
		if err := ValidateEntryName(name); err != nil {
			return err
		}
		lower := strings.ToLower(name)
		if seen[lower] {
			return fmt.Errorf("duplicate/case-colliding zip entry %q", name)
		}
		seen[lower] = true
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(filepath.Join(dest, filepath.FromSlash(name)), 0o755); err != nil {
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
		if f.UncompressedSize64 > MaxEntryBytes {
			return fmt.Errorf("zip entry %q exceeds %d bytes", name, MaxEntryBytes)
		}
		if f.CompressedSize64 > 0 && f.UncompressedSize64 > 1<<20 &&
			f.UncompressedSize64/f.CompressedSize64 > MaxZipRatio {
			return fmt.Errorf("zip entry %q has suspicious compression ratio", name)
		}
		total += int64(f.UncompressedSize64)
		if total > MaxTotalBytes {
			return fmt.Errorf("zip exceeds %d uncompressed bytes", MaxTotalBytes)
		}
		target := filepath.Join(dest, filepath.FromSlash(name))
		// defense in depth: resolved path must stay inside dest (entry names
		// are already validated, this catches any future gap)
		absDest, err := filepath.Abs(dest)
		if err != nil {
			return err
		}
		absTarget, err := filepath.Abs(target)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(absTarget, absDest+string(os.PathSeparator)) {
			return fmt.Errorf("zip entry %q escapes destination directory", name)
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
		_, copyErr := io.Copy(out, io.LimitReader(rc, MaxEntryBytes+1))
		out.Close()
		rc.Close()
		if copyErr != nil {
			return copyErr
		}
		if fi, err := os.Stat(target); err != nil || fi.Size() > MaxEntryBytes {
			return fmt.Errorf("zip entry %q exceeded limits while extracting", name)
		}
	}
	return nil
}
