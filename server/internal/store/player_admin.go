package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Admin player management (B3 管理面板对齐, 2026-10-24): list / rename /
// owner change / texture swap / delete. Follows user_admin.go precedent.

// AdminPlayers returns a page of players with owner nickname, UUID and
// texture hashes (hash -> frontend builds /thumbnails from /textures/<hash>).
// q (optional) matches player name, owner nickname, or exact owner uid when
// numeric. uidFilter > 0 narrows to one owner (texture picker support).
func (m *Mysql) AdminPlayers(ctx context.Context, q string, uidFilter int64, page, size int) ([]map[string]interface{}, int, error) {
	q = strings.TrimSpace(q)
	where := ""
	var args []interface{}
	if uidFilter > 0 {
		where += " AND p.uid = ?"
		args = append(args, uidFilter)
	}
	if q != "" {
		ci := m.db.Dialect()
		where += " AND (" + ci.CIContains("p.name") + " OR " + ci.CIContains("usr.nickname") + ")"
		like := "%" + q + "%"
		args = append(args, like, like)
		if uidFilter == 0 {
			// numeric q also matches the owner uid exactly (blessing 列表习惯)
			where += " OR p.uid = ?"
			if uid, err := strconv.ParseInt(q, 10, 64); err == nil {
				args = append(args, uid)
			} else {
				args = append(args, -1) // non-numeric: never matches
			}
		}
		where += ")"
	}
	from := ` FROM players p
	  LEFT JOIN users usr ON usr.id = p.uid
	  LEFT JOIN uuid_map u ON u.player_id = p.id
	  LEFT JOIN textures ts ON ts.id = p.tid_skin
	  LEFT JOIN textures tc ON tc.id = p.tid_cape
	  WHERE 1=1` + where
	// COUNT(DISTINCT p.id): the identity joins are 1:1 in practice but the
	// count must never drift if a legacy uuid_map anomaly duplicates a row.
	var total int
	if err := m.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT p.id)`+from, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := m.db.QueryContext(ctx,
		`SELECT p.id, p.uid, p.name, COALESCE(u.uuid,''),
		        COALESCE(ts.hash,''), COALESCE(tc.hash,''), COALESCE(usr.nickname,''), p.updated_at`+
			from+` ORDER BY p.id DESC LIMIT ? OFFSET ?`,
		append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []map[string]interface{}
	for rows.Next() {
		var pid, uid int64
		var name, uuid, skinHash, capeHash, ownerNick string
		var updated time.Time
		if err := rows.Scan(&pid, &uid, &name, &uuid, &skinHash, &capeHash, &ownerNick, &updated); err != nil {
			return nil, 0, err
		}
		out = append(out, map[string]interface{}{
			"id": pid, "uid": uid, "name": name, "uuid": uuid,
			"skinHash": skinHash, "capeHash": capeHash,
			"ownerNickname": ownerNick, "updatedAt": updated.Format(time.RFC3339),
		})
	}
	return out, total, rows.Err()
}

// AdminRenamePlayer renames a character. UUID ZERO-DRIFT RULE (铁律 2): the
// uuid_map row's uuid column is NEVER recomputed — only the name column
// (its PK) follows, keeping the identity stable across renames. A name
// collision on either table → ErrDuplicate (rolled back as one tx).
func (m *Mysql) AdminRenamePlayer(ctx context.Context, pid int64, newName string) error {
	var one int
	if err := m.db.QueryRowContext(ctx, `SELECT id FROM players WHERE id = ?`, pid).Scan(&one); err != nil {
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
	if _, err := tx.ExecContext(ctx,
		`UPDATE players SET name = ? WHERE id = ?`, newName, pid); err != nil {
		if isDupKey(err) {
			return ErrDuplicate
		}
		return err
	}
	// uuid stays untouched (SELECT-verified in the integration test):
	if _, err := tx.ExecContext(ctx,
		`UPDATE uuid_map SET name = ? WHERE player_id = ?`, newName, pid); err != nil {
		if isDupKey(err) {
			return ErrDuplicate
		}
		return err
	}
	// Rename invalidation (B5.1, upstream OnPlayerRenamed.php:18 +
	// AccessToken.php:145-149 parity): tokens issued BEFORE the rename go
	// 暂时失效 — squeeze their expires_at to the rename moment instead of
	// invalidating. Upstream rejects validate/join for pre-rename tokens but
	// keeps refresh alive (isRefreshable skips the rename check); a plain
	// invalidated=1 would kill refresh too and force full re-logins, so the
	// soft-expiry squeeze is the faithful equivalent. Tokens with no
	// selected profile are untouched (upstream keys on selectedProfile).
	// Only shortens, never revives: expires_at > now is required in WHERE.
	now := time.Now()
	if _, err := tx.ExecContext(ctx,
		`UPDATE ygg_tokens SET expires_at = ?
		 WHERE selected_pid = ? AND created_at < ? AND expires_at > ?`,
		now, pid, now, now); err != nil {
		return err
	}
	return tx.Commit()
}

// AllProfileUUIDs returns every (uuid, name) pair for the union roster push
// (B5.3 Sync button). Empty-uuid rows (no uuid_map entry) are skipped — the
// hub identifies profiles by uuid.
func (m *Mysql) AllProfileUUIDs(ctx context.Context) (map[string]string, error) {
	rows, err := m.db.QueryContext(ctx,
		`SELECT u.uuid, p.name FROM players p JOIN uuid_map u ON u.player_id = p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var uuid, name string
		if err := rows.Scan(&uuid, &name); err != nil {
			return nil, err
		}
		out[uuid] = name
	}
	return out, rows.Err()
}

// AdminSetPlayerOwner re-homes a character. The caller (adminapi) validates
// that the target user exists; the caller also revokes the OLD owner's ygg
// tokens (hygiene: their launcher profile selection pointed here).
func (m *Mysql) AdminSetPlayerOwner(ctx context.Context, pid, uid int64) error {
	var one int
	if err := m.db.QueryRowContext(ctx, `SELECT id FROM players WHERE id = ?`, pid).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	_, err := m.db.ExecContext(ctx, `UPDATE players SET uid = ? WHERE id = ?`, uid, pid)
	return err
}

// AdminDeletePlayer removes a character and its uuid_map identity in one
// transaction. Texture rows are KEPT (shared-library semantics, same as
// DeleteUser). Tokens whose profile selection pointed here are detached
// (selected_pid -> NULL) instead of deleted — the token itself stays valid
// and the client falls back to profile selection. Returns the (uuid, name)
// pair captured BEFORE deletion for the best-effort union hub announcement.
func (m *Mysql) AdminDeletePlayer(ctx context.Context, pid int64) (uuid, name string, err error) {
	if err = m.db.QueryRowContext(ctx,
		`SELECT COALESCE(u.uuid,''), p.name FROM players p
		 LEFT JOIN uuid_map u ON u.player_id = p.id WHERE p.id = ?`, pid).
		Scan(&uuid, &name); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = ErrNotFound
		}
		return "", "", err
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	stmts := []string{
		`DELETE FROM uuid_map WHERE player_id = ?`,
		`UPDATE ygg_tokens SET selected_pid = NULL WHERE selected_pid = ?`,
		`DELETE FROM players WHERE id = ?`,
	}
	for _, q := range stmts {
		if _, err := tx.ExecContext(ctx, q, pid); err != nil {
			return "", "", err
		}
	}
	if err := tx.Commit(); err != nil {
		return "", "", err
	}
	return uuid, name, nil
}
