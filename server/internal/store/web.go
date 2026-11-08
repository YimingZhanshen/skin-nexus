package store

import (
	"context"
	"crypto/md5" //nolint:gosec // legacy-compatible UUID derivation
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"skin-nexus/server/internal/avatargen"
	"skin-nexus/server/internal/dbx"
	"skin-nexus/server/internal/dialect"
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

// CreateUserAdmin registers the install-wizard bootstrap account as the
// SUPER_ADMIN (permission = 2). The admin panel needs >0 and plugin
// install/market operations require exactly 2; creating only permission=1
// left a fresh site unable to install its first plugin.
func (m *Mysql) CreateUserAdmin(ctx context.Context, email, nickname, passwordHash string, playerLimit int) (int64, error) {
	return m.createUser(ctx, email, nickname, passwordHash, playerLimit, 2, "")
}

// EnsureSuperAdmin is a one-time upgrade for sites created before the wizard
// wrote permission=2: if no super admin exists, promote the oldest legacy
// admin (permission=1). Idempotent; returns the promoted uid when changed.
func (m *Mysql) EnsureSuperAdmin(ctx context.Context) (int64, bool, error) {
	var n int
	if err := m.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users WHERE permission >= 2`).Scan(&n); err != nil {
		return 0, false, err
	}
	if n > 0 {
		return 0, false, nil
	}
	var uid int64
	err := m.db.QueryRowContext(ctx,
		`SELECT id FROM users WHERE permission = 1 ORDER BY id LIMIT 1`).Scan(&uid)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	res, err := m.db.ExecContext(ctx,
		`UPDATE users SET permission = 2 WHERE id = ? AND permission = 1`, uid)
	if err != nil {
		return 0, false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, false, nil
	}
	return uid, true, nil
}

func (m *Mysql) createUser(ctx context.Context, email, nickname, passwordHash string, playerLimit, permission int, ip string) (int64, error) {
	return insertUser(ctx, m.db, email, nickname, passwordHash, playerLimit, permission, ip, m.now().UTC())
}

// NicknameTaken says which half of the (nickname, email) pair a rejected
// registration collided with: the unique index does not tell, and answering
// "昵称或邮箱已被占用" sends the player trying both. The case folding matches
// UserByIdentifier, so the name that would log in is the name reported taken.
func (m *Mysql) NicknameTaken(ctx context.Context, nickname string) (bool, error) {
	var one int
	err := m.db.QueryRowContext(ctx,
		`SELECT 1 FROM users WHERE `+m.db.Dialect().CIEquals("nickname")+` LIMIT 1`, nickname).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// CreateSession stores an opaque web session token. The column holds
// hex(sha256(token)) — see digest.go — because a dumped sessions table must not
// be a list of working logins.
func (m *Mysql) CreateSession(ctx context.Context, token string, uid int64, expires time.Time) error {
	_, err := m.db.ExecContext(ctx,
		`INSERT INTO web_sessions (token, user_id, expires_at) VALUES (?,?,?)`, credentialDigest(token), uid, expires)
	return err
}

// SessionUser resolves a session token to a user (nil-safe: ErrNotFound if invalid).
func (m *Mysql) SessionUser(ctx context.Context, token string) (int64, error) {
	var uid int64
	err := m.db.QueryRowContext(ctx,
		`SELECT user_id FROM web_sessions WHERE token = ? AND expires_at > ?`, credentialDigest(token), m.now().UTC()).
		Scan(&uid)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return uid, err
}

func (m *Mysql) DeleteSession(ctx context.Context, token string) error {
	_, err := m.db.ExecContext(ctx, `DELETE FROM web_sessions WHERE token = ?`, credentialDigest(token))
	return err
}

// DeleteUserSessionsExcept purges a user's other web sessions (password
// change hygiene: the caller's own session survives, stolen ones do not).
func (m *Mysql) DeleteUserSessionsExcept(ctx context.Context, uid int64, keepToken string) error {
	_, err := m.db.ExecContext(ctx,
		`DELETE FROM web_sessions WHERE user_id = ? AND token <> ?`, uid, credentialDigest(keepToken))
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
	// Changing the address clears the verified flag: a verified mailbox is no
	// longer evidence that the *current* one is reachable. When the value is
	// unchanged the flag survives, so a no-op edit cannot lock a player out.
	// The email_verified assignment comes FIRST and reads `email` from the OLD
	// row: MySQL evaluates SET left-to-right (later assignments see already-
	// updated columns, so `email` is still old here), and PostgreSQL/SQLite
	// always evaluate every right-hand side against the old row — all three
	// therefore compare the pre-update email with the incoming value.
	value := nullableStr(email)
	_, err := m.db.ExecContext(ctx,
		`UPDATE users SET email_verified = CASE WHEN `+m.db.Dialect().NullSafeEq("email", "?")+` THEN email_verified ELSE 0 END, email = ?
		 WHERE id = ?`,
		value, value, uid)
	if isDupKey(err) {
		return ErrDuplicate
	}
	if err == nil {
		_ = m.InvalidateEmailVerifyTokens(ctx, uid)
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
func (m *Mysql) AddTexture(ctx context.Context, uid int64, typ, hash, model string, meta TextureMeta) (int64, error) {
	tid, err := m.db.InsertReturningID(ctx,
		`INSERT INTO textures (uid, type, hash, model, description, detail_md, source, source_url, public_dedupe)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		uid, typ, hash, nullableStr(model), nullableStr(meta.Description),
		nullableStr(meta.DetailMD), meta.Source, nullableStr(meta.SourceURL), typ+":"+hash)
	if err != nil {
		if isDupKey(err) {
			return 0, ErrDuplicatePublic
		}
		return 0, err
	}
	return tid, nil
}

