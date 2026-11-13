package marketfmt

// version_predicate_test.go — W1-3: this engine must parse versions exactly
// the way the plugin engine does.
//
// Three engines used to parse x.y[.z] with three different levels of
// strictness: plugin matched a regex, this one ran Sscanf("%d") per segment
// and waved whatever trailed the number through, and the theme engine
// discarded Atoi errors and called a typo 0. The same string was therefore
// "invalid" here and "equal to 0.1.0" there, so a package version like
// 0-rc1 could sort as 0.0.0 while a plugin declaring the same string was
// rejected outright. The shared predicate lives in internal/version; these
// cases are the half of the table this package is responsible for.

import "testing"

func TestCompareVersionsRejectsNonNumericVersions(t *testing.T) {
	for _, s := range []string{"0-rc1", "1.2.3-beta", "1.2.3.4", "1.x", "abc", "", "1,2"} {
		if _, err := CompareVersions(s, "1.0.0"); err == nil {
			t.Fatalf("CompareVersions(%q, ...) must reject a version that is not x.y[.z]", s)
		}
	}
}

// Missing segments count as zero — a theme or plugin written as "1.21" and a
// host written as "1.21.0" are the same version, and the v prefix is
// accepted because packagers write it.
func TestCompareVersionsTreatsMissingSegmentsAsZero(t *testing.T) {
	for _, p := range [][2]string{{"1.21", "1.21.0"}, {"0.6", "0.6.0"}, {"v1.2", "1.2.0"}, {"V2", "2.0.0"}} {
		got, err := CompareVersions(p[0], p[1])
		if err != nil {
			t.Fatalf("CompareVersions(%q, %q): %v", p[0], p[1], err)
		}
		if got != 0 {
			t.Fatalf("CompareVersions(%q, %q) = %d, want 0 — missing segments are zero", p[0], p[1], got)
		}
	}
}

func TestCompareVersionsOrders(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.1", -1},
		{"1.1.0", "1.0.9", 1},
		{"2.0.0", "10.0.0", -1}, // numeric, not lexicographic
		{"0.6.0", "0.6", 0},
	}
	for _, c := range cases {
		got, err := CompareVersions(c.a, c.b)
		if err != nil {
			t.Fatalf("CompareVersions(%q, %q): %v", c.a, c.b, err)
		}
		if got != c.want {
			t.Fatalf("CompareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
