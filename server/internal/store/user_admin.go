package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
)

// Admin user management (P1 管理面余项, 2026-10-23).

// AdminSetPassword overwrites a user's hash (argon2id) — admin reset path.
// The caller (adminapi) fires credential-change side effects (ygg + OIDC
// revocation) like the self-service change does.
func (m *Mysql) AdminSetPassword(ctx context.Context, uid int64, hash string) error {
	res, err := m.db.ExecContext(ctx,
		`UPDATE users SET password = ?, password_algo = 'argon2id' WHERE id = ?`, hash, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// row exists with identical hash → still success (changed-rows)
		var one int
		if err := m.db.QueryRowContext(ctx, `SELECT id FROM users WHERE id = ?`, uid).Scan(&one); err != nil {
			return ErrNotFound
		}
	}
	return nil
}

// DeleteUser removes a user and every credential/session row that would let
// the account keep working, in one transaction:
//
//	web_sessions / ygg_tokens / oidc refresh+access / user_closet /
//	uuid_map of their players / players / the users row last.
//
// textures.uid (uploaded library files) and audit_log.actor_uid are
// deliberately KEPT — shared-library + audit-history semantics (legacy had
// no cascades either; orphan visibility beats silent data loss).
// ygg_join_sessions has no uid column (120s TTL, unusable once tokens die).
func (m *Mysql) DeleteUser(ctx context.Context, uid int64) error {
	var one int
	if err := m.db.QueryRowContext(ctx, `SELECT id FROM users WHERE id = ?`, uid).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmts := []struct {
		q    string
		args []any
	}{
		{`DELETE FROM web_sessions WHERE user_id = ?`, []any{uid}},
		{`DELETE FROM ygg_tokens WHERE user_id = ?`, []any{uid}},
		{`DELETE FROM oidc_refresh_tokens WHERE uid = ?`, []any{uid}},
		{`DELETE FROM oidc_access_tokens WHERE uid = ?`, []any{uid}},
		// review P2: device grants and pending auth requests also carry user
		// state and can still walk into token issuance after deletion.
		{`DELETE FROM oidc_device_auth WHERE uid = ?`, []any{uid}},
		// subject stores the numeric uid as text; bind the string form so no
		// engine-specific CAST(int AS CHAR) is needed (PostgreSQL/SQLite reject
		// that spelling and compare text to an integer otherwise).
		{`DELETE FROM oidc_auth_requests WHERE subject = ?`, []any{strconv.FormatInt(uid, 10)}},
		{`DELETE FROM user_closet WHERE user_uid = ?`, []any{uid}},
		{`DELETE FROM uuid_map WHERE player_id IN (SELECT id FROM players WHERE uid = ?)`, []any{uid}},
		{`DELETE FROM players WHERE uid = ?`, []any{uid}},
		{`DELETE FROM users WHERE id = ?`, []any{uid}},
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s.q, s.args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}
