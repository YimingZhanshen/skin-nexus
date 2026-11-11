package webapi

// The web half of the email-verification gate.
//
// `require_verification` used to mean exactly one thing: refuse an unverified
// account in the launcher (ygg.Service.verificationBlocks). The web stayed
// open, so a player could register, upload, wear, read their whole panel and
// only be refused three screens later by a Minecraft client — with nothing on
// the site explaining what they still owed. The webmaster asked for one door
// instead of two, and picked the scope on 2026-11-12 (option B):
//
//	pending = gate on && unverified && grace window spent
//
// A pending subject keeps what a guest already has (public browsing) plus the
// ways out of the state, and loses everything that acts as this account. Both
// halves matter: without the first the site bricks itself for anyone whose
// mail is late or lost, without the second the gate is a sign that says
// "verify" and means "eventually".
//
// The gate-off path returns before touching the store, so a deployment that
// never switched this on pays nothing and sees nothing change.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"skin-nexus/server/internal/accesslog"
	"skin-nexus/server/internal/i18n"
	"skin-nexus/server/internal/store"
	"skin-nexus/server/internal/verifygate"
)

// verifyGrace reads the same option the launcher reads. Nil or non-positive =
// enforce immediately, which is also what "the row is missing" means here:
// a grace window nobody configured is not a window.
func (b *Backend) verifyGrace(ctx context.Context) time.Duration {
	if b.VerifyGrace == nil {
		return 0
	}
	return b.VerifyGrace(ctx)
}

// VerifyPending answers whether this session's account still owes a
// verification click. Exported because the admin panel asks the same question
// about the same person, and main hands it this method as a value: a second
// copy of the rule is how "the panel says no, the profile page says yes" ships. It is exported because the admin panel asks the same
// question of the same account, and main hands this method to it as a value —
// two copies of the rule is how "the user centre says no, the panel says yes"
// ships.
//
// A super admin is never pending, and that exemption is load-bearing rather
// than a courtesy: the only ways to repair a broken mail transport, a wrong
// address, or a spam folder are in the panel. Locking the one account that can
// change the mail settings would leave nobody able to open the door again —
// the same class of hole as the 2026-11-11 wizard that seeded `admin@localhost`
// and mailed a verification link to it. Every other account, permission-1
// staff included, is decided by the launcher's own formula.
func (b *Backend) VerifyPending(ctx context.Context, uid int64) bool {
	pending, _ := b.gateVerdict(ctx, uid)
	return pending
}

// gateVerdict is VerifyPending with the row it decided from: the pending
// screen has to name the mailbox it is waiting on, and asking for the address
// separately would be a second read of a row the gate just read.
//
// The gate-off path returns before touching the store, so an ordinary site
// pays nothing for a feature it never switched on.
func (b *Backend) gateVerdict(ctx context.Context, uid int64) (bool, store.GateState) {
	if !b.verificationEnabled(ctx) {
		return false, store.GateState{}
	}
	gs, err := b.Store.GateState(ctx, uid)
	if err != nil {
		// A gone account is not a pending one; its own handler answers 401.
		// Anything else is a database we cannot read the flag from, and
		// answering "not pending" there would fail open at exactly the moment
		// the truth is unavailable.
		if errors.Is(err, store.ErrNotFound) {
			return false, store.GateState{}
		}
		return true, store.GateState{}
	}
	if gs.Permission >= 2 {
		return false, gs
	}
	// Enabled is taken as true because the only caller path here already read
	// the option and found the gate on; re-reading it would let the two halves
	// of one decision disagree.
	return verifygate.Blocks(verifygate.Facts{
		Enabled:   true,
		Verified:  gs.EmailVerified,
		CreatedAt: gs.CreatedAt,
		Grace:     b.verifyGrace(ctx),
		Now:       time.Now(),
	}), gs
}

