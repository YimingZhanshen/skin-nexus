package webapi

// verify_web_gate_test.go — the gate now costs what it says on the web side.
//
// Since 2026-11-02 `require_verification` meant one thing: refuse an
// unverified account at the Minecraft door (ygg.Service.verificationBlocks).
// The web stayed open, so a player could sign up, upload, wear, browse the
// whole panel, and only be refused in the launcher — three screens away from
// anything that explained why. The webmaster asked for the door to be the
// same door: past `email_verify_grace_days`, the account is not usable, and
// the site says so where the player is standing.
//
// Scope agreed on 2026-11-12 (option B): a pending subject keeps what a guest
// has (public browsing) plus the four ways out of the state — see
// webGateSelfRescue. Everything that acts *as this account* is refused with
// its own code, so the panel can explain it instead of showing a spinner.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"skin-nexus/server/internal/store"
)

func requestWithin(t *testing.T, h http.Handler, method, path, body, session string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if session != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// accountSeed writes one account (optionally with an admin permission) plus a
// live session for it, and returns the session token. createdOffset backdates
// created_at so a grace window can be crossed without waiting for it.
func accountSeed(t *testing.T, m *store.Mysql, email, nick string, permission int, createdOffset time.Duration) string {
	t.Helper()
	ctx := context.Background()
	var uid int64
	var err error
	if permission >= 2 {
		uid, err = m.CreateUserAdmin(ctx, email, nick, "hash-not-used", 5)
	} else {
		uid, err = m.CreateUser(ctx, email, nick, "hash-not-used", 5, "")
	}
	if err != nil {
		t.Fatalf("create user %s: %v", nick, err)
	}
	if permission == 1 {
		if err := m.SetUserPermission(ctx, uid, 1); err != nil {
			t.Fatalf("grant admin on %d: %v", uid, err)
		}
	}
	if createdOffset != 0 {
		// An absolute timestamp, not `created_at - ?`: the subtraction form
		// needs an interval literal and every engine spells that differently
		// (MySQL a number of seconds, PG an INTERVAL, SQLite a modifier
		// string). Writing the target value works on all three.
		at := time.Now().UTC().Add(-createdOffset).Format("2006-01-02 15:04:05")
		if _, err := m.DB().Exec(`UPDATE users SET created_at = ? WHERE id = ?`, at, uid); err != nil {
			t.Fatalf("backdate created_at: %v", err)
		}
	}
	token := "sess-" + nick
	if err := m.CreateSession(ctx, token, uid, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return token
}

// Tracer: gate on, grace 0, unverified player, valid session. Reading their own
// inbox is an act as this account, so it must be refused — with the code the UI
// needs in order to render the explanation instead of a spinner.
//
// /me/players was the first pick and is not usable here: it delegates to the
// account service, which this fixture never wires, so it answers 500 for a
// reason unrelated to the gate — a test on it could pass for the wrong cause.
// The pre-check below is the same guard: if the route were already refused
// before the middleware existed, this test would prove nothing.
func TestVerifyGateBlocksIdentityRoutesForUnverifiedAccount(t *testing.T) {
	h, m := registerGateFixture(t, true)
	ctx := context.Background()
	sess := accountSeed(t, m, "pending@example.test", "pendingplayer", 0, 0)

	rec := requestWithin(t, h, http.MethodGet, "/api/v1/me/notifications", "", sess)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("pending player reading own inbox = %d %s, want 403 while the gate is unpaid",
			rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "auth.email_verify_required") {
		t.Fatalf("body %s should carry auth.email_verify_required so the UI can explain the state",
			rec.Body.String())
	}

	// The control, in the same test rather than as a pre-check: a pre-check
	// ("was it 200 before the gate existed?") goes stale the moment the gate
	// lands and then fails for being unable to see itself. This half keeps
	// proving the refusal comes from the flag and not from a missing table, a
	// broken session, or a route that never worked in this fixture.
	var uid int64
	if err := m.DB().QueryRowContext(ctx, `SELECT id FROM users WHERE nickname='pendingplayer'`).Scan(&uid); err != nil {
		t.Fatalf("find the seeded account: %v", err)
	}
	if err := m.SetEmailVerified(ctx, uid, true); err != nil {
		t.Fatalf("verify the account: %v", err)
	}
	if after := requestWithin(t, h, http.MethodGet, "/api/v1/me/notifications", "", sess); after.Code != http.StatusOK {
		t.Fatalf("same session after clicking the link = %d %s, want 200 — the gate must open on the flag alone",
			after.Code, after.Body.String())
	}
}

// The grace window is a promise about the web too, not only about joining.
// Two halves in one test because they are one story: an account inside the
// window is usable, an account past it is not, and both are unverified.
func TestVerifyGateHonoursTheGraceWindowOnTheWebToo(t *testing.T) {
	h, m := gateFixture(t, true, 7*24*time.Hour)

	fresh := accountSeed(t, m, "fresh@example.test", "freshplayer", 0, 24*time.Hour)
	if rec := requestWithin(t, h, http.MethodGet, "/api/v1/me/notifications", "", fresh); rec.Code != http.StatusOK {
		t.Fatalf("day-old account inside a 7-day window = %d %s, want 200 — grace is the point of the option",
			rec.Code, rec.Body.String())
	}

	lapsed := accountSeed(t, m, "lapsed@example.test", "lapsedplayer", 0, 8*24*time.Hour)
	if rec := requestWithin(t, h, http.MethodGet, "/api/v1/me/notifications", "", lapsed); rec.Code != http.StatusForbidden {
		t.Fatalf("8-day-old account with a 7-day window = %d %s, want 403", rec.Code, rec.Body.String())
	}
}

// The gate off is the shipped behaviour, and it is the half that keeps a
// thousand ordinary sites from being affected by this change at all.
func TestVerifyGateOffLeavesTheWebUntouched(t *testing.T) {
	h, m := gateFixture(t, false, 0)
	sess := accountSeed(t, m, "free@example.test", "freeplayer", 0, 0)

	if rec := requestWithin(t, h, http.MethodGet, "/api/v1/me/notifications", "", sess); rec.Code != http.StatusOK {
		t.Fatalf("gate off, unverified = %d %s, want 200 — no site that never switched this on may change",
			rec.Code, rec.Body.String())
	}
	me := requestWithin(t, h, http.MethodGet, "/api/v1/me", "", sess)
	if strings.Contains(me.Body.String(), `"verifyRequired":true`) {
		t.Fatalf("/me with the gate off reports verifyRequired: %s", me.Body.String())
	}
}

// A pending subject keeps the ways out and the guest view. Asserted as "the
// gate did not speak" (any status but 403) rather than as success: several of
// these routes have their own rules — a resend against a dead transport, a
// delete that needs a password — and this file must not become the test that
// pins those.
func TestPendingSubjectKeepsTheWaysOut(t *testing.T) {
	h, m := gateFixture(t, true, 0)
	sess := accountSeed(t, m, "stuck@example.test", "stuckplayer", 0, 0)

	calls := []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/me", ""},
		{http.MethodGet, "/api/v1/skinlib", ""},
		{http.MethodGet, "/api/v1/site-config", ""},
		{http.MethodPost, "/api/v1/email/verify", "{}"},
		{http.MethodPut, "/api/v1/me/profile", `{"email":"fixed@example.test"}`},
		{http.MethodPost, "/api/v1/logout", "{}"},
	}
	for _, c := range calls {
		req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sess})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusForbidden {
			t.Fatalf("%s %s refused by the gate: %s — a player who cannot resend, cannot fix the address and cannot leave is locked in, not gated",
				c.method, c.path, rec.Body.String())
		}
	}

	// The address route is here for a second reason: changing it is the only
	// recovery from a typo at signup, so it has to actually land.
	var email string
	if err := m.DB().QueryRow(`SELECT email FROM users WHERE nickname='stuckplayer'`).Scan(&email); err != nil {
		t.Fatalf("read the address back: %v", err)
	}
	if email != "fixed@example.test" {
		t.Fatalf("email = %q, want the new address to be stored so the next link goes somewhere real", email)
	}
}

