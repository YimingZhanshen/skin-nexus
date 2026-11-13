// Package version is the one version predicate in this repository.
//
// Three engines used to parse x.y[.z] with three different levels of
// strictness: the plugin gate matched a regex, the market formatter ran
// Sscanf("%d") per segment and waved whatever trailed the number through,
// and the theme gate discarded Atoi errors and called a typo 0. The same
// string was therefore "invalid" in one place and "equal to 0.1.0" in
// another, so a pack declaring 0-rc1 sorted as 0.0.0 in the registry while
// a plugin declaring the same string was rejected — and a theme constraint
// written with a typo silently passed against every host ever built.
//
// Everything that compares a version parses it here first. The grammar is
// deliberately narrower than semver 2.0: plain x.y[.z] with an optional v
// prefix, nothing else. Prerelease and build metadata are rejected rather
// than ignored, because a version this package cannot order is a version no
// gate should decide on — the callers fail closed instead.
package version

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// re is narrower than semver 2.0 on purpose: unsupported syntax must fail
// closed rather than silently satisfy a gate meant for a newer host.
var re = regexp.MustCompile(`^[vV]?(\d+)(?:\.(\d+))?(?:\.(\d+))?$`)

// Version is a parsed x.y[.z]. Missing segments are zero, so "1.21" and
// "1.21.0" are the same version — every pack in the wild writes one form
// and every host the other.
type Version struct {
	Major, Minor, Patch int
}

// Parse accepts an optional v/V prefix, surrounding space, and one to three
// numeric segments. Anything else — a prerelease suffix, a fourth segment,
// a non-numeric segment, an empty string — is an error, never a zero.
func Parse(s string) (Version, error) {
	m := re.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Version{}, fmt.Errorf("invalid version %q", s)
	}
	segment := func(p string) int {
		if p == "" {
			return 0
		}
		n, _ := strconv.Atoi(p) // the regex proved every byte is a digit
		return n
	}
	return Version{segment(m[1]), segment(m[2]), segment(m[3])}, nil
}

// Compare orders numerically, segment by segment: 2.0.0 sorts before
// 10.0.0, and a missing segment is zero.
func (v Version) Compare(o Version) int {
	switch {
	case v.Major != o.Major:
		if v.Major < o.Major {
			return -1
		}
		return 1
	case v.Minor != o.Minor:
		if v.Minor < o.Minor {
			return -1
		}
		return 1
	case v.Patch != o.Patch:
		if v.Patch < o.Patch {
			return -1
		}
		return 1
	}
	return 0
}

// CompareStrings is the whole predicate in one call: parse both sides, then
// order them. An unparseable side is an error, so a caller can never read a
// typo as 0.0.0.
func CompareStrings(a, b string) (int, error) {
	av, err := Parse(a)
	if err != nil {
		return 0, err
	}
	bv, err := Parse(b)
	if err != nil {
		return 0, err
	}
	return av.Compare(bv), nil
}
