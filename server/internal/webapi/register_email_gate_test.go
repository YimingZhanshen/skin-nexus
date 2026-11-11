package webapi

// register_email_gate_test.go — the verification gate has to be paid for at
// the door it guards.
//
// require_verification is a JOIN gate: it refuses an unverified account in
// ygg.Service.verificationBlocks (service.go:296). It never touched
// registration, and registration accepts an empty email by design
// (webapi.go:608 only validates a non-empty one). The two together produced the
// complaint the webmaster filed on 2026-11-12: with the gate on and a zero
// grace period, a player could sign up without an address, browse the whole
// site, and only be refused at the server door — with no mail to verify, no
// warning at signup, and no path they could find on their own.
//
// So when the gate is on, an address is required at signup. The refusal must
// happen before any row is written, and the mail that the gate depends on is
// already sent by the existing best-effort branch (webapi.go:715) — which is
// why this test asserts the two states separately: the gate off must still
// allow an empty address, byte for byte as before.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"skin-nexus/server/internal/dialect"
	"skin-nexus/server/internal/migrate"
	"skin-nexus/server/internal/store"
)

// registerGateFixture builds a backend with the gate in one state and no grace
// window. SMTP points at the blackhole listener so the registration send has a
// transport to try; the tests below never wait on it.
func registerGateFixture(t *testing.T, gateOn bool) (http.Handler, *store.Mysql) {
	t.Helper()
	return gateFixture(t, gateOn, 0)
}

// gateFixture is the shared webapi gate fixture. grace is `email_verify_grace_
// days` as the deployment sees it, so one file can prove both halves of the
// window without waiting for a day to pass.
func gateFixture(t *testing.T, gateOn bool, grace time.Duration) (http.Handler, *store.Mysql) {
	t.Helper()
	m, err := store.Open("sqlite", "sqlite:"+filepath.Join(t.TempDir(), "gate.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	if err := migrate.ApplySQLDirTracked(m.DB(), filepath.Join("..", "..", "migrations"), dialect.SQLite); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	ctx := context.Background()
	host, portStr := blackholeSMTP(t)
	p, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("port: %v", err)
	}
	for k, v := range map[string]string{
		"smtp_host": host, "smtp_port": strconv.Itoa(p),
		"smtp_user": "site@example.test", "smtp_pass": "secret", "smtp_from": "site@example.test",
		"site_name": "Rig",
	} {
		if err := m.SetOption(ctx, k, v); err != nil {
			t.Fatalf("set %s: %v", k, err)
		}
	}
	b := &Backend{Store: m, BaseURL: "http://rig.test",
		VerificationEnabled: func(context.Context) bool { return gateOn },
		VerifyGrace:         func(context.Context) time.Duration { return grace }}
	// The fixture proves its own transport is readable before a test blames
	// the handler for a missing option.
	if b.smtpConfig(ctx) == nil {
		t.Fatal("fixture: smtp options did not produce a mailer config — every send path below would be unreachable")
	}
	return b.Handler(), m
}

func postRegister(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func userCount(t *testing.T, m *store.Mysql) int {
	t.Helper()
	var n int
	if err := m.DB().QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		t.Fatalf("count users: %v", err)
	}
	return n
}

func registerBody(email, nickname string) string {
	return `{"email":"` + email + `","nickname":"` + nickname + `","password":"password123"}`
}

// The gate on, no address: refused with its own code, and no account written.
func TestRegisterRequiresEmailWhenGateIsOn(t *testing.T) {
	h, m := registerGateFixture(t, true)
	rec := postRegister(t, h, registerBody("", "noemail"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty email with the gate on = %d %s, want 400", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "auth.email_required") {
		t.Fatalf("body %s should name auth.email_required so the form can say why", rec.Body.String())
	}
	if n := userCount(t, m); n != 0 {
		t.Fatalf("users = %d, want 0 — a refusal must not create an account", n)
	}
}

// The gate off is the shipped behaviour: an empty address still registers.
// Without this half, "we now require email" would be true for every site.
func TestRegisterStillAllowsEmptyEmailWhenGateIsOff(t *testing.T) {
	h, m := registerGateFixture(t, false)
	rec := postRegister(t, h, registerBody("", "freeemail"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("empty email with the gate off = %d %s, want 201", rec.Code, rec.Body.String())
	}
	if n := userCount(t, m); n != 1 {
		t.Fatalf("users = %d, want 1 — the account should exist", n)
	}
}

// A malformed address keeps answering with the existing code: the new branch
// must not swallow it, or the form loses the difference between "missing" and
// "typo".
func TestRegisterBadEmailStillReportsEmailInvalid(t *testing.T) {
	h, _ := registerGateFixture(t, true)
	rec := postRegister(t, h, registerBody("not-an-email", "typo"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("= %d %s, want 400", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "auth.email_invalid") {
		t.Fatalf("body %s should keep auth.email_invalid", rec.Body.String())
	}
}

// What requiring the address actually buys: the account lands with a real,
// still-unverified address, which is the precondition the gate's own mail
// branch (webapi.go:715) needs. The send itself is deliberately NOT asserted
// here — it is detached, and t.Cleanup closes the store before the goroutine
// reaches it, so a token-count assertion in this shape is a race, not a test.
// The send path has its own coverage in mail_detach_test.go.
func TestRegisterWithGateOnStoresVerifiableAddress(t *testing.T) {
	h, m := registerGateFixture(t, true)
	rec := postRegister(t, h, registerBody("player@example.test", "verifiedsoon"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("= %d %s, want 201", rec.Code, rec.Body.String())
	}
	var email string
	var verified int
	if err := m.DB().QueryRow(`SELECT email, email_verified FROM users WHERE nickname='verifiedsoon'`).Scan(&email, &verified); err != nil {
		t.Fatalf("read back the account: %v", err)
	}
	if email != "player@example.test" {
		t.Fatalf("stored email = %q, want the address the gate needs to mail", email)
	}
	if verified != 0 {
		t.Fatalf("email_verified = %d on a brand-new account, want 0 — the gate would be satisfied before the player clicked anything", verified)
	}
}
