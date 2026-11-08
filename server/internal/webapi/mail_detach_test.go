package webapi

// A wedged SMTP host used to hang two browser-facing requests: the mailer runs
// a 10s dial + 30s session, while the SPA aborts its own requests at 15s
// (web/src/api.js). Players watched a spinner turn into "network error" for an
// action the server was still performing — and the honest answer, where one
// existed, arrived too late to be read.
//
// The blackhole listener is the independent truth: it accepts and never greets,
// so any send costs the full window. An answer inside a few seconds therefore
// proves the request did not wait on the transport.

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"skin-nexus/server/internal/dialect"
	"skin-nexus/server/internal/migrate"
	"skin-nexus/server/internal/store"
)

// smtpAbortWindow is how long the tests are willing to wait for a handler
// before calling it wedged. The SPA's own client gives up at 15s, so anything
// past a couple of seconds is already the defect.
const smtpAbortWindow = 3 * time.Second

// blackholeSMTP accepts connections and never speaks, holding each one until
// the test ends.
func blackholeSMTP(t *testing.T) (host, port string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var mu sync.Mutex
	var open []net.Conn
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range open {
			_ = c.Close()
		}
	})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			open = append(open, conn)
			mu.Unlock()
		}
	}()
	h, p, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	return h, p
}

func mailFixture(t *testing.T) (http.Handler, *store.Mysql) {
	t.Helper()
	m, err := store.Open("sqlite", "sqlite:"+filepath.Join(t.TempDir(), "mail.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	if err := migrate.ApplySQLDirTracked(m.DB(), filepath.Join("..", "..", "migrations"), dialect.SQLite); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	ctx := context.Background()
	uid, err := m.CreateUser(ctx, "own@example.com", "owner", "x", 3, "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := m.CreateSession(ctx, "sess-own", uid, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("create session: %v", err)
	}
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
	return (&Backend{Store: m, BaseURL: "http://rig.test"}).Handler(), m
}

// postWithin answers or fails: a handler that does not reply inside
// smtpAbortWindow is treated as wedged on the transport.
func postWithin(t *testing.T, h http.Handler, path, body, session string) *httptest.ResponseRecorder {
	t.Helper()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if session != "" {
			req.AddCookie(&http.Cookie{Name: "skin_session", Value: session})
		}
		h.ServeHTTP(rec, req)
		done <- rec
	}()
	select {
	case rec := <-done:
		return rec
	case <-time.After(smtpAbortWindow):
		t.Fatalf("%s did not answer within %v against a silent SMTP — the request waited on the mail transport", path, smtpAbortWindow)
		return nil
	}
}

// The reset token is the deliverable and the response shape is deliberately
// send-independent (no enumeration), so nothing about the answer needs the
// transport: the request must return and let the send finish on its own.
func TestForgotPasswordDoesNotWaitOnSMTP(t *testing.T) {
	h, m := mailFixture(t)
	rec := postWithin(t, h, "/api/v1/password/forgot", `{"email":"own@example.com"}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("forgot = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	var n int
	if err := m.DB().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM password_reset_tokens`).Scan(&n); err != nil {
		t.Fatalf("count reset tokens: %v", err)
	}
	if n != 1 {
		t.Fatalf("password_reset_tokens rows = %d, want the one token the player was promised", n)
	}
}

// Unlike reset, this endpoint answers the owner of the mailbox, so it keeps the
// honest failure — but honesty past the browser's window is indistinguishable
// from a hang, so its send must run on a budget the caller can still read.
func TestEmailVerifyRequestAnswersInsideTheBrowserWindow(t *testing.T) {
	h, _ := mailFixture(t)
	budget := mailReplyBudget
	mailReplyBudget = 300 * time.Millisecond
	t.Cleanup(func() { mailReplyBudget = budget })

	start := time.Now()
	rec := postWithin(t, h, "/api/v1/email/verify", `{}`, "sess-own")
	elapsed := time.Since(start)

	if elapsed > smtpAbortWindow {
		t.Fatalf("resend took %v, want the mailReplyBudget window %v to bound it", elapsed, mailReplyBudget)
	}
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("wedged SMTP = %d, want 502; body %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	// The stable dictionary code for "the mail transport failed" (i18n
	// MsgEmailSendFailed spells it email.verification_failed).
	if body.Code != "email.verification_failed" {
		t.Fatalf("code = %q, want email.verification_failed", body.Code)
	}
	if strings.Contains(strings.ToLower(rec.Body.String()), "smtp") {
		t.Fatalf("the response leaked transport detail: %s", rec.Body.String())
	}
}
