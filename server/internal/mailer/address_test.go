package mailer

import "testing"

// The wizard used to seed admin@localhost, an address no mail server will ever
// accept (exmail answers RCPT with 501 Bad address syntax). The set of shapes
// we refuse is CLOSED on purpose: a LAN relay can legitimately deliver to
// .local / .internal / .test, and refusing those would break a working intranet
// deployment. Only dot-less domains and the literal localhost are placeholders.
func TestIsPlaceholderAddress(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"admin@localhost", true},
		{"a@LOCALHOST", true},
		{"x@localhost", true},
		{"u@nodot", true},
		{"", true},
		{"a@b.c", false},
		{"a@sub.example.com", false},
		{"a@foo.local", false},
		{"a@box.internal", false},
		{"a@rig.test", false},
		{"a@127.0.0.1", false}, // dotted: a different problem, not this closed set
	}
	for _, c := range cases {
		if got := IsPlaceholderAddress(c.in); got != c.want {
			t.Errorf("IsPlaceholderAddress(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
