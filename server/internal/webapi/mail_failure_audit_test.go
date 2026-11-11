package webapi

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"skin-nexus/server/internal/store"
)

// refuseSMTP accepts and immediately closes: the dial succeeds but the greeting
// fails at once, so a detached send fails in milliseconds instead of burning
// the whole 30s session window inside a test.
func refuseSMTP(t *testing.T) (host, port string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	h, p, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return h, p
}

func auditFixture(t *testing.T) (http.Handler, *store.Mysql) {
	t.Helper()
	h, m := mailFixture(t)
	host, port := refuseSMTP(t)
	p, _ := strconv.Atoi(port)
	ctx := context.Background()
	if err := m.SetOption(ctx, "smtp_host", host); err != nil {
		t.Fatal(err)
	}
	if err := m.SetOption(ctx, "smtp_port", strconv.Itoa(p)); err != nil {
		t.Fatal(err)
	}
	return h, m
}

// A player-side send failure used to vanish into the log: the operator only
// learned about it when a player reported "I never got the mail". Both flows
// now leave an audit row with a masked address, and the reset response shape
// stays byte-identical (existence-oracle red line).
func TestForgotPasswordFailureIsAudited(t *testing.T) {
	h, m := auditFixture(t)
	rec := postWithin(t, h, "/api/v1/password/forgot", `{"email":"own@example.com"}`, "")
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"ok":true}` {
		t.Fatalf("forgot = %d %s, want the unchanged oracle-safe 200", rec.Code, rec.Body.String())
	}
	row := waitForAudit(t, m, "password_reset_mail_failed")
	if !strings.Contains(row, "o***@example.com") {
		t.Errorf("audit detail must mask the address, got %s", row)
	}
	if strings.Contains(row, "own@example.com") {
		t.Errorf("audit detail leaked the full address: %s", row)
	}
}

func TestVerifyRequestFailureIsAudited(t *testing.T) {
	h, m := auditFixture(t)
	rec := postWithin(t, h, "/api/v1/email/verify", `{}`, "sess-own")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("verify request against dead SMTP = %d, want 502", rec.Code)
	}
	row := waitForAudit(t, m, "email_verify_mail_failed")
	if !strings.Contains(row, "o***@example.com") {
		t.Errorf("audit detail must mask the address, got %s", row)
	}
}

func waitForAudit(t *testing.T, m *store.Mysql, action string) string {
	t.Helper()
	for i := 0; i < 40; i++ {
		entries, _, err := m.AdminAudit(context.Background(), "", action, 1, 5)
		if err == nil && len(entries) > 0 {
			b, _ := json.Marshal(entries[0])
			return string(b)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no %s audit row within 2s", action)
	return ""
}
