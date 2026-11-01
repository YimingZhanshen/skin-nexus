// Email verification tokens. Same rules as password reset tokens: digest-only
// storage, single use, DB-clock expiry, one live token per uid.
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
	_, _ = m.db.ExecContext(ctx,
		`DELETE FROM email_verify_tokens
		  WHERE used_at IS NOT NULL OR expires_at < NOW(3) - INTERVAL 1 DAY`)
	if _, err := m.db.ExecContext(ctx,
		`UPDATE email_verify_tokens SET used_at = NOW(3)
		  WHERE uid = ? AND used_at IS NULL`, uid); err != nil {
		return err
	}
	_, err := m.db.ExecContext(ctx,
		`INSERT INTO email_verify_tokens (uid, token_hash, expires_at)
		 VALUES (?, ?, DATE_ADD(NOW(3), INTERVAL ? SECOND))`, uid, tokenHash, secs)
	return err
}

// InvalidateEmailVerifyTokens drops a uid's unused tokens. Called when the
// address changes: a link sent to the old mailbox must not verify the new one.
func (m *Mysql) InvalidateEmailVerifyTokens(ctx context.Context, uid int64) error {
	_, err := m.db.ExecContext(ctx,
		`UPDATE email_verify_tokens SET used_at = NOW(3)
		  WHERE uid = ? AND used_at IS NULL`, uid)
	return err
}

// ConsumeEmailVerifyToken atomically claims a token. Unknown digest, expired
// and already used all return ErrNotFound — the endpoint must not distinguish
// them, or it becomes a state oracle.
func (m *Mysql) ConsumeEmailVerifyToken(ctx context.Context, tokenHash string) (int64, error) {
	res, err := m.db.ExecContext(ctx,
		`UPDATE email_verify_tokens SET used_at = NOW(3)
		  WHERE token_hash = ? AND used_at IS NULL AND expires_at > NOW(3)`,
		tokenHash)
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