// /me is how the panel learns the state, so it carries the verdict and not
// just the flag: an account inside the window must not be told it is blocked.
func TestMeCarriesTheGateVerdict(t *testing.T) {
	blocked, bm := gateFixture(t, true, 0)
	sess := accountSeed(t, bm, "verdict@example.test", "verdictplayer", 0, 0)
	rec := requestWithin(t, blocked, http.MethodGet, "/api/v1/me", "", sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("/me = %d %s, want 200 (the shell needs this to draw the screen)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"verifyRequired":true`) {
		t.Fatalf("/me body %s should say verifyRequired true", rec.Body.String())
	}
	// The address rides along only while the block is real. This was not true
	// on the first run of the rig: the screen rendered "the link is on its way
	// to" and then nothing, because login and register answers carry no email
	// field. A gate that cannot name the mailbox is not a help.
	if !strings.Contains(rec.Body.String(), "verdict@example.test") {
		t.Fatalf("/me for a pending account should name the address it is waiting on: %s", rec.Body.String())
	}

	inWindow, im := gateFixture(t, true, 7*24*time.Hour)
	sess2 := accountSeed(t, im, "graced@example.test", "gracedplayer", 0, 24*time.Hour)
	rec2 := requestWithin(t, inWindow, http.MethodGet, "/api/v1/me", "", sess2)
	if strings.Contains(rec2.Body.String(), `"verifyRequired":true`) {
		t.Fatalf("/me inside the window says verifyRequired: %s", rec2.Body.String())
	}
	// …and an account that is merely unverified, not blocked, keeps the /me
	// body it has always had: no address newly exposed to a site that never
	// asked for this feature.
	if strings.Contains(rec2.Body.String(), "graced@example.test") {
		t.Fatalf("/me inside the window should not start exposing the address: %s", rec2.Body.String())
	}
}

// The exemption is for the one account that can repair the mail transport, not
// for "anyone with a badge". A permission-1 moderator who has not verified is
// still a player who owes a click.
func TestVerifyGateExemptsOnlyTheSuperAdmin(t *testing.T) {
	h, m := gateFixture(t, true, 0)

	owner := accountSeed(t, m, "owner@example.test", "owneradmin", 2, 0)
	if rec := requestWithin(t, h, http.MethodGet, "/api/v1/me/notifications", "", owner); rec.Code == http.StatusForbidden {
		t.Fatalf("super admin refused by the gate: %s — with mail broken there would be nobody left who can fix it", rec.Body.String())
	}

	mod := accountSeed(t, m, "mod@example.test", "modadmin", 1, 0)
	if rec := requestWithin(t, h, http.MethodGet, "/api/v1/me/notifications", "", mod); rec.Code != http.StatusForbidden {
		t.Fatalf("permission-1 admin = %d %s, want 403 — staff is not a second owner account", rec.Code, rec.Body.String())
	}
}

// The refusal happens before the work: a pending request must not create a
// row, half-apply a change, or land in the outbox.
func TestPendingRefusalWritesNothing(t *testing.T) {
	h, m := gateFixture(t, true, 0)
	sess := accountSeed(t, m, "writes@example.test", "writesplayer", 0, 0)

	rec := requestWithin(t, h, http.MethodPost, "/api/v1/me/players", `{"name":"ghostplayer"}`, sess)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("create player while pending = %d %s, want 403", rec.Code, rec.Body.String())
	}
	var n int
	if err := m.DB().QueryRow(`SELECT COUNT(*) FROM players`).Scan(&n); err != nil {
		t.Fatalf("count players: %v", err)
	}
	if n != 0 {
		t.Fatalf("players = %d after a refusal, want 0", n)
	}
}

