// Email verification tokens. Same rules as password reset tokens: digest-only
// storage, single use, Go-clock expiry (every deadline and comparison binds
// m.now(), so writes and reads share one clock), one live token per uid.
package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// CreateEmailVerifyToken stores the digest of a fresh verification token and
// invalidates that uid's previous unused ones. Rows finished more than a day
// ago are purged here so the table stays bounded without a cron job.
func (m *Mysql) CreateEmailVerifyToken(ctx context.Context, uid int64, tokenHash string, ttl time.Duration) error {
	secs := int64(ttl / time.Second)
	if secs <= 0 {
		secs = 1800
	}
	now := m.now().UTC()
	_, _ = m.db.ExecContext(ctx,
		`DELETE FROM email_verify_tokens
		  WHERE used_at IS NOT NULL OR expires_at < ?`, now.Add(-24*time.Hour))
	if _, err := m.db.ExecContext(ctx,
		`UPDATE email_verify_tokens SET used_at = ?
		  WHERE uid = ? AND used_at IS NULL`, now, uid); err != nil {
		return err
	}
	_, err := m.db.ExecContext(ctx,
		`INSERT INTO email_verify_tokens (uid, token_hash, expires_at)
		 VALUES (?, ?, ?)`, uid, tokenHash, now.Add(time.Duration(secs)*time.Second))
	return err
}

// InvalidateEmailVerifyTokens drops a uid's unused tokens. Called when the
// address changes: a link sent to the old mailbox must not verify the new one.
func (m *Mysql) InvalidateEmailVerifyTokens(ctx context.Context, uid int64) error {
	_, err := m.db.ExecContext(ctx,
		`UPDATE email_verify_tokens SET used_at = ?
		  WHERE uid = ? AND used_at IS NULL`, m.now().UTC(), uid)
	return err
}

// DeleteEmailVerifyToken drops one token by digest. Used when the mail never
// left the server: an outstanding link nobody received is still a live
// credential sitting in the database.
func (m *Mysql) DeleteEmailVerifyToken(ctx context.Context, tokenHash string) error {
	_, err := m.db.ExecContext(ctx,
		`DELETE FROM email_verify_tokens WHERE token_hash = ?`, tokenHash)
	return err
}

// ConsumeEmailVerifyToken atomically claims a token. Unknown digest, expired
// and already used all return ErrNotFound — the endpoint must not distinguish
// them, or it becomes a state oracle.
func (m *Mysql) ConsumeEmailVerifyToken(ctx context.Context, tokenHash string) (int64, error) {
	now := m.now().UTC()
	res, err := m.db.ExecContext(ctx,
		`UPDATE email_verify_tokens SET used_at = ?
		  WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`,
		now, tokenHash, now)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return 0, ErrNotFound
	}
	var uid int64
	if err := m.db.QueryRowContext(ctx,
		`SELECT uid FROM email_verify_tokens WHERE token_hash = ?`, tokenHash).
		Scan(&uid); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	return uid, nil
}
