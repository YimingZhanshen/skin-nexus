package accesslog

import (
	"strings"
	"testing"
)

// The 2026-11-09 rig run found this: a diagnostic detail row's body_excerpt
// held the authenticate request body verbatim, which means the account PASSWORD
// in plaintext — and the body column follows the 7..90 day retention clock, not
// the 24h credential clock. "The reader API never returns a credential" was
// true of credential_ref and false of the body next to it. These tests pin the
// scrub at the one gate every writer passes through (Sanitized).

func TestSanitizedScrubsPasswordShapedFields(t *testing.T) {
	const secret = "hunter2-not-a-real-password"
	cases := []struct {
		name string
		body string
	}{
		{"yggdrasil authenticate", `{"username":"rig@localhost","password":"` + secret + `"}`},
		{"web login", `{"identifier":"rig@localhost","password":"` + secret + `"}`},
		{"nested object", `{"a":{"password":"` + secret + `"}}`},
		{"inside an array", `{"items":[{"token":"` + secret + `"}]}`},
		{"refresh with old and new", `{"oldPassword":"` + secret + `","newPassword":"` + secret + `"}`},
		{"spacing varies", `{ "Password" : "` + secret + `" }`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := make(map[string][]string)
			out := (&Capture{Body: tc.body, Headers: h}).Sanitized()
			if strings.Contains(out.Body, secret) {
				t.Fatalf("secret survived the scrub: %q", out.Body)
			}
			if out.Body == "" {
				t.Fatalf("the body was dropped entirely: %q — keep the shape, lose the value", tc.body)
			}
		})
	}
}

// A credential name inside an unparseable body must not become a way to store
// the value anyway: the safe answer there is "no body", said out loud.
func TestSanitizedDropsUnparseableBodiesThatMentionSecrets(t *testing.T) {
	const secret = "leak-me"
	out := (&Capture{Body: `password=` + secret + `&username=rig`}).Sanitized()
	if strings.Contains(out.Body, secret) {
		t.Fatalf("form-encoded secret leaked: %q", out.Body)
	}
	if out.Body == "" {
		t.Error("the row should say WHY the body is gone, not show an empty column")
	}
	if !strings.Contains(strings.ToLower(out.Body), "redact") {
		t.Errorf("body = %q, want a visible redaction marker", out.Body)
	}
}

// Over-redaction is the other failure mode: the body excerpt is the reason an
// operator opened the window. Non-secret fields must come back untouched.
func TestSanitizedKeepsNonSecretFieldsReadable(t *testing.T) {
	body := `{"username":"rig@localhost","requestUser":true,"client":"Minecraft","count":3,"agent":{"name":"X","version":2}}`
	out := (&Capture{Body: body}).Sanitized()
	if out.Body != body {
		t.Errorf("a body with no secret was rewritten:\n got %q\nwant %q", out.Body, body)
	}
	benign := "GET /api/v1/ping with an empty query"
	if got := (&Capture{Body: benign}).Sanitized(); got.Body != benign {
		t.Errorf("plain-text body changed: %q", got.Body)
	}
}

// The scrub is idempotent, because InsertAccessDetail sanitizes a second time
// and a marker must not be mistaken for content that still needs redacting.
func TestSanitizedScrubIsIdempotent(t *testing.T) {
	once := (&Capture{Body: `{"password":"abc123","username":"rig"}`}).Sanitized()
	twice := once.Sanitized()
	if twice.Body != once.Body {
		t.Errorf("second pass changed the body: %q -> %q", once.Body, twice.Body)
	}
}

// The list of names is a gate, not a hint: a key nobody thought of is still
// caught when the body cannot be parsed, and the exported helper lets the docs
// and tests name exactly what is treated as secret-shaped.
func TestSecretFieldNamesCoverTheKnownShapes(t *testing.T) {
	want := []string{"password", "oldPassword", "newPassword", "accessToken", "clientToken", "securityToken", "refreshToken", "authorization", "secret", "token"}
	have := map[string]bool{}
	for _, k := range SecretFieldNames() {
		have[strings.ToLower(k)] = true
	}
	for _, k := range want {
		if !have[strings.ToLower(k)] {
			t.Errorf("secret field list is missing %q", k)
		}
	}
}
