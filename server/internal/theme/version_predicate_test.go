package theme

// version_predicate_test.go — W1-3: the requires gate must fail closed.
//
// satisfiesRequires documents that "unparseable constraints fail closed so a
// typo never silently widens compatibility". It did not: cmpVersion threw
// every Atoi error away and substituted 0, so ">=0-rc1" compared as if it
// were ">=0" and passed against every host ever built, and a host version
// written "0.6.0-dev" compared as 0.6.0 and unlocked themes its real number
// might not satisfy. The shared predicate lives in internal/version; a
// version this engine cannot parse is a version it must not gate on.

import "testing"

func TestRequiresFailsClosedOnUnparseableVersions(t *testing.T) {
	cases := []struct{ constraint, host string }{
		{">=0-rc1", "0.6.0"},      // a prerelease string is not 0
		{">=1.2.3-beta", "0.6.0"}, // trailing garbage must not parse as 3
		{">=1.2.3.4", "1.2.3.4"},  // four segments are not a version
		{">=0.1", "0.6.0-dev"},    // an unparseable HOST is not "anything goes"
		{">=not-a-version", "0.6.0"},
	}
	for _, c := range cases {
		if satisfiesRequires(c.constraint, c.host) {
			t.Fatalf("satisfiesRequires(%q, %q) must fail closed", c.constraint, c.host)
		}
	}
}

// Missing segments still count as zero: every pack in the wild declares
// ">=0.1" and every host is built as x.y.z, so tightening the parser must
// not close the gate on the themes that are installed today.
func TestRequiresTreatsMissingSegmentsAsZero(t *testing.T) {
	pass := [][2]string{
		{">=0.1", "0.6.0"},
		{">=1.21", "1.21.0"},
		{">=0.6", "0.6"},
		{">=1.0", "v1.0.0"},
	}
	for _, c := range pass {
		if !satisfiesRequires(c[0], c[1]) {
			t.Fatalf("satisfiesRequires(%q, %q) must pass — missing segments are zero", c[0], c[1])
		}
	}
	fail := [][2]string{
		{">=0.7", "0.6.0"},
		{">=1.22", "1.21.0"},
	}
	for _, c := range fail {
		if satisfiesRequires(c[0], c[1]) {
			t.Fatalf("satisfiesRequires(%q, %q) must fail", c[0], c[1])
		}
	}
}
