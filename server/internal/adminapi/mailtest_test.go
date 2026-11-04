package adminapi

// Tests for the panel test-send (U17). The success path is the mailer's own
// business (internal/mailer has the fake SMTP server); these pin the guards
// this endpoint exists to have: who may call it, who it may write to, and what
// it reports when the transport refuses.

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"skin-nexus/server/internal/mailer"
)

func TestMaskEmail(t *testing.T) {
	cases := []struct{ in, want string }{
		{"admin@example.com", "a***@example.com"},
		{"root16@skin.example.test", "r***@skin.example.test"},
		{"a@b.co", "a***@b.co"},
		{"no-at-sign", "***"},
		{"@broken.example", "***"},
		{"", "***"},
	}
	for _, c := range cases {
		if got := maskEmail(c.in); got != c.want {
			t.Errorf("maskEmail(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSenderOfFallsBackToUser(t *testing.T) {
	// Mirrors mailer.Config.Send: the header and envelope name the account when
	// no explicit From is set, so the test letter must say what was really used.
	if got := senderOf(&mailer.Config{User: "account@example.com"}); got != "account@example.com" {
		t.Errorf("senderOf without From = %q, want the account", got)
	}
	cfg := &mailer.Config{User: "account@example.com", From: "site@example.com"}
	if got := senderOf(cfg); got != "site@example.com" {
		t.Errorf("senderOf with From = %q, want the explicit sender", got)
	}
}

func TestMailTestRequiresWebSession(t *testing.T) {
	b := newDiagBackend(t)
	w := httptest.NewRecorder()
	// No actor in the context = the X-Admin-Token path.
	b.handleMailTest(w, httptest.NewRequest("POST", "/api/admin/v1/mail-test", nil))
	if w.Code != 403 {
		t.Fatalf("token call = %d %s, want 403", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "admin.web_session_required") {
		t.Errorf("body %s should carry the translated code", w.Body.String())
	}
}

func TestMailTestReportsUnconfiguredSite(t *testing.T) {
	b, uid := mailTestBackend(t, true)
	w := httptest.NewRecorder()
	b.handleMailTest(w, mailTestRequest(uid))
	if w.Code != 503 {
		t.Fatalf("unconfigured site = %d %s, want 503", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "mail.admin_not_configured") {
		t.Errorf("body %s should say the site has no mail account", w.Body.String())
	}
}

func TestMailTestChecksConfigBeforeAddress(t *testing.T) {
	ctx := context.Background()
	// An admin with no address and no mail account: the operator's question is
	// "can this site send at all", so that is the answer even though the caller
	// is also missing an inbox.
	b, uid := mailTestBackend(t, false)
	w := httptest.NewRecorder()
	b.handleMailTest(w, mailTestRequest(uid))
	if w.Code != 503 || !strings.Contains(w.Body.String(), "mail.admin_not_configured") {
		t.Fatalf("no config + no address = %d %s, want 503 mail.admin_not_configured", w.Code, w.Body.String())
	}
	// With a mail account configured, the same caller learns about the address.
	if err := b.Store.SetOption(ctx, "smtp_host", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if err := b.Store.SetOption(ctx, "smtp_from", "site@example.com"); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	b.handleMailTest(w, mailTestRequest(uid))
	if w.Code != 400 || !strings.Contains(w.Body.String(), "email.not_set") {
		t.Fatalf("configured + no address = %d %s, want 400 email.not_set", w.Code, w.Body.String())
	}
}

func TestMailTestReportsTransportFailure(t *testing.T) {
	ctx := context.Background()
	// A closed port: the dial fails immediately, which is the whole point of
	// this endpoint (password reset used to answer 200 while nothing arrived).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	b, uid := mailTestBackend(t, true)
	for _, p := range [][2]string{
		{"smtp_host", "127.0.0.1"},
		{"smtp_port", strconv.Itoa(port)},
		{"smtp_from", "site@example.com"},
	} {
		if err := b.Store.SetOption(ctx, p[0], p[1]); err != nil {
			t.Fatal(err)
		}
	}

	w := httptest.NewRecorder()
	b.handleMailTest(w, mailTestRequest(uid))
	if w.Code != 502 {
		t.Fatalf("dead smtp = %d %s, want 502", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "mail.send_failed") {
		t.Errorf("body %s should carry the transport code", w.Body.String())
	}
	// Never the recipient in the clear, and never a bare 200 either.
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %s: %v", w.Body.String(), err)
	}
	if strings.Contains(w.Body.String(), "@") {
		t.Errorf("response %s should not echo an address", w.Body.String())
	}
	// The raw transport error belongs to the audit row and the log.
	entries, _, err := b.Store.AdminAudit(ctx, "", "", 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range entries {
		if e["action"] != "mail_test_failed" {
			continue
		}
		found = true
		details, _ := json.Marshal(e)
		if !strings.Contains(string(details), "127.0.0.1") {
			t.Errorf("audit row %s should name the host", details)
		}
	}
	if !found {
		t.Errorf("a failed test send must be audited, got %v", entries)
	}
}

// ---- fixtures --------------------------------------------------------------

// mailTestBackend is a store-backed admin backend with one super-admin account;
// withEmail=false creates it the way the wizard's empty-address branch would.
func mailTestBackend(t *testing.T, withEmail bool) (*Backend, int64) {
	t.Helper()
	b := newDiagBackend(t)
	ctx := context.Background()
	email := ""
	if withEmail {
		email = fmt.Sprintf("admin%d@example.com", 1)
	}
	uid, err := b.Store.CreateUserAdmin(ctx, email, "mailtest_admin", "x", 3)
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	brief, err := b.Store.UserBrief(ctx, uid)
	if err != nil {
		t.Fatalf("user brief: %v", err)
	}
	if got := strings.TrimSpace(brief.Email); (withEmail && got == "") || (!withEmail && got != "") {
		t.Fatalf("fixture address = %q, wantEmail=%v", got, withEmail)
	}
	return b, uid
}

func mailTestRequest(uid int64) *http.Request {
	r := httptest.NewRequest("POST", "/api/admin/v1/mail-test", nil)
	return r.WithContext(context.WithValue(r.Context(), actorUIDKey{}, uid))
}
