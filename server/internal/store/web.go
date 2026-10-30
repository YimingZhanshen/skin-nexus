package store

import (
	"context"
	"crypto/md5" //nolint:gosec // legacy-compatible UUID derivation
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// ---- registration & web sessions ----

// CreateUser registers a user. passwordHash is already modern PHC. ip is the
// registrant address (blessing records it per-account for the regs_per_ip
// cap; empty = unknown, never counted).
func (m *Mysql) CreateUser(ctx context.Context, email, nickname, passwordHash string, playerLimit int, ip string) (int64, error) {
	return m.createUser(ctx, email, nickname, passwordHash, playerLimit, 0, ip)
}

// CreateUserAdmin registers the install-wizard bootstrap account with the
// legacy blessing admin permission (permission = 1 > 0) in the same INSERT —
// the wizard is a one-way door, so the account must never land half-promoted
// (otherwise no account could reach the admin panel by login alone).
func (m *Mysql) CreateUserAdmin(ctx context.Context, email, nickname, passwordHash string, playerLimit int) (int64, error) {
	return m.createUser(ctx, email, nickname, passwordHash, playerLimit, 1, "")
}

func (m *Mysql) createUser(ctx context.Context, email, nickname, passwordHash string, playerLimit, permission int, ip string) (int64, error) {
	res, err := m.db.ExecContext(ctx,
		`INSERT INTO users (email, nickname, password, password_algo, player_limit, email_verified, can_join_server, permission, ip)
		 VALUES (?,?,?,'argon2id',?,0,1,?,?)`,
		nullableStr(email), nickname, passwordHash, playerLimit, permission, nullableStr(ip))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// CreateSession stores an opaque web session token.
func (m *Mysql) CreateSession(ctx context.Context, token string, uid int64, expires time.Time) error {
	_, err := m.db.ExecContext(ctx,
		`INSERT INTO web_sessions (token, user_id, expires_at) VALUES (?,?,?)`, token, uid, expires)
	return err
}

// SessionUser resolves a session token to a user (nil-safe: ErrNotFound if invalid).
func (m *Mysql) SessionUser(ctx context.Context, token string) (int64, error) {
	var uid int64
	err := m.db.QueryRowContext(ctx,
		`SELECT user_id FROM web_sessions WHERE token = ? AND expires_at > NOW(3)`, token).
		Scan(&uid)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return uid, err
}

func (m *Mysql) DeleteSession(ctx context.Context, token string) error {
	_, err := m.db.ExecContext(ctx, `DELETE FROM web_sessions WHERE token = ?`, token)
	return err
}

// DeleteUserSessionsExcept purges a user's other web sessions (password
// change hygiene: the caller's own session survives, stolen ones do not).
func (m *Mysql) DeleteUserSessionsExcept(ctx context.Context, uid int64, keepToken string) error {
	_, err := m.db.ExecContext(ctx,
		`DELETE FROM web_sessions WHERE user_id = ? AND token <> ?`, uid, keepToken)
	return err
}

// RehashPassword updates to modern digest after successful legacy login.
// Guarded on still-legacy algo so a concurrent password change cannot be
// clobbered with a rehash of the OLD password (review P3 lost-update race).
// Deliberately NOT used by the password-change flow — that must always write.
func (m *Mysql) RehashPassword(ctx context.Context, uid int64, newHash string) error {
	_, err := m.db.ExecContext(ctx,
		`UPDATE users SET password = ?, password_algo = 'argon2id' WHERE id = ? AND password_algo LIKE 'legacy%'`,
		newHash, uid)
	return err
}

// SetPasswordHash writes a new credential UNCONDITIONALLY (self-service
// password change). Chain-trace P0 (2026-10-23): the change flow once reused
// the legacy-guarded RehashPassword — for argon2id users the UPDATE silently
// matched zero rows (password unchanged!) while every token was still
// revoked, turning "change password to stop a leak" into an amplifier.
func (m *Mysql) SetPasswordHash(ctx context.Context, uid int64, newHash string) error {
	_, err := m.db.ExecContext(ctx,
		`UPDATE users SET password = ?, password_algo = 'argon2id' WHERE id = ?`,
		newHash, uid)
	return err
}

// UserPasswordAlgo exposes the password algorithm marker (for lazy rehash
// decisions in webapi after a successful verify).
func (m *Mysql) UserPasswordAlgo(ctx context.Context, uid int64) (string, error) {
	var algo string
	err := m.db.QueryRowContext(ctx, `SELECT password_algo FROM users WHERE id = ?`, uid).Scan(&algo)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return algo, err
}

// UserPlayerLimit returns the per-user character limit (review P1: the
// column was written at register/setup but never enforced anywhere).
func (m *Mysql) UserPlayerLimit(ctx context.Context, uid int64) (int, error) {
	var n int
	err := m.db.QueryRowContext(ctx,
		`SELECT player_limit FROM users WHERE id = ?`, uid).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// UserPermission returns the legacy blessing permission column
// (0 = normal, >0 = admin, -1 = banned). Powers the session-based admin
// panel gate (blessing parity) and the /api/v1/me `permission` field.
func (m *Mysql) UserPermission(ctx context.Context, uid int64) (int, error) {
	var p int
	err := m.db.QueryRowContext(ctx,
		`SELECT permission FROM users WHERE id = ?`, uid).Scan(&p)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return p, err
}

// WeakHashCount powers the 「弱哈希余量」 dashboard.
func (m *Mysql) WeakHashCount(ctx context.Context) (int, error) {
	var n int
	err := m.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users WHERE password_algo LIKE 'legacy%'`).Scan(&n)
	return n, err
}

// ---- textures ----

// ErrDuplicate surfaces a unique-key collision (uq_users_email /
// uq_users_nickname) from the admin inline-edit updates; handlers map it
// to HTTP 409.
var ErrDuplicate = errors.New("store: duplicate key")

// UpdateUserEmail sets a user's email ("" clears to NULL). Duplicate
// collision → ErrDuplicate.
func (m *Mysql) UpdateUserEmail(ctx context.Context, uid int64, email string) error {
	_, err := m.db.ExecContext(ctx,
		`UPDATE users SET email = ? WHERE id = ?`, nullableStr(email), uid)
	if isDupKey(err) {
		return ErrDuplicate
	}
	return err
}

// UpdateUserNickname sets a user's nickname (admin inline edit, B2). The
// column is NOT NULL + unique — empty input must be rejected by the caller.
// Duplicate collision → ErrDuplicate.
func (m *Mysql) UpdateUserNickname(ctx context.Context, uid int64, nickname string) error {
	_, err := m.db.ExecContext(ctx,
		`UPDATE users SET nickname = ? WHERE id = ?`, nickname, uid)
	if isDupKey(err) {
		return ErrDuplicate
	}
	return err
}

// SetEmailVerified flips the email_verified flag (0/1).
func (m *Mysql) SetEmailVerified(ctx context.Context, uid int64, verified bool) error {
	v := 0
	if verified {
		v = 1
	}
	_, err := m.db.ExecContext(ctx,
		`UPDATE users SET email_verified = ? WHERE id = ?`, v, uid)
	return err
}

// SetUserPermission writes the legacy blessing permission column
// (-1 banned / 0 normal / 1 admin / 2 super-admin). Guard logic (who may
// change whom) lives in the adminapi handler — this is the raw write.
func (m *Mysql) SetUserPermission(ctx context.Context, uid int64, perm int) error {
	_, err := m.db.ExecContext(ctx,
		`UPDATE users SET permission = ? WHERE id = ?`, perm, uid)
	return err
}

// ---- textures ----

// AddTexture inserts a public texture record and returns its id. The
// public_dedupe unique key rejects concurrent duplicate content.
func (m *Mysql) AddTexture(ctx context.Context, uid int64, typ, hash, model, desc string) (int64, error) {
	res, err := m.db.ExecContext(ctx,
		`INSERT INTO textures (uid, type, hash, model, description, public_dedupe) VALUES (?,?,?,?,?,?)`,
		uid, typ, hash, nullableStr(model), nullableStr(desc), typ+":"+hash)
	if err != nil {
		if isDuplicateKey(err) {
			return 0, ErrDuplicatePublic
		}
		return 0, err
	}
	return res.LastInsertId()
}

// AddTexturePrivate inserts a library import as a private copy (no file copy;
// the content-addressed blob already exists). public_dedupe stays NULL, so
// private copies may coexist with a public row.
func (m *Mysql) AddTexturePrivate(ctx context.Context, uid int64, typ, hash, model, desc string) (int64, error) {
	res, err := m.db.ExecContext(ctx,
		`INSERT INTO textures (uid, type, hash, model, description, public, public_dedupe) VALUES (?,?,?,?,?,0,NULL)`,
		uid, typ, hash, nullableStr(model), nullableStr(desc))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// TextureByOwnerHash returns one of the user's textures with the same content
// hash and type; upload/import use it for retry idempotency.
func (m *Mysql) TextureByOwnerHash(ctx context.Context, uid int64, typ, hash string) (TextureRow, bool, error) {
	var row TextureRow
	err := m.db.QueryRowContext(ctx,
		`SELECT id, type, hash, COALESCE(model,''), COALESCE(description,''), upload_at
		   FROM textures WHERE uid = ? AND type = ? AND hash = ? ORDER BY id LIMIT 1`,
		uid, typ, hash).Scan(&row.ID, &row.Type, &row.Hash, &row.Model, &row.Description, &row.UploadAt)
	if errors.Is(err, sql.ErrNoRows) {
		return TextureRow{}, false, nil
	}
	if err != nil {
		return TextureRow{}, false, err
	}
	return row, true, nil
}

// PublicTextureByHash returns a public texture with the same content hash
// and type. Private rows are never returned, so callers cannot probe other
// users' private libraries.
func (m *Mysql) PublicTextureByHash(ctx context.Context, typ, hash string) (TextureRow, bool, error) {
	var row TextureRow
	err := m.db.QueryRowContext(ctx,
		`SELECT id, type, hash, COALESCE(model,''), COALESCE(description,''), upload_at
		   FROM textures WHERE public = 1 AND type = ? AND hash = ? ORDER BY id LIMIT 1`,
		typ, hash).Scan(&row.ID, &row.Type, &row.Hash, &row.Model, &row.Description, &row.UploadAt)
	if errors.Is(err, sql.ErrNoRows) {
		return TextureRow{}, false, nil
	}
	if err != nil {
		return TextureRow{}, false, err
	}
	return row, true, nil
}

// TextureByID returns texture row (for serving raw bytes later).
func (m *Mysql) TextureByHash(ctx context.Context, hash string) (id, uid int64, typ string, err error) {
	err = m.db.QueryRowContext(ctx,
		`SELECT id, uid, type FROM textures WHERE hash = ? LIMIT 1`, hash).
		Scan(&id, &uid, &typ)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return
}

// ListTextures returns a user's textures.
func (m *Mysql) ListTextures(ctx context.Context, uid int64) ([]TextureRow, error) {
	rows, err := m.db.QueryContext(ctx,
		`SELECT id, type, hash, COALESCE(model,''), COALESCE(description,''), upload_at
		 FROM textures WHERE uid = ? ORDER BY id DESC`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TextureRow
	for rows.Next() {
		var t TextureRow
		if err := rows.Scan(&t.ID, &t.Type, &t.Hash, &t.Model, &t.Description, &t.UploadAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

type TextureRow struct {
	ID          int64
	Type        string
	Hash        string
	Model       string
	Description string
	UploadAt    time.Time
}

// TextureOwner returns the owning user id of a texture.
func (m *Mysql) TextureOwner(ctx context.Context, tid int64) (int64, error) {
	var uid int64
	err := m.db.QueryRowContext(ctx, `SELECT uid FROM textures WHERE id = ?`, tid).Scan(&uid)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return uid, err
}

// TextureType returns a texture's kind ("skin"/"cape"). Avatar needs the
// type gate: legacy TextureController.php:141 aborts 422 unless steve/alex
// (i.e. a skin) — a cape must never become an avatar.
func (m *Mysql) TextureType(ctx context.Context, tid int64) (string, error) {
	var typ string
	err := m.db.QueryRowContext(ctx, `SELECT type FROM textures WHERE id = ?`, tid).Scan(&typ)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return typ, err
}

// TextureByTID returns a texture's hash, kind and slim flag in one round
// trip — the avatar/preview handlers need all three (hash to open the
// file, type for the cape-422 gate, slim for the arm geometry).
func (m *Mysql) TextureByTID(ctx context.Context, tid int64) (hash, typ string, slim bool, err error) {
	var model sql.NullString
	err = m.db.QueryRowContext(ctx,
		`SELECT hash, type, model FROM textures WHERE id = ?`, tid).
		Scan(&hash, &typ, &model)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	slim = model.Valid && model.String == "slim"
	return
}

// TextureMetaByHash is TextureByTID keyed by content hash (preview/hash route).
func (m *Mysql) TextureMetaByHash(ctx context.Context, hash string) (typ string, slim bool, err error) {
	var model sql.NullString
	err = m.db.QueryRowContext(ctx,
		`SELECT type, model FROM textures WHERE hash = ? LIMIT 1`, hash).
		Scan(&typ, &model)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	slim = model.Valid && model.String == "slim"
	return
}

// UserAvatarTID returns the tid a user set as avatar (0 = none).
// Legacy users.avatar stores the tid; avatar handler renders it.
func (m *Mysql) UserAvatarTID(ctx context.Context, uid int64) (int64, error) {
	var tid sql.NullInt64
	err := m.db.QueryRowContext(ctx, `SELECT avatar_tid FROM users WHERE id = ?`, uid).Scan(&tid)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if !tid.Valid {
		return 0, nil
	}
	return tid.Int64, nil
}

// PlayerTextureIDs returns current skin/cape texture ids (0 = unset).
func (m *Mysql) PlayerTextureIDs(ctx context.Context, pid int64) (skin, cape int64, err error) {
	var s, c sql.NullInt64
	err = m.db.QueryRowContext(ctx,
		`SELECT tid_skin, tid_cape FROM players WHERE id = ?`, pid).Scan(&s, &c)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return s.Int64, c.Int64, err
}

// PlayerTextureHashes returns the raw texture hashes for legacy-compat
// endpoints (CustomSkinAPI / USM / legacy-api need hashes, not URLs).
func (m *Mysql) PlayerTextureHashes(ctx context.Context, pid int64) (skinHash, skinModel, capeHash string, err error) {
	row := m.db.QueryRowContext(ctx,
		`SELECT COALESCE(ts.hash,''), COALESCE(ts.model,'default'), COALESCE(tc.hash,'')
		 FROM players p
		 LEFT JOIN textures ts ON ts.id = p.tid_skin
		 LEFT JOIN textures tc ON tc.id = p.tid_cape
		 WHERE p.id = ?`, pid)
	err = row.Scan(&skinHash, &skinModel, &capeHash)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return
}

// ---- admin queries ----

// Counters for the admin overview.
func (m *Mysql) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (m *Mysql) CountPlayers(ctx context.Context) (int, error) {
	var n int
	err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM players`).Scan(&n)
	return n, err
}

func (m *Mysql) CountTextures(ctx context.Context) (int, error) {
	var n int
	err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM textures`).Scan(&n)
	return n, err
}

// AdminUsers returns a page of users with weak-hash marker. q (optional)
// searches email/nickname case-insensitively (LOWER() comparison —
// general_ci column collations fold case the same way). Rows carry
// permission + email_verified for the admin table (B2).
func (m *Mysql) AdminUsers(ctx context.Context, q string, page, size int) ([]map[string]interface{}, int, error) {
	q = strings.TrimSpace(q)
	where := ""
	var args []interface{}
	if q != "" {
		where = `WHERE LOWER(COALESCE(email,'')) LIKE ? OR LOWER(nickname) LIKE ?`
		like := "%" + strings.ToLower(q) + "%"
		args = append(args, like, like)
	}
	var total int
	if err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := m.db.QueryContext(ctx,
		`SELECT id, COALESCE(email,''), nickname, password_algo, player_limit,
		        COALESCE(email_verified,0), COALESCE(permission,0), created_at
		 FROM users `+where+` ORDER BY id DESC LIMIT ? OFFSET ?`,
		append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []map[string]interface{}
	for rows.Next() {
		var id, limit int64
		var email, nick, algo string
		var verified, perm int
		var created time.Time
		if err := rows.Scan(&id, &email, &nick, &algo, &limit, &verified, &perm, &created); err != nil {
			return nil, 0, err
		}
		out = append(out, map[string]interface{}{
			"id": id, "email": email, "nickname": nick,
			"passwordAlgo": algo, "playerLimit": limit,
			"emailVerified": verified, "permission": perm,
			"createdAt": created.Format(time.RFC3339),
		})
	}
	return out, total, rows.Err()
}

// AdminTextures returns a page of texture records.
func (m *Mysql) AdminTextures(ctx context.Context, page, size int) ([]map[string]interface{}, int, error) {
	var total int
	if err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM textures`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := m.db.QueryContext(ctx,
		`SELECT t.id, t.uid, t.type, t.hash, COALESCE(t.model,''), t.upload_at
		 FROM textures t ORDER BY t.id DESC LIMIT ? OFFSET ?`, size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []map[string]interface{}
	for rows.Next() {
		var id, uid int64
		var typ, hash, model string
		var up time.Time
		if err := rows.Scan(&id, &uid, &typ, &hash, &model, &up); err != nil {
			return nil, 0, err
		}
		out = append(out, map[string]interface{}{
			"id": id, "uid": uid, "type": typ, "hash": hash, "model": model,
			"uploadAt": up.Format(time.RFC3339),
		})
	}
	return out, total, rows.Err()
}

// RecentAudit returns latest audit entries.
func (m *Mysql) RecentAudit(ctx context.Context, limit int) ([]map[string]interface{}, error) {
	rows, err := m.db.QueryContext(ctx,
		`SELECT id, COALESCE(actor_uid,0), action, COALESCE(detail,'{}'), created_at
		 FROM audit_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]interface{}
	for rows.Next() {
		var id, actor int64
		var action, detail string
		var created time.Time
		if err := rows.Scan(&id, &actor, &action, &detail, &created); err != nil {
			return nil, err
		}
		out = append(out, map[string]interface{}{
			"id": id, "actorUid": actor, "action": action, "detail": detail,
			"createdAt": created.Format(time.RFC3339),
		})
	}
	return out, rows.Err()
}

// ---- players ----

// CreatePlayer adds a character. Generates a UUID per the legacy v3 rule
// (hex2bin(md5("OfflinePlayer:"+name)), byte6 |= 0x30, byte8 |= 0x80) and
// records it in uuid_map so renames never recompute it.
func (m *Mysql) CreatePlayer(ctx context.Context, uid int64, name string) (int64, string, error) {
	uuid := OfflineUUID(name)
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, "", err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `INSERT INTO players (uid, name) VALUES (?,?)`, uid, name)
	if err != nil {
		return 0, "", err
	}
	pid, err := res.LastInsertId()
	if err != nil {
		return 0, "", err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO uuid_map (name, uuid, player_id) VALUES (?,?,?)`, name, uuid, pid); err != nil {
		return 0, "", err
	}
	return pid, uuid, tx.Commit()
}

// SetPlayerTextures points a player at texture ids (0 = clear).
func (m *Mysql) SetPlayerTextures(ctx context.Context, pid, skinTid, capeTid int64) error {
	_, err := m.db.ExecContext(ctx,
		`UPDATE players SET
		 tid_skin = IF(? = 0, NULL, ?),
		 tid_cape = IF(? = 0, NULL, ?)
		 WHERE id = ?`, skinTid, skinTid, capeTid, capeTid, pid)
	return err
}

// SetUserAvatar points users.avatar_tid at a texture the user owns
// (tid=0 clears — blessing 更改头像/重置头像 semantics; legacy column
// users.avatar carried the same tid meaning). Ownership is the CALLER's
// job (webapi checks TextureOwner first); this method only writes.
func (m *Mysql) SetUserAvatar(ctx context.Context, uid, tid int64) error {
	_, err := m.db.ExecContext(ctx,
		`UPDATE users SET avatar_tid = IF(? = 0, NULL, ?) WHERE id = ?`, tid, tid, uid)
	return err
}

// UserAvatarHash returns the texture hash behind users.avatar_tid ("" when
// unset or the texture row is gone — avatar degrades to default skin).
func (m *Mysql) UserAvatarHash(ctx context.Context, uid int64) (string, error) {
	var h sql.NullString
	err := m.db.QueryRowContext(ctx,
		`SELECT t.hash FROM users u
		 LEFT JOIN textures t ON t.id = u.avatar_tid
		 WHERE u.id = ?`, uid).Scan(&h)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return h.String, nil
}

// OfflineUUID reproduces the legacy v3 UUID rule byte-exactly
// (analysis/blessing-skin-plugins.md §2 — hex2bin(md5(...)) + variant bits).
func OfflineUUID(name string) string {
	sum := md5.Sum([]byte("OfflinePlayer:" + name)) //nolint:gosec
	b := sum[:]
	b[6] = (b[6] & 0x0f) | 0x30 // version 3
	b[8] = (b[8] & 0x3f) | 0x80 // RFC4122 variant
	return hex.EncodeToString(b)
}

func nullableStr(s string) interface{} {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}
