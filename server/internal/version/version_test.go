package version

// The table every engine in this repository has to agree on. The behavioural
// assertions that keep the three consumers honest live next to each of them
// (marketfmt/version_predicate_test.go, theme/version_predicate_test.go,
// plugin's constraint tests) — this file pins the predicate itself.

import "testing"

func TestParseAcceptsTheFormsPacksActuallyUse(t *testing.T) {
	cases := []struct {
		in   string
		want Version
	}{
		{"1.21", Version{1, 21, 0}},
		{"1.21.0", Version{1, 21, 0}},
		{"0.6.0", Version{0, 6, 0}},
		{"v0.6.0", Version{0, 6, 0}},
		{"V1.2.3", Version{1, 2, 3}},
		{" 1.2.3 ", Version{1, 2, 3}},
		{"2", Version{2, 0, 0}},
		{"0.1", Version{0, 1, 0}},
	}
	for _, c := range cases {
		got, err := Parse(c.in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("Parse(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

// A version this package cannot order must be refused, not read as zero —
// every caller turns an error into a closed gate.
func TestParseRejectsWhatItCannotOrder(t *testing.T) {
	for _, in := range []string{"", "   ", "0-rc1", "1.2.3-beta", "1.2.3+build", "1.2.3.4", "1.x", "1.2.", "abc", "1,2", "-1.2.3", "1.2.3rc1"} {
		if got, err := Parse(in); err == nil {
			t.Fatalf("Parse(%q) = %+v, want an error", in, got)
		}
	}
}

func TestCompareOrdersNumerically(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.21", "1.21.0", 0}, // missing segment is zero
		{"0.6", "0.6.0", 0},
		{"1.0.0", "1.0.1", -1},
		{"1.1.0", "1.0.9", 1},
		{"2.0.0", "10.0.0", -1}, // numeric, not lexicographic
		{"0.6.0", "0.7", -1},
	}
	for _, c := range cases {
		got, err := CompareStrings(c.a, c.b)
		if err != nil {
			t.Fatalf("CompareStrings(%q, %q): %v", c.a, c.b, err)
		}
		if got != c.want {
			t.Fatalf("CompareStrings(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestCompareStringsPropagatesParseErrors(t *testing.T) {
	if _, err := CompareStrings("0-rc1", "1.0.0"); err == nil {
		t.Fatal("an unparseable left side must be an error, not a zero")
	}
	if _, err := CompareStrings("1.0.0", "0-rc1"); err == nil {
		t.Fatal("an unparseable right side must be an error, not a zero")
	}
}
