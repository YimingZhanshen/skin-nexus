package ugctext

import "testing"

func TestVisibleStripsDirectionalTricks(t *testing.T) {
	cases := []struct{ in, want string }{
		{"‮gnail", "gnail"},
		{"a​b", "ab"},
		{"⁦abc⁩", "abc"},
		{"‪‫‬", ""},
	}
	for _, c := range cases {
		if got := Visible(c.in); got != c.want {
			t.Errorf("Visible(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestVisibleKeepsJoiners(t *testing.T) {
	for _, keep := range []string{"‍", "family‍person", "a‌b"} {
		if got := Visible(keep); got != keep {
			t.Errorf("Visible(%q) = %q; joiners must survive (emoji sequences and Indic scripts use them)", keep, got)
		}
	}
}