// webGateOpenRoutes are the exact "METHOD /path" pairs a pending session may
// still reach. Everything else under /api/v1/ is refused, which is why this
// list is short and why every entry has to say what it is for:
//
//   - reading who you are, and leaving, are never privileges;
//   - the three ways out of the state (resend, change address, delete account)
//     are the point of the screen — without them the gate is a wall, not a
//     door, and a mistyped address becomes permanent;
//   - the exchange route must stay reachable, or the link would 403 the player
//     who finally clicked it;
//   - mail-less recovery and the shell's boot reads are what keep the page
//     renderable enough to show the explanation.
//
// Public browse routes are here too: they already answer for anonymous
// visitors, so a pending subject seeing them grants nothing new.
//
// verify_gate_routes_test.go walks the real route table and fails if a route
// is added without being decided, or if an entry below stops existing.
var webGateOpenRoutes = map[string]string{
	"GET /api/v1/me":               "the shell must be able to ask who is signed in; this is where verifyRequired rides, and refusing it would leave a blank page instead of an explanation",
	"POST /api/v1/logout":          "leaving is never a privilege, and a player who cannot sign out cannot hand the browser to someone else",
	"POST /api/v1/register":        "signup is the state before the gate exists; the email requirement is enforced inside the handler",
	"POST /api/v1/login":           "signing in is what reveals the state; the refusal lands on what you do next, not on the attempt",
	"GET /api/v1/site-config":      "public capability advertisement; the register form reads the gate off this before it can show anything",
	"GET /api/v1/announcement":     "the site notice is addressed to visitors as much as to members, and carries no account data",
	"GET /api/v1/skinlib":          "public browsing (option B): the library answers for anonymous visitors, so a pending subject sees nothing new",
	"GET /api/v1/plugins/ui":       "shell boot metadata: nav entries and page descriptors, no account data",
	"GET /api/v1/setup/status":     "install wizard state, public while the site is empty and harmless afterwards",
	"POST /api/v1/setup":           "install wizard submit: a site that still needs setup has no pending accounts, and the claim is one-way",
	"POST /api/v1/password/forgot": "recovery by mail, pre-session by design; refusing it would strand someone who already cannot read their inbox",
	"POST /api/v1/password/reset":  "recovery by mail, token-scoped; the token is its own proof and burning it must not depend on the gate",
	"POST /api/v1/email/verify":    "resend — first way out of the state, and the only action that can make the block lift",
	"POST /api/v1/email/confirm":   "the link exchange; blocking it would punish the player for finally clicking",
	"PUT /api/v1/me/profile":       "change the address — second way out; it resets the flag and mails a new link, so a typo at signup is recoverable",
	"DELETE /api/v1/me":            "delete the account — third way out, for whoever cannot receive mail at all and should not be held",
}

// Not exempt, on purpose: every `/api/v1/union/*` read requires a session
// (unionUser answers 401 without one), so the hub pages are part of the user
// centre rather than the guest view — unlike the skin library, which is the
// same bytes an anonymous visitor gets.

// webGateExemptPrefix covers non-account surfaces under /api/v1 that a player
// browsing the library still needs (rendered previews of public skins).
func webGateExemptPrefix(path string) bool {
	for _, p := range []string{"/api/v1/texture-render/", "/api/v1/assets/", "/api/v1/render/"} {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// verifyWebGate wraps the /api/v1 mux. Requests with no session pass through
// untouched: an anonymous visitor is already subject to whatever the route
// itself says, and this middleware has no business becoming the identity
// layer. Nor may it decide "who am I" — that stays with currentUser, so the
// gate can never widen what a cookie is allowed to do.
func (b *Backend) verifyWebGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !b.verificationEnabled(r.Context()) {
			next.ServeHTTP(w, r)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/api/v1/") || webGateExemptPrefix(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if _, open := webGateOpenRoutes[r.Method+" "+r.URL.Path]; open {
			next.ServeHTTP(w, r)
			return
		}
		uid, err := b.currentUser(r)
		if err != nil {
			next.ServeHTTP(w, r) // no session: the route's own auth answers
			return
		}
		if !b.VerifyPending(r.Context(), uid) {
			next.ServeHTTP(w, r)
			return
		}
		// Refusals are logged per request rather than deduplicated: an account
		// stuck hitting the panel is the support signal the operator needs to
		// be able to find, and it is the reason this kind exists.
		b.webAccessLog(r, accesslog.Entry{Kind: accesslog.WebVerifyGate,
			Outcome: accesslog.OutcomeDenied, UID: uid,
			Detail: r.Method + " " + r.URL.Path})
		writeErrCode(w, http.StatusForbidden,
			"email verification is required before this account can be used",
			i18n.MsgEmailVerifyRequired)
	})
}
