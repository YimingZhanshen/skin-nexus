package webapi

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A placeholder inbox (the wizard's old admin@localhost seed) can never receive
// the link. Minting a token for it and dialing SMTP only produces a 502 the
// operator cannot act on; the honest answer names the address shape before any
// token exists. The reset flow keeps its byte-identical answers regardless.
func TestEmailVerifyRequestRefusesPlaceholderRecipient(t *testing.T) {
	h, m := mailFixture(t)
	ctx := context.Background()
	uid, err := m.CreateUser(ctx, "admin@localhost", "placeholder", "x", 0, "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := m.CreateSession(ctx, "sess-ph", uid, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("session: %v", err)
	}
	rec := postWithin(t, h, "/api/v1/email/verify", `{}`, "sess-ph")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("placeholder recipient = %d %s, want 400", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "mail.recipient_placeholder") {
		t.Fatalf("body %s should carry the placeholder code", rec.Body.String())
	}
	var n int
	if err := m.DB().QueryRow(`SELECT COUNT(*) FROM email_verify_tokens`).Scan(&n); err != nil {
		t.Fatalf("count tokens: %v", err)
	}
	if n != 0 {
		t.Fatalf("refused request minted %d tokens — the check must run before CreateEmailVerifyToken", n)
	}
}

// The existence-oracle red line: forgot answers the same bytes for a known and
// an unknown address, and a malformed shape is rejected by the existing rule
// (not by a new placeholder branch that could leak which addresses exist).
func TestForgotPasswordShapeIsUntouched(t *testing.T) {
	h, _ := mailFixture(t)
	a := postWithin(t, h, "/api/v1/password/forgot", `{"email":"own@example.com"}`, "")
	b := postWithin(t, h, "/api/v1/password/forgot", `{"email":"nobody@example.com"}`, "")
	if a.Code != http.StatusOK || b.Code != http.StatusOK {
		t.Fatalf("forgot codes = %d/%d, want 200/200", a.Code, b.Code)
	}
	if a.Body.String() != b.Body.String() {
		t.Fatalf("forgot bodies differ: %s vs %s — that is an account existence oracle", a.Body.String(), b.Body.String())
	}
	c := postWithin(t, h, "/api/v1/password/forgot", `{"email":"admin@localhost"}`, "")
	if c.Code != http.StatusBadRequest {
		t.Fatalf("malformed shape = %d %s, want the existing 400", c.Code, c.Body.String())
	}
}
