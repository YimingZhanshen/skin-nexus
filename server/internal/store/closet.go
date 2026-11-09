package store

import (
	"context"
	"database/sql"
	"errors"
	"skin-nexus/server/internal/dialect"
	"strings"
)

// ClosetRow is one衣柜 entry joined with its texture metadata. Orphan legacy
// rows (texture_tid no longer in textures) stay visible with empty metadata —
// data-compat rule 7: they can still be listed and removed, never hidden.
type ClosetRow struct {
	ID        int64
	TextureID int64
	ItemName  string
	Type      string
	Hash      string
	Model     string
	// Description is the texture's own name. The player picker needs it to label
	// favourited textures the user does not own, where ItemName is usually empty.
	Description string
}

const closetCols = `c.id, c.texture_tid, COALESCE(c.item_name, ''),
		        COALESCE(t.type, ''), COALESCE(t.hash, ''), COALESCE(t.model, ''),
		        COALESCE(t.description, '')`

// ListCloset returns the user's closet, newest entry first.
func (m *Mysql) ListCloset(ctx context.Context, uid int64) ([]ClosetRow, error) {
	return m.queryCloset(ctx,
		`SELECT `+closetCols+`
		 FROM user_closet c
		 LEFT JOIN textures t ON t.id = c.texture_tid
		 WHERE c.user_uid = ?
		 ORDER BY c.id DESC`, uid)
}

