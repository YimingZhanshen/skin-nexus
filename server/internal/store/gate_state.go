package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// GateState is the account side of the email-verification gate, in one read.
//
// The web panel needs three columns to decide it — the flag the link sets, the
// clock the grace window measures from, and the permission that keeps a
// super admin out of a lockout — and they must come from a single statement:
// three separate reads would let a verification landing between them produce a
// request that is both "just verified" and "was blocked", which is exactly the
// kind of answer an operator cannot debug.
//
// It is deliberately not folded into UserBrief (which OIDC calls per token):
// brief carries no created_at, and adding it there would put a gate column on
// a hot path that never reads it.
type GateState struct {
	// Email is here because /me has to name the mailbox the screen is waiting
	// on. Splitting it into a second read of the same row would only double the
	// chance of answering about two different versions of the account.
	Email         string
	EmailVerified bool
	CreatedAt     time.Time
	Permission    int
}

// GateState reads the gate's account facts for one uid. ErrNotFound when the
// row is gone — callers treat that as "not pending", because the identity
// layer already refuses a session whose account vanished.
func (m *Mysql) GateState(ctx context.Context, uid int64) (GateState, error) {
	var g GateState
	err := m.db.QueryRowContext(ctx,
		`SELECT COALESCE(email,''), email_verified, created_at, permission FROM users WHERE id = ?`, uid).
		Scan(&g.Email, &g.EmailVerified, &g.CreatedAt, &g.Permission)
	if errors.Is(err, sql.ErrNoRows) {
		return GateState{}, ErrNotFound
	}
	if err != nil {
		return GateState{}, err
	}
	return g, nil
}

// UnverifiedAccounts is the blast radius of switching the gate on: how many
// accounts would be refused the panel the moment their grace window ends.
// The panel shows it before the operator flips the option, because on a site
// with migrated blessing accounts the honest answer is "most of them".
func (m *Mysql) UnverifiedAccounts(ctx context.Context) (int64, error) {
	var n int64
	if err := m.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users WHERE email_verified = 0`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}
