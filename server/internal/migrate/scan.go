package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"skin-nexus/server/internal/texture"
)

// TextureScan is the legacy texture tree audit (原样搬迁 policy):
// full re-hash (filename must equal sha256 of BYTES) + IHDR check.
// Nothing is rewritten; problems are reported for human handling.
type TextureScan struct {
	Checked      int
	OK           int
	HashMismatch []string // filename != content hash (cache-key integrity broken)
	BadIHDR      []string // unreadable header (candidate for background decode audit)
	NotHexName   []string // filename not 64-hex (legacy anomalies)
}

func (s TextureScan) String() string {
	return fmt.Sprintf("texture scan: checked=%d ok=%d hashMismatch=%d badIHDR=%d badName=%d",
		s.Checked, s.OK, len(s.HashMismatch), len(s.BadIHDR), len(s.NotHexName))
}

// ScanTextureTree walks a legacy TEXTURES_DIR (flat, hash-named, no extension).
func ScanTextureTree(dir string) (TextureScan, error) {
	var out TextureScan
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		out.Checked++
		if !isHexOfLen(name, 64) {
			out.NotHexName = append(out.NotHexName, name)
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			out.BadIHDR = append(out.BadIHDR, name)
			continue
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != name {
			out.HashMismatch = append(out.HashMismatch, name)
			continue
		}
		if _, _, err := texture.ScanLegacyIHDR(raw); err != nil {
			out.BadIHDR = append(out.BadIHDR, name)
			continue
		}
		out.OK++
	}
	return out, nil
}

// MigrateFile imports one legacy texture byte-exact (原样搬迁: no re-encode).
// Returns the hash it is stored under (== filename for well-formed trees).
func MigrateFile(s *texture.Store, srcPath, name string) (string, error) {
	raw, err := os.ReadFile(srcPath)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	if hash != name {
		return "", fmt.Errorf("migrate: %s content != name (%s)", name, hash)
	}
	if err := s.Save(&texture.Result{Hash: hash, Bytes: raw}); err != nil {
		return "", err
	}
	return hash, nil
}
