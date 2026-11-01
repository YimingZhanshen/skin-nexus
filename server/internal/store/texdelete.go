package store

import (
	"context"
	"database/sql"

	"errors"
	"fmt"
	"skin-nexus/server/internal/dbx"
	"time"
)

// ErrTextureInUse: characters, a closet entry or an avatar still points at the
// texture. The caller either unbinds it (force) or tells the user what blocks it.
var ErrTextureInUse = errors.New("store: texture still in use")

// ErrBusy: another writer holds this content address. Advisory locks are taken
// with a short timeout so a stuck peer surfaces as a retryable answer instead of
// a hanging request.
var ErrBusy = errors.New("store: texture busy, retry")

// TextureRefs counts what refers to one texture row.
type TextureRefs struct {
	Players []string
	Closets int
	Avatars int
}

func (r TextureRefs) Empty() bool {
	return len(r.Players) == 0 && r.Closets == 0 && r.Avatars == 0
}

// DeleteResult reports what was removed and how much of the content is left.
type DeleteResult struct {
	Hash      string
	Type      string
	ShareLeft int
	Refs      TextureRefs
}

// WithHashLock serialises work on one content address across the whole server.
// Uploads write the file before the row exists and deletion removes the file
// once no row refers to it, so the two must not interleave; no other mechanism
// covers both without a new table. Row locks alone cannot: the upload's file
// write happens outside any transaction.
//
// Everything that touches a hash's file life must run inside this lock, and
// must take database row locks only while already holding it — otherwise two
// orders of acquisition become possible.
func (m *Mysql) WithHashLock(ctx context.Context, hash string, fn func(context.Context) error) error {
	conn, err := m.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("hash lock: %w", err)
	}
	defer conn.Close()

	// MySQL caps lock names at 64 characters; the first 32 hex digits identify
	// a content address well beyond any realistic collision set, and a
	// collision would only cost an extra serialization.
	name := "tex:" + hash[:min(len(hash), 32)]
	var got sql.NullInt64
	if err := conn.QueryRowContext(ctx, `SELECT GET_LOCK(?, 5)`, name).Scan(&got); err != nil {
		return fmt.Errorf("hash lock %s: %w", name, err)
	}
	if !got.Valid {
		return fmt.Errorf("hash lock %s: not granted", name)
	}
	if got.Int64 != 1 {
		return ErrBusy
	}
	// GET_LOCK is per connection and the pool hands the same connection to the
	// next caller, so releasing it is not optional. Deferred in this order:
	// release first, then return the connection.
	defer func() {
		release, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(release, `SELECT RELEASE_LOCK(?)`, name)
	}()
	return fn(ctx)
}

// TextureContentRef reads the content address and type of one texture, so the
// caller can take its lock and describe the operation before the transaction.
func (m *Mysql) TextureContentRef(ctx context.Context, tid int64) (string, string, error) {
	var hash, typ string
	err := m.db.QueryRowContext(ctx, `SELECT hash, type FROM textures WHERE id = ?`, tid).Scan(&hash, &typ)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", fmt.Errorf("texture hash: %w", err)
	}
	return hash, typ, nil
}

// DeleteTexture removes one texture row with its moderation evidence, unbinding
// whatever refers to it when force allows. It reports ErrTextureInUse together
// with the blockers so the caller can name them.
//
// It does not touch the file: the caller decides that from ShareLeft, and must
// hold the hash lock across both.
func (m *Mysql) DeleteTexture(ctx context.Context, tid, ownerUID, actorUID int64, force bool, reason string) (DeleteResult, error) {
	var out DeleteResult
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()

	var uid int64
	err = tx.QueryRowContext(ctx,
		`SELECT uid, hash, type FROM textures WHERE id = ? FOR UPDATE`, tid).
		Scan(&uid, &out.Hash, &out.Type)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, fmt.Errorf("texture delete read: %w", err)
	}
	if ownerUID != 0 && uid != ownerUID {
		return out, ErrForbidden
	}

	refs, err := textureRefs(ctx, tx, tid)
	if err != nil {
		return out, err
	}
	out.Refs = refs
	if !refs.Empty() && !force {
		return out, ErrTextureInUse
	}
	if err := unbindTexture(ctx, tx, tid); err != nil {
		return out, err
	}

	// The tombstone is written from the row itself, so the evidence is exactly
	// what was stored, and no second read can slip between the two.
	res, err := tx.ExecContext(ctx,
		`INSERT INTO texture_tombstones
		   (texture_id, uid, type, hash, model, description, source, source_url,
		    public, moderation, upload_at, deleted_by, deleted_at, reason)
		 SELECT id, uid, type, hash, model, description, COALESCE(source,''), source_url,
		        public, moderation, upload_at, ?, NOW(3), ?
		   FROM textures WHERE id = ?`, actorUID, reason, tid)
	if err != nil {
		return out, fmt.Errorf("texture tombstone: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return out, ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM textures WHERE id = ?`, tid); err != nil {
		return out, fmt.Errorf("texture row delete: %w", err)
	}
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM textures WHERE hash = ?`, out.Hash).Scan(&out.ShareLeft); err != nil {
		return out, fmt.Errorf("texture share count: %w", err)
	}
	return out, tx.Commit()
}

func textureRefs(ctx context.Context, tx *dbx.Tx, tid int64) (TextureRefs, error) {
	var refs TextureRefs
	rows, err := tx.QueryContext(ctx,
		`SELECT name FROM players WHERE tid_skin = ? OR tid_cape = ? ORDER BY id LIMIT 10`, tid, tid)
	if err != nil {
		return refs, fmt.Errorf("texture refs players: %w", err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return refs, err
		}
		refs.Players = append(refs.Players, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return refs, err
	}
	rows.Close()

	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM user_closet WHERE texture_tid = ?`, tid).Scan(&refs.Closets); err != nil {
		return refs, fmt.Errorf("texture refs closet: %w", err)
	}
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users WHERE avatar_tid = ?`, tid).Scan(&refs.Avatars); err != nil {
		return refs, fmt.Errorf("texture refs avatar: %w", err)
	}
	return refs, nil
}

// unbindTexture clears every reference to a texture. Characters fall back to
// the built-in skin, which is what a NULL tid already means everywhere else.
func unbindTexture(ctx context.Context, tx *dbx.Tx, tid int64) error {
	stmts := []string{
		`UPDATE players SET tid_skin = NULL WHERE tid_skin = ?`,
		`UPDATE players SET tid_cape = NULL WHERE tid_cape = ?`,
		`UPDATE users SET avatar_tid = NULL WHERE avatar_tid = ?`,
		`DELETE FROM user_closet WHERE texture_tid = ?`,
	}
	for _, q := range stmts {
		if _, err := tx.ExecContext(ctx, q, tid); err != nil {
			return fmt.Errorf("texture unbind: %w", err)
		}
	}
	return nil
}
