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

// A local part in any script must survive masking as the character it is:
// slicing bytes off a multi-byte rune puts an invalid byte into the log line.
func TestEmailMasksWholeFirstRune(t *testing.T) {
	cases := map[string]string{
		"测试号@example.com":    "测***@example.com",
		"שeldon@example.com": "ש***@example.com",
	}
	for in, want := range cases {
		if got := Email(in); got != want {
			t.Errorf("Email(%q) = %q, want %q", in, got, want)
		}
	}
}

// The login form takes "email or nickname"; only the first is a permanent
// copy of personal data, so the nickname has to come back unchanged.
func TestIdentifierMasksOnlyTheEmail(t *testing.T) {
	cases := map[string]string{
		"someone@example.com": "s***@example.com",
		"steve":               "steve",
		"玩家_01":               "玩家_01",
		"":                    "",
	}
	for in, want := range cases {
		if got := Identifier(in); got != want {
			t.Errorf("Identifier(%q) = %q, want %q", in, got, want)
		}
	}
}
