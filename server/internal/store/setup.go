package store

// First-run initialization, written as one transaction.
//
// The wizard used to check NeedsSetup (an unlocked COUNT(*)) and then create
// the account. A site becomes reachable from the network well before the
// operator opens the wizard, so two racing POSTs could both pass the check and
// both end up with permission=2. Claiming the one-way marker inside the same
// transaction as the account fixes both halves: only one caller can insert the
// row, and a failure rolls the marker back so the operator can retry.

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"
)

// SetupSpec is everything the install wizard collects in one submission.
type SetupSpec struct {
	Email        string
	Nickname     string
	PasswordHash string
	PlayerLimit  int
	Options      map[string]string
}

// execer is satisfied by both *dbx.DB and *dbx.Tx, so the account insert below
// serves the normal registration path and the setup transaction alike.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	InsertReturningID(ctx context.Context, query string, args ...any) (int64, error)
}

// insertUser writes both timestamps from the caller's clock. Leaving them to
// the column DEFAULT would put the account's age on the database clock while
// the email-verify grace period ages it on the application clock
// (ygg.Service.verificationBlocks) — two clocks, one value.
func insertUser(ctx context.Context, ex execer, email, nickname, passwordHash string, playerLimit, permission int, ip string, now time.Time) (int64, error) {
	uid, err := ex.InsertReturningID(ctx,
		`INSERT INTO users (email, nickname, password, password_algo, player_limit, email_verified, can_join_server, permission, ip, created_at, updated_at)
		 VALUES (?,?,?,'argon2id',?,0,1,?,?,?,?)`,
		nullableStr(email), nickname, passwordHash, playerLimit, permission, nullableStr(ip), now, now)
	if err != nil {
		if isDupKey(err) {
			return 0, ErrDuplicate
		}
		return 0, err
	}
	return uid, nil
}

// ClaimSetup writes the one-way marker, the bootstrap super admin
// (permission=2 — plugin install needs exactly 2, not merely >0) and the
// wizard's options atomically. claimed=false means another caller already
// installed the site; that is a normal outcome, not an error.
func (m *Mysql) ClaimSetup(ctx context.Context, spec SetupSpec) (uid int64, claimed bool, err error) {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // a no-op once committed

	// INSERT ... ON CONFLICT DO NOTHING / INSERT IGNORE: exactly one racing
	// caller sees RowsAffected==1, and no engine-specific error code is sniffed.
	q, err := m.db.Dialect().InsertIgnore("options", []string{"k", "v"})
	if err != nil {
		return 0, false, err
	}
	res, err := tx.ExecContext(ctx, q, "setup_done", "1")
	if err != nil {
		return 0, false, fmt.Errorf("store: claim setup: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, false, nil
	}

	uid, err = insertUser(ctx, tx, spec.Email, spec.Nickname, spec.PasswordHash, spec.PlayerLimit, 2, "", m.now().UTC())
	if err != nil {
		return 0, false, err
	}

	if len(spec.Options) > 0 {
		up, err := m.db.Dialect().Upsert("options", []string{"k", "v"}, []string{"k"}, []string{"v"})
		if err != nil {
			return 0, false, err
		}
		keys := make([]string, 0, len(spec.Options))
		for k := range spec.Options {
			keys = append(keys, k)
		}
		sort.Strings(keys) // deterministic statement order across engines
		for _, k := range keys {
			if _, err := tx.ExecContext(ctx, up, k, spec.Options[k]); err != nil {
				return 0, false, fmt.Errorf("store: setup option %q: %w", k, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	return uid, true, nil
}
