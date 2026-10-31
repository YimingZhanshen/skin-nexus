// validate.go: the pack-acceptance rules of Rescan, exported so the theme
// market installer validates a package BEFORE it lands in the themes dir
// (a rejected pack must never touch the registry-visible tree).
package theme

import "fmt"

// Validate checks a manifest exactly the way Rescan does: id matches the
// directory name (no aliasing), required fields present (theme-spec §2),
// and requires.skin_nexus satisfied by hostVer.
func Validate(m Manifest, dirName, hostVer string) error {
	if !idRe.MatchString(dirName) {
		return fmt.Errorf("theme id %q must match %s", dirName, idRe)
	}
	if m.ID != dirName {
		return fmt.Errorf("theme.json id %q != directory name %q", m.ID, dirName)
	}
	if m.Name == "" || m.Version == "" || (len(m.Inject.CSS) == 0 && len(m.Inject.JS) == 0) {
		return fmt.Errorf("name/version/inject are required")
	}
	if !satisfiesRequires(m.Requires.SkinNexus, hostVer) {
		return fmt.Errorf("requires.skin_nexus %q not met by host %s", m.Requires.SkinNexus, hostVer)
	}
	return nil
}
