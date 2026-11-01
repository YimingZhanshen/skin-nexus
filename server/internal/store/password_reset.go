package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Password reset tokens (A1 找回密码): digest-only storage, single-use,
// Go-clock TTL. Every deadline and comparison binds a Go time (m.now()), so
// writes and reads use one clock — the invariant that keeps the three engines
// alike. A replayed link past its deadline still finds nothing.

// CreatePasswordResetToken stores the SHA-256 digest of a fresh reset token.
// Two housekeeping rules ride along:
//   - a new request invalidates the user's previous unused tokens (the
//     latest email wins, and the active-token surface stays at one);
//   - rows that expired or were already consumed more than a day ago are
//     purged, so the table stays bounded without a cron job.
func (m *Mysql) CreatePasswordResetToken(ctx context.Context, uid int64, tokenHash string, ttl time.Duration) error {
	secs := int64(ttl / time.Second)
	if secs <= 0 {
		secs = 1800
	}
	now := m.now().UTC()
	// Best-effort housekeeping: a purge failure must not block the insert.
	_, _ = m.db.ExecContext(ctx,
		`DELETE FROM password_reset_tokens
		  WHERE used_at IS NOT NULL OR expires_at < ?`, now.Add(-24*time.Hour))
	if _, err := m.db.ExecContext(ctx,
		`UPDATE password_reset_tokens SET used_at = ?
		  WHERE uid = ? AND used_at IS NULL`, now, uid); err != nil {
		return err
	}
	_, err := m.db.ExecContext(ctx,
		`INSERT INTO password_reset_tokens (uid, token_hash, expires_at)
		 VALUES (?, ?, ?)`, uid, tokenHash, now.Add(time.Duration(secs)*time.Second))
	return err
}

// ConsumePasswordResetToken atomically claims a token: the UPDATE matches
// only an unused, unexpired row, so a double-click or an attacker replaying
// a leaked link finds nothing. Every failure mode — unknown digest, expired,
// already used — collapses into ErrNotFound: the endpoint must not leak
// which one it was.
func (m *Mysql) ConsumePasswordResetToken(ctx context.Context, tokenHash string) (int64, error) {
	now := m.now().UTC()
	res, err := m.db.ExecContext(ctx,
		`UPDATE password_reset_tokens SET used_at = ?
		  WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`,
		now, tokenHash, now)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return 0, ErrNotFound
	}
	// The row exists and is now claimed; read the owner back (the digest is
	// unique-indexed, and the row is never deleted by this path).
	var uid int64
	if err := m.db.QueryRowContext(ctx,
		`SELECT uid FROM password_reset_tokens WHERE token_hash = ?`, tokenHash).
		Scan(&uid); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	return uid, nil
}

// UserByEmail resolves an email address to a uid (case-insensitive). Legacy
// MySQL folded it through the general_ci column collation; LOWER() keeps the
// same match on PostgreSQL's case-sensitive default and is a no-op on the
// other two engines.
func (m *Mysql) UserByEmail(ctx context.Context, email string) (int64, error) {
	var uid int64
	err := m.db.QueryRowContext(ctx,
		`SELECT id FROM users WHERE `+m.db.Dialect().CIEquals("email")+` LIMIT 1`,
		email).Scan(&uid)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return uid, err
}