// Anonymous stays anonymous: the gate must not become the identity layer, and
// a route that wants a session has to keep answering with its own 401.
func TestAnonymousRequestsAreNotTheGatesBusiness(t *testing.T) {
	h, _ := gateFixture(t, true, 0)
	rec := requestWithin(t, h, http.MethodGet, "/api/v1/me/notifications", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session = %d %s, want the route's own 401 rather than a gate answer", rec.Code, rec.Body.String())
	}
}

// The two answers the shell reads before it draws anything. /me alone would be
// enough for a reload, but login and register are how a player *arrives*: with
// the verdict missing there, the first screen after 注册 is a normal-looking
// dashboard whose every button is about to 403.
//
// Both halves go through the real registration path. accountSeed writes a
// placeholder digest into the password column, so an account seeded that way
// can never sign in — asserting on it would have tested a 401 nobody sees.
func TestLoginAndRegisterCarryTheVerdictToo(t *testing.T) {
	h, _ := gateFixture(t, true, 0)
	reg := requestWithin(t, h, http.MethodPost, "/api/v1/register",
		`{"email":"newcomer@example.test","nickname":"newcomer","password":"password123"}`, "")
	if reg.Code != http.StatusCreated {
		t.Fatalf("register = %d %s, want 201 with an address supplied", reg.Code, reg.Body.String())
	}
	if !strings.Contains(reg.Body.String(), `"verifyRequired":true`) {
		t.Fatalf("register response %s should carry verifyRequired so the shell lands on the right screen", reg.Body.String())
	}

	log := requestWithin(t, h, http.MethodPost, "/api/v1/login",
		`{"identifier":"newcomer","password":"password123"}`, "")
	if log.Code != http.StatusOK {
		t.Fatalf("login = %d %s, want 200 — the gate refuses what you do next, not signing in", log.Code, log.Body.String())
	}
	if !strings.Contains(log.Body.String(), `"verifyRequired":true`) {
		t.Fatalf("login response %s should carry verifyRequired", log.Body.String())
	}

	// The same two answers with the gate off must not claim anybody is blocked.
	off, _ := gateFixture(t, false, 0)
	if r := requestWithin(t, off, http.MethodPost, "/api/v1/register",
		`{"email":"free2@example.test","nickname":"free2","password":"password123"}`, ""); r.Code != http.StatusCreated ||
		strings.Contains(r.Body.String(), `"verifyRequired":true`) {
		t.Fatalf("gate off register = %d %s, want 201 and no verdict claim", r.Code, r.Body.String())
	}
	if r := requestWithin(t, off, http.MethodPost, "/api/v1/login",
		`{"identifier":"free2","password":"password123"}`, ""); r.Code != http.StatusOK ||
		strings.Contains(r.Body.String(), `"verifyRequired":true`) {
		t.Fatalf("gate off login = %d %s, want 200 and no verdict claim", r.Code, r.Body.String())
	}
}
