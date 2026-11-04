package dialect

import "testing"

// A search box that forwards the user's text straight into LIKE turns "%" into
// "match everything" (an unbounded scan behind an anonymous request) and "_"
// into a single-character wildcard, so "a_b" also finds "axb".
func TestLikeArgEscapesWildcards(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "plain"},
		{"50%off", "50!%off"},
		{"a_b", "a!_b"},
		{"100%!(_)", "100!%!!(!_)"},
		{"", ""},
	}
	for _, c := range cases {
		if got := LikeArg(c.in); got != c.want {
			t.Errorf("LikeArg(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestLikePatterns(t *testing.T) {
	if got := LikeContains("a%b"); got != "%a!%b%" {
		t.Fatalf("LikeContains = %q", got)
	}
	if got := LikePrefix("a_b"); got != "a!_b%" {
		t.Fatalf("LikePrefix = %q", got)
	}
}

// The predicate and the escaping are one contract: a LIKE without ESCAPE would
// read the escaped text as wildcards again.
func TestCIContainsSQLCarriesTheEscapeClause(t *testing.T) {
	got := CIContainsSQL("t.description")
	want := "LOWER(t.description) LIKE LOWER(?) ESCAPE '!'"
	if got != want {
		t.Fatalf("CIContainsSQL = %q, want %q", got, want)
	}
	if s := Engine(MySQL).CIContains("x"); s != "LOWER(x) LIKE LOWER(?) ESCAPE '!'" {
		t.Fatalf("MySQL CIContains = %q", s)
	}
}