// AddTexturePrivate inserts a library import as a private copy (no file copy;
// the content-addressed blob already exists). public_dedupe stays NULL, so
// private copies may coexist with a public row.
func (m *Mysql) AddTexturePrivate(ctx context.Context, uid int64, typ, hash, model string, meta TextureMeta) (int64, error) {
	tid, err := m.db.InsertReturningID(ctx,
		`INSERT INTO textures (uid, type, hash, model, description, detail_md, source, source_url, public, public_dedupe)
		 VALUES (?,?,?,?,?,?,?,?,0,NULL)`,
		uid, typ, hash, nullableStr(model), nullableStr(meta.Description),
		nullableStr(meta.DetailMD), meta.Source, nullableStr(meta.SourceURL))
	if err != nil {
		return 0, err
	}
	return tid, nil
}

// TextureByOwnerHash returns one of the user's textures with the same content
// hash and type; upload/import use it for retry idempotency.
func (m *Mysql) TextureByOwnerHash(ctx context.Context, uid int64, typ, hash string) (TextureRow, bool, error) {
	var row TextureRow
	err := m.db.QueryRowContext(ctx,
		`SELECT id, type, hash, COALESCE(model,''), COALESCE(description,''),
		         COALESCE(detail_md,''), COALESCE(source,''), COALESCE(source_url,''), upload_at
		   FROM textures WHERE uid = ? AND type = ? AND hash = ? ORDER BY id LIMIT 1`,
		uid, typ, hash).Scan(&row.ID, &row.Type, &row.Hash, &row.Model, &row.Description,
		&row.DetailMD, &row.Source, &row.SourceURL, &row.UploadAt)
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
		`SELECT id, type, hash, COALESCE(model,''), COALESCE(description,''),
		         COALESCE(detail_md,''), COALESCE(source,''), COALESCE(source_url,''), upload_at
		   FROM textures WHERE public = 1 AND type = ? AND hash = ? ORDER BY id LIMIT 1`,
		typ, hash).Scan(&row.ID, &row.Type, &row.Hash, &row.Model, &row.Description,
		&row.DetailMD, &row.Source, &row.SourceURL, &row.UploadAt)
	if errors.Is(err, sql.ErrNoRows) {
		return TextureRow{}, false, nil
	}
	if err != nil {
		return TextureRow{}, false, err
	}
	return row, true, nil
}

// TextureIsPublic reports a texture's visibility by id (upload reuse check).
func (m *Mysql) TextureIsPublic(ctx context.Context, tid int64) (bool, error) {
	var pub bool
	err := m.db.QueryRowContext(ctx, `SELECT public FROM textures WHERE id = ?`, tid).Scan(&pub)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	return pub, err
}

