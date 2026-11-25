package signedpkg

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

const (
	MaxEntries       = 4096
	MaxEntryBytes    = 32 << 20
	MaxTotalBytes    = 64 << 20
	MaxZipRatio      = 200
	MaxMetadataBytes = 1 << 20
	MaxZipNameSize   = 512
)

var windowsReserved = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// ValidateEntryName is shared by the server extractor and the pack CLI so a
// bundle accepted by one is accepted by the other.
func ValidateEntryName(name string) error {
	if name == "" || len(name) > MaxZipNameSize || strings.Contains(name, "\\") {
		return fmt.Errorf("invalid zip entry name %q", name)
	}
	if strings.HasPrefix(name, "/") || path.IsAbs(name) || strings.Contains(name, "\x00") {
		return fmt.Errorf("absolute/unsafe zip entry %q", name)
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("traversal zip entry %q", name)
		}
		if strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " ") {
			return fmt.Errorf("unsafe zip entry %q", name)
		}
		base := strings.ToLower(strings.TrimSuffix(seg, filepath.Ext(seg)))
		if windowsReserved[base] {
			return fmt.Errorf("reserved zip entry %q", name)
		}
		if strings.Contains(seg, ":") {
			return fmt.Errorf("unsafe zip entry %q", name)
		}
	}
	return nil
}
