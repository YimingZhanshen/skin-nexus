package adminapi

// The panel is the same door as the user centre.
//
// Option B agreed on 2026-11-12 refuses a pending account everything that acts
// as itself — and an admin session acting as itself includes reading the
// texture queue. The one exemption is the super admin, who is the only person
// that can repair the mail transport or mark an account verified by hand; the
// owner being locked out is how a site ends up with nobody who can open it.
//
// The decision itself lives in webapi (verify_gate_web.go) and reaches this
// package as an injected closure, wired once in main. These tests inject it,
// so the panel cannot quietly build its own copy of the rule.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuthRefusesPendingSessionAdmin(t *testing.T) {
	stubSession(t, func(*http.Request) (int64, bool) { return 7, true })
	b := &Backend{VerifyGate: func(_ context.Context, uid int64) bool {
		return uid == 7
	}}
	r := httptest.NewRequest(http.MethodGet, "/api/admin/v1/overview", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "sess-mod"})

	served, _, rec := authServed(t, b, r)
	if served {
		t.Fatal("a pending admin reached the panel handler — the gate stopped at the user centre only")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d %s, want 403", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "auth.email_verify_required") {
		t.Fatalf("body %s should carry auth.email_verify_required so the panel explains itself", rec.Body.String())
	}
}

// The owner is never refused, and the machine channel is not an account: an
// operator holding the admin token without a session is not asked to verify a
// mailbox they may not even have.
func TestAuthKeepsTheSuperAdminAndTheTokenPath(t *testing.T) {
	// Pure machine call: token, no session cookie at all.
	stubSession(t, func(*http.Request) (int64, bool) { return 0, false })
	b := &Backend{AdminTok: "sekret", VerifyGate: func(_ context.Context, uid int64) bool {
		t.Fatalf("VerifyGate ran for a machine call (uid %d) — there is no account behind it", uid)
		return false
	}}
	r := httptest.NewRequest(http.MethodGet, "/api/admin/v1/overview", nil)
	r.Header.Set("X-Admin-Token", "sekret")
	if served, _, rec := authServed(t, b, r); !served || rec.Code != http.StatusOK {
		t.Fatalf("token-only call refused: served=%v code=%d", served, rec.Code)
	}

	// The super admin session is exempt because webapi says so; this package
	// does not know what a super admin is, and must not start guessing.
	stubSession(t, func(*http.Request) (int64, bool) { return 1, true })
	b2 := &Backend{VerifyGate: func(context.Context, int64) bool { return false }}
	r2 := httptest.NewRequest(http.MethodGet, "/api/admin/v1/overview", nil)
	r2.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "sess-owner"})
	if served, _, rec := authServed(t, b2, r2); !served || rec.Code != http.StatusOK {
		t.Fatalf("exempt session refused: served=%v code=%d", served, rec.Code)
	}

	// A backend with no gate wired at all behaves exactly as before, which is
	// what keeps this change off every site that never switched the option on.
	b3 := &Backend{}
	r3 := httptest.NewRequest(http.MethodGet, "/api/admin/v1/overview", nil)
	r3.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "sess-owner"})
	if served, _, rec := authServed(t, b3, r3); !served || rec.Code != http.StatusOK {
		t.Fatalf("VerifyGate nil changed panel behaviour: served=%v code=%d", served, rec.Code)
	}
}

// The panel attaches its stored token to every request, so a browser that once
// pasted one arrives with both credentials and is treated as the person. The
// gate has to hold there too, or a pending moderator keeps the panel by adding
// a header.
func TestAuthGatesTheHybridTokenPlusSessionPath(t *testing.T) {
	stubSession(t, func(*http.Request) (int64, bool) { return 7, true })
	b := &Backend{AdminTok: "sekret", VerifyGate: func(_ context.Context, uid int64) bool {
		return uid == 7
	}}
	r := httptest.NewRequest(http.MethodGet, "/api/admin/v1/overview", nil)
	r.Header.Set("X-Admin-Token", "sekret")
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "sess-mod"})

	served, actor, rec := authServed(t, b, r)
	if served {
		t.Fatalf("hybrid path served a pending admin (actor %d) — the token header is a way around the gate", actor)
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", rec.Code)
	}
}
