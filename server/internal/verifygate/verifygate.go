// Package verifygate holds the one definition of the email-verification gate.
//
// Before 2026-11-12 the sentence "this site verifies email before joining"
// lived in ygg.Service.verificationBlocks only, and the web had no copy of the
// rule at all — which is how a player could satisfy every requirement of the
// web and still be refused by the server, three screens away from any
// explanation. Both surfaces now decide here, so the grace window and the
// fail-closed case cannot drift into two different meanings.
package verifygate

import "time"

// Facts is everything the decision reads. Two come from the account (Verified,
// CreatedAt) and three from the deployment (Enabled, Grace, Now), so callers
// resolve them — this package never reaches for a store, an options table or a
// request, which is what lets the launcher and the panel prove they agree.
type Facts struct {
	// Enabled is the live state of option `require_verification`.
	Enabled bool
	// Verified is users.email_verified.
	Verified bool
	// CreatedAt is users.created_at. A zero value means the caller never read
	// the column: treated as "no grace", so the gate fails closed rather than
	// handing an infinite window to whoever forgot the field.
	CreatedAt time.Time
	// Grace is option `email_verify_grace_days` as a duration. Zero or
	// negative means enforce immediately.
	Grace time.Duration
	// Now is the clock, injected so a test can cross the window without
	// sleeping. A zero Now falls back to the wall clock rather than blocking,
	// because "caller forgot the field" is a bug, not a policy.
	Now time.Time
}

// Blocks reports the whole gate: the option is on, the address is unverified,
// and the grace window is spent.
func Blocks(f Facts) bool {
	if !f.Enabled || f.Verified {
		return false
	}
	if f.Now.IsZero() {
		f.Now = time.Now()
	}
	if f.Grace > 0 && !f.CreatedAt.IsZero() && f.Now.Sub(f.CreatedAt) < f.Grace {
		return false
	}
	return true
}