// TextureIsPublicByHash reports whether any row with this content hash is
// public (a private copy must not be enumerable through previews).
func (m *Mysql) TextureIsPublicByHash(ctx context.Context, hash string) (bool, error) {
	var pub bool
	err := m.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(public),0) FROM textures WHERE hash = ?`, hash).Scan(&pub)
	return pub, err
}

// TextureHashVisibility answers both halves of the visibility question for a
// content address in one pass: is any row public, and which accounts hold
// private rows. A shared hash can be claimed privately by more than one
// uploader, so "the owner" is a set — a route that authorises by hash has to
// check membership, not a single uid.
func (m *Mysql) TextureHashVisibility(ctx context.Context, hash string) (public bool, privateOwners []int64, err error) {
	rows, err := m.db.QueryContext(ctx, `SELECT public, uid FROM textures WHERE hash = ?`, hash)
	if err != nil {
		return false, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var pub bool
		var uid int64
		if err := rows.Scan(&pub, &uid); err != nil {
			return false, nil, err
		}
		if pub {
			public = true
			continue
		}
		privateOwners = append(privateOwners, uid)
	}
	return public, privateOwners, rows.Err()
}

// ViolationTombstoned reports whether this content address was hard-deleted while
// carrying a violation verdict. The verdict is the discriminator, not the deletion:
// an owner who removed their own skin must be able to upload it again, and only bytes
// a moderator struck are refused. Callers hold the hash lock, since a delete racing
// the check could move the verdict the answer is based on.
func (m *Mysql) ViolationTombstoned(ctx context.Context, typ, hash string) (bool, error) {
	var struck int
	err := m.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM texture_tombstones
		  WHERE type = ? AND hash = ? AND moderation = 'violation'`,
		typ, hash).Scan(&struck)
	if err != nil {
		return false, err
	}
	return struck > 0, nil
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
	return m.queryTextures(ctx,
		`SELECT id, type, hash, COALESCE(model,''), COALESCE(description,''),
		        COALESCE(detail_md,''), COALESCE(source,''), COALESCE(source_url,''), upload_at,
		        EXISTS (SELECT 1 FROM user_closet c WHERE c.user_uid = t.uid AND c.texture_tid = t.id)
		 FROM textures t WHERE t.uid = ? ORDER BY t.id DESC`, uid)
}