// ListClosetPage returns one page of the closet plus the full count, optionally
// filtered by type and by alias / texture name / hash. Uploads auto-collect
// now, so a heavy uploader's closet is as long as their texture list.
func (m *Mysql) ListClosetPage(ctx context.Context, uid int64, q, typ string, limit, offset int) ([]ClosetRow, int64, error) {
	where := "c.user_uid = ?"
	args := []any{uid}
	if typ == "skin" || typ == "cape" {
		where += ` AND t.type = ?`
		args = append(args, typ)
	}
	if s := strings.TrimSpace(q); s != "" {
		d := m.db.Dialect()
		where += ` AND (` + d.CIContains("COALESCE(c.item_name, '')") + ` OR ` +
			d.CIContains("COALESCE(t.description, '')") + ` OR ` + d.CIContains("COALESCE(t.hash, '')") + `)`
		like := dialect.LikeContains(s)
		args = append(args, like, like, like)
	}
	from := ` FROM user_closet c LEFT JOIN textures t ON t.id = c.texture_tid WHERE ` + where
	var total int64
	if err := m.db.QueryRowContext(ctx, `SELECT COUNT(*)`+from, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	page := append(append([]any{}, args...), limit, offset)
	rows, err := m.queryCloset(ctx, `SELECT `+closetCols+from+` ORDER BY c.id DESC LIMIT ? OFFSET ?`, page...)
	return rows, total, err
}

func (m *Mysql) queryCloset(ctx context.Context, query string, args ...any) ([]ClosetRow, error) {
	rows, err := m.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ClosetRow, 0)
	for rows.Next() {
		var c ClosetRow
		if err := rows.Scan(&c.ID, &c.TextureID, &c.ItemName, &c.Type, &c.Hash, &c.Model, &c.Description); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// WearableRow is one entry of what a user may put on a player.
type WearableRow struct {
	ClosetID int64 // the entry this row IS; PUT /me/closet/{id} addresses it
	ID       int64
	Hash     string
	Model    string
	Alias    string // the user's own closet name for it, may be empty
	Name     string // the texture's own description
}

// WearableTextures is the set a player may wear: exactly the user's closet.
// Blessing gates the skin picker on the closet (PlayerController), and this
// project keeps that single concept — an own upload reaches the picker because
// upload collects it (and migration 0022 collected the history), not because
// the picker also unions the uploads table.
//
// One row per texture, and that row is ONE closet entry: the newest one holding
// the texture. user_closet keeps the legacy shape with no unique constraint
// (data-compat rule 7), and AddClosetEntry is a check-then-insert, so a
// double-click or a direct import can leave the same pair twice. Grouping with
// MAX(item_name) used to resolve such a texture to the alphabetically largest
// alias while ordering by MAX(id) — so the label shown and the entry a rename
// would address were different rows. A derived table over (texture_tid, MAX(id))
// pins both to the same entry; the join is plain SQL, so all three engines read
// it the same way, and because each texture now appears once the total is a
// COUNT(*) over the join rather than a COUNT(DISTINCT …).
//
// The picker pages and searches, so it gets the count back as total. Search
// matches the row that is displayed — the newest entry's alias, the texture's
// own description, or its hash.
func (m *Mysql) WearableTextures(ctx context.Context, uid int64, typ, q string, limit, offset int) ([]WearableRow, int64, error) {
	d := m.db.Dialect()
	where := "c.user_uid = ? AND t.type = ?"
	// The subquery's own uid placeholder comes first in the statement text.
	args := []any{uid, uid, typ}
	if s := strings.TrimSpace(q); s != "" {
		where += ` AND (` + d.CIContains("COALESCE(c.item_name, '')") + ` OR ` +
			d.CIContains("COALESCE(t.description, '')") + ` OR ` + d.CIContains("COALESCE(t.hash, '')") + `)`
		like := dialect.LikeContains(s)
		args = append(args, like, like, like)
	}
	newest := `(SELECT texture_tid, MAX(id) AS id FROM user_closet WHERE user_uid = ? GROUP BY texture_tid) n`
	from := ` FROM user_closet c JOIN textures t ON t.id = c.texture_tid JOIN ` + newest +
		` ON n.id = c.id WHERE ` + where
	var total int64
	if err := m.db.QueryRowContext(ctx, `SELECT COUNT(*)`+from, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	page := append(append([]any{}, args...), limit, offset)
	rows, err := m.db.QueryContext(ctx, `
		SELECT c.id, c.texture_tid, t.hash, COALESCE(t.model, ''),
		       COALESCE(c.item_name, ''), COALESCE(t.description, '')`+from+`
		ORDER BY c.id DESC LIMIT ? OFFSET ?`, page...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]WearableRow, 0)
	for rows.Next() {
		var w WearableRow
		if err := rows.Scan(&w.ClosetID, &w.ID, &w.Hash, &w.Model, &w.Alias, &w.Name); err != nil {
			return nil, 0, err
		}
		out = append(out, w)
	}
	return out, total, rows.Err()
}

// AddClosetEntry puts a texture into the closet. Idempotent: an existing
// (uid, tid) pair returns created=false without inserting — legacy had no
// unique key either, so the app dedupes (kept deliberately loose for
// direct-migration duplicates).
//
// Discoverability gate (review P1 IDOR fix, 2026-10-23): only the owner's own
// textures or PUBLIC textures may be collected (legacy skinlib visibility
// semantics). Another user's private texture is rejected as ErrNotFound so
// closet enumeration cannot leak private hashes/urls.
func (m *Mysql) AddClosetEntry(ctx context.Context, uid, tid int64, itemName string) (created bool, err error) {
	var owner, pub int64
	err = m.db.QueryRowContext(ctx,
		`SELECT uid, public FROM textures WHERE id = ?`, tid).Scan(&owner, &pub)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, err
	}
	if owner != uid && pub == 0 {
		return false, ErrNotFound // not discoverable — indistinguishable from missing
	}
	var one int
	err = m.db.QueryRowContext(ctx,
		`SELECT id FROM user_closet WHERE user_uid = ? AND texture_tid = ? LIMIT 1`,
		uid, tid).Scan(&one)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if _, err := m.db.ExecContext(ctx,
		`INSERT INTO user_closet (user_uid, texture_tid, item_name) VALUES (?, ?, ?)`,
		uid, tid, nullableStr(itemName)); err != nil {
		return false, err
	}
	return true, nil
}

// SetClosetItemName updates the alias of one entry (ownership scoped by uid).
// MySQL changed-rows semantics: same-value update on an existing row is ok.
func (m *Mysql) SetClosetItemName(ctx context.Context, uid, id int64, name string) error {
	res, err := m.db.ExecContext(ctx,
		`UPDATE user_closet SET item_name = ? WHERE id = ? AND user_uid = ?`,
		nullableStr(name), id, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	var one int
	if err := m.db.QueryRowContext(ctx,
		`SELECT id FROM user_closet WHERE id = ? AND user_uid = ?`, id, uid).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

// InCloset reports whether this texture sits in the user's closet — the whole
// of what account.SetTexture accepts as wearable, and the reason both upload
// (autoCloset) and migration 0022 keep own uploads collected.
func (m *Mysql) InCloset(ctx context.Context, uid, tid int64) (bool, error) {
	var one int
	err := m.db.QueryRowContext(ctx,
		`SELECT id FROM user_closet WHERE user_uid = ? AND texture_tid = ? LIMIT 1`,
		uid, tid).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// RemoveClosetEntry deletes one entry (ownership scoped by uid).
func (m *Mysql) RemoveClosetEntry(ctx context.Context, uid, id int64) error {
	res, err := m.db.ExecContext(ctx,
		`DELETE FROM user_closet WHERE id = ? AND user_uid = ?`, id, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
