package redact

import "testing"

func TestEmail(t *testing.T) {
	cases := map[string]string{
		"notch@example.com": "n***@example.com",
		"a@b.co":            "a***@b.co",
		"":                  "***",
		"no-at-sign":        "***",
		"@weird.com":        "***",
	}
	for in, want := range cases {
		if got := Email(in); got != want {
			t.Errorf("Email(%q) = %q, want %q", in, got, want)
		}
	}
}