// ListTexturesPage returns one page plus the count that matches the same
// filters. An uploader with hundreds of textures used to get every row rendered
// at once, and with no way to find one short of paging through it by hand while
// the library and the closet both already search as you type.
//
// q matches the name or the content address; typ is "skin"/"cape" and anything
// else (including "") means "no type filter" — the caller validates, this stays
// total: the filter cannot turn into an unbounded scan.
func (m *Mysql) ListTexturesPage(ctx context.Context, uid int64, q, typ string, limit, offset int) ([]TextureRow, int64, error) {
	where := "WHERE t.uid = ?"
	args := []any{uid}
	if typ == "skin" || typ == "cape" {
		where += " AND t.type = ?"
		args = append(args, typ)
	}
	if q = strings.TrimSpace(q); q != "" {
		like := dialect.LikeContains(q)
		where += " AND (" + dialect.CIContainsSQL("COALESCE(t.description,'')") +
			" OR " + dialect.CIContainsSQL("t.hash") + ")"
		args = append(args, like, like)
	}
	var total int64
	if err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM textures t `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := m.queryTextures(ctx,
		`SELECT id, type, hash, COALESCE(model,''), COALESCE(description,''),
		        COALESCE(detail_md,''), COALESCE(source,''), COALESCE(source_url,''), upload_at,
		        EXISTS (SELECT 1 FROM user_closet c WHERE c.user_uid = t.uid AND c.texture_tid = t.id)
		 FROM textures t `+where+` ORDER BY t.id DESC LIMIT ? OFFSET ?`,
		append(append([]any{}, args...), limit, offset)...)
	return rows, total, err
}

func (m *Mysql) queryTextures(ctx context.Context, query string, args ...any) ([]TextureRow, error) {
	rows, err := m.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TextureRow
	for rows.Next() {
		var t TextureRow
		if err := rows.Scan(&t.ID, &t.Type, &t.Hash, &t.Model, &t.Description,
			&t.DetailMD, &t.Source, &t.SourceURL, &t.UploadAt, &t.InCloset); err != nil {
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
	DetailMD    string
	Source      string
	SourceURL   string
	UploadAt    time.Time
	// InCloset marks whether the owner already collected it, so their own list
	// can say so without shipping the whole closet to the browser.
	InCloset bool
}

// TextureMeta carries the user-editable fields written on upload/import.
// Description is the short display name; DetailMD is the long Markdown body.
type TextureMeta struct {
	Description string
	DetailMD    string
	Source      string
	SourceURL   string
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

// NewestPublicSkin picks the most recent skin the library actually serves, for
// the admin self-check's preview probe. One real row is the point: it exercises
// the file layer and the renderer with actual data, where a built-in sample
// could never fail. ErrNotFound just means the library is still empty.
func (m *Mysql) NewestPublicSkin(ctx context.Context) (hash string, tid int64, slim bool, err error) {
	var model sql.NullString
	err = m.db.QueryRowContext(ctx,
		`SELECT hash, id, model FROM textures
		  WHERE type = 'skin' AND public = 1 AND moderation <> 'violation'
		  ORDER BY id DESC LIMIT 1`).
		Scan(&hash, &tid, &model)
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
		where = "WHERE " + m.db.Dialect().CIContains("COALESCE(email,'')") + " OR " + m.db.Dialect().CIContains("nickname")
		like := dialect.LikeContains(q)
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
		append(args, size, dbx.Offset(page, size))...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]map[string]interface{}, 0)
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

// AdminTextures returns a page of texture records for the panel.
//
// The three filters are the questions an operator actually asks — 「这个人传的
// 那张在哪」(name / hash / uploader), 皮肤还是披风, 还在库里还是在货架上 — and
// none of them could be answered when the view only paged by id. The nickname
// rides along on the row because a bare `#7` says nothing about who to contact.
// `state` is deliberately one dropdown over two columns: 「已判违规」 is the
// verdict, 公开/私密 is the listing, and a struck texture always sits at
// public=0, so the three choices stay mutually meaningful.
func (m *Mysql) AdminTextures(ctx context.Context, q, typ, state string, page, size int) ([]map[string]interface{}, int, error) {
	d := m.db.Dialect()
	var cond []string
	var args []interface{}
	if s := strings.TrimSpace(q); s != "" {
		cond = append(cond, "("+d.CIContains("COALESCE(t.description, '')")+` OR `+
			d.CIContains("t.hash")+` OR `+d.CIContains("COALESCE(u.nickname, '')")+")")
		like := dialect.LikeContains(s)
		args = append(args, like, like, like)
	}
	switch typ {
	case "skin", "cape":
		cond = append(cond, `t.type = ?`)
		args = append(args, typ)
	}
	switch state {
	case "public":
		cond = append(cond, `t.public = 1`)
	case "private":
		cond = append(cond, `t.public = 0`)
	case "struck":
		cond = append(cond, `t.moderation = 'violation'`)
	}
	where := ""
	if len(cond) > 0 {
		where = "WHERE " + strings.Join(cond, " AND ")
	}
	from := ` FROM textures t LEFT JOIN users u ON u.id = t.uid ` + where
	var total int
	if err := m.db.QueryRowContext(ctx, `SELECT COUNT(*)`+from, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := m.db.QueryContext(ctx,
		`SELECT t.id, t.uid, t.type, t.hash, COALESCE(t.model,''), COALESCE(t.description,''),
		        t.public, t.moderation, t.upload_at, COALESCE(u.nickname, '')`+from+`
		 ORDER BY t.id DESC LIMIT ? OFFSET ?`, append(args, size, dbx.Offset(page, size))...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]map[string]interface{}, 0)
	for rows.Next() {
		var id, uid, pub int64
		var typ2, hash, model, name, moderation, nick string
		var up time.Time
		if err := rows.Scan(&id, &uid, &typ2, &hash, &model, &name, &pub, &moderation, &up, &nick); err != nil {
			return nil, 0, err
		}
		// Visibility and verdict travel together: with only the hash on screen,
		// a struck texture that is still listed looks exactly like a live one.
		row := map[string]interface{}{
			"id": id, "uid": uid, "type": typ2, "hash": hash, "model": model, "name": name,
			"nickname": nick, "public": pub == 1, "moderation": moderation,
			"uploadAt": up.Format(time.RFC3339),
		}
		if typ2 == "skin" {
			row["previewURL"] = avatargen.PreviewThumb(id)
		} else {
			row["previewURL"] = "" // no body render for a cape
		}
		out = append(out, row)
	}
	return out, total, rows.Err()
}

// AdminAudit pages the audit log and reports the whole count. It replaced
// RecentAudit(limit), which silently stopped showing history at the cap and gave
// the operator no way to know more rows exist.
func (m *Mysql) AdminAudit(ctx context.Context, q, action string, page, size int) ([]map[string]interface{}, int, error) {
	if page < 1 {
		page = 1
	}
	d := m.db.Dialect()
	var cond []string
	var args []interface{}
	if s := strings.TrimSpace(q); s != "" {
		// 「谁在什么时候干了什么」 is asked in names: the actor's nickname, the
		// action, and the ids/names inside the detail JSON all belong to one
		// search box rather than three.
		cond = append(cond, "("+d.CIContains("a.action")+` OR `+d.CIContains("COALESCE(a.detail, '{}')")+
			` OR `+d.CIContains("COALESCE(u.nickname, '')")+")")
		like := dialect.LikeContains(s)
		args = append(args, like, like, like)
	}
	if a := strings.TrimSpace(action); a != "" {
		cond = append(cond, `a.action = ?`)
		args = append(args, a)
	}
	where := ""
	if len(cond) > 0 {
		where = "WHERE " + strings.Join(cond, " AND ")
	}
	from := ` FROM audit_log a LEFT JOIN users u ON u.id = a.actor_uid ` + where
	var total int
	if err := m.db.QueryRowContext(ctx, `SELECT COUNT(*)`+from, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := m.db.QueryContext(ctx,
		`SELECT a.id, COALESCE(a.actor_uid,0), a.action, COALESCE(a.detail,'{}'), a.created_at,
		        COALESCE(u.nickname, '')`+from+`
		 ORDER BY a.id DESC LIMIT ? OFFSET ?`, append(args, size, dbx.Offset(page, size))...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]map[string]interface{}, 0)
	for rows.Next() {
		var id, actor int64
		var action2, detail, nick string
		var created time.Time
		if err := rows.Scan(&id, &actor, &action2, &detail, &created, &nick); err != nil {
			return nil, 0, err
		}
		out = append(out, map[string]interface{}{
			"id": id, "actorUid": actor, "actorName": nick, "action": action2, "detail": detail,
			"createdAt": created.Format(time.RFC3339),
		})
	}
	return out, total, rows.Err()
}

// AuditActions lists the action names actually present in the log, newest first.
// The dropdown is built from the data because the set grows with every feature
// that audits itself; a hardcoded list would silently hide the newer events.
func (m *Mysql) AuditActions(ctx context.Context) ([]string, error) {
	rows, err := m.db.QueryContext(ctx,
		`SELECT DISTINCT action FROM audit_log ORDER BY action LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
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

	pid, err := tx.InsertReturningID(ctx, `INSERT INTO players (uid, name) VALUES (?,?)`, uid, name)
	if err != nil {
		return 0, "", wrapPlayerCollision(err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO uuid_map (name, uuid, player_id) VALUES (?,?,?)`, name, uuid, pid); err != nil {
		return 0, "", wrapPlayerCollision(err)
	}
	return pid, uuid, tx.Commit()
}

// wrapPlayerCollision maps a players.name OR uuid_map unique-key collision onto
// ErrDuplicate — the same treatment AdminRenamePlayer gives at
// player_admin.go:103/:111. Without it, a name abandoned by a rename re-derives
// the identical OfflineUUID the renamed row still owns (铁律 2 keeps the uuid),
// the uuid_map insert fails on UNIQUE(uuid), and the raw driver error escapes as
// an opaque 500-shaped answer (CodeInternal) for what is really "the name is not
// available". The driver text is kept in the chain for diagnostics.
func wrapPlayerCollision(err error) error {
	if err == nil || !isDupKey(err) {
		return err
	}
	return fmt.Errorf("%w: %v", ErrDuplicate, err)
}

// SetPlayerTextures points a player at texture ids (0 = clear).
//
// The zero means NULL, and it is decided here rather than with
// `CASE WHEN ? = 0 THEN NULL ELSE ? END`: PostgreSQL cannot type a placeholder
// that only ever appears inside such a comparison, so the same statement that
// MySQL and SQLite accept fails with "could not determine data type of
// parameter". Binding the value the caller already knows is also one fewer
// thing for the reader to unwrap.
func (m *Mysql) SetPlayerTextures(ctx context.Context, pid, skinTid, capeTid int64) error {
	_, err := m.db.ExecContext(ctx,
		`UPDATE players SET tid_skin = ?, tid_cape = ? WHERE id = ?`,
		nullableTID(skinTid), nullableTID(capeTid), pid)
	return err
}

// nullableTID maps a legacy "0 means unset" texture id onto a NULL column.
func nullableTID(tid int64) any {
	if tid == 0 {
		return nil
	}
	return tid
}

// SetUserAvatar points users.avatar_tid at a texture the user owns
// (tid=0 clears — blessing 更改头像/重置头像 semantics; legacy column
// users.avatar carried the same tid meaning). Ownership is the CALLER's
// job (webapi checks TextureOwner first); this method only writes.
func (m *Mysql) SetUserAvatar(ctx context.Context, uid, tid int64) error {
	_, err := m.db.ExecContext(ctx,
		`UPDATE users SET avatar_tid = ? WHERE id = ?`, nullableTID(tid), uid)
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
