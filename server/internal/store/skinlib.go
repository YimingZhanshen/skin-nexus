package store

// Skinlib (public texture library) queries — blessing SkinlibController
// parity. Legacy semantics (refs SkinlibController.php:60-95):
//   - visibility: anonymous → public only; user → public OR own;
//     admin (permission>0) → all
//   - filter: skin (type=skin) | cape | steve (default model) | alex (slim)
//   - keyword: LIKE on the texture name (our `description` column)
//   - sort: time (upload_at) | likes
//   - paginate 20/page
// Likes are NOT a denormalized column here: legacy `textures.likes` was a
// counter maintained on closet add/remove, but its source of truth is the
// user_closet pivot (Texture.php:65 likers() = belongsToMany via
// user_closet). We count the pivot directly — zero schema change, no
// drift, and the imported closet rows carry the legacy counts.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// SkinlibRow is one library entry with uploader + like state.
type SkinlibRow struct {
	TID       int64
	UID       int64
	Name      string
	Type      string
	Model     string
	Hash      string
	Public    bool
	Nickname  string
	Likes     int64
	Liked     bool
	DetailMD  string
	Source    string
	SourceURL string
	UploadAt  sql.NullTime
}

// SkinlibOpts filters one library page.
type SkinlibOpts struct {
	ViewerUID   int64 // 0 = anonymous
	ViewerAdmin bool
	PublicOnly  bool   // ignore own private rows (public launcher library)
	Type        string // "", "skin", "cape", "steve", "alex"
	Keyword     string
	Uploader    int64  // 0 = any
	Sort        string // "time" (default) | "likes"
	Page        int    // 1-based
	PerPage     int    // default 20, capped 50
}

func (o *SkinlibOpts) normalize() {
	switch o.Type {
	case "skin", "cape":
	case "steve":
		o.Type = "skin:default"
	case "alex":
		o.Type = "skin:slim"
	default:
		o.Type = ""
	}
	o.Keyword = strings.TrimSpace(o.Keyword)
	if o.Sort != "likes" {
		o.Sort = "time"
	}
	if o.Page < 1 {
		o.Page = 1
	}
	if o.PerPage <= 0 || o.PerPage > 50 {
		o.PerPage = 20
	}
}

// where builds the shared WHERE clause. Visibility is applied first so a
// private texture never leaks through keyword/sort side channels.
func (o *SkinlibOpts) where() (clause string, args []any) {
	var conds []string
	switch {
	case o.PublicOnly:
		conds = append(conds, "t.public = 1")
	case o.ViewerUID == 0:
		conds = append(conds, "t.public = 1")
	case o.ViewerAdmin:
		// sees everything
	default:
		conds = append(conds, "(t.public = 1 OR t.uid = ?)")
		args = append(args, o.ViewerUID)
	}
	switch o.Type {
	case "skin":
		conds = append(conds, "t.type = 'skin'")
	case "cape":
		conds = append(conds, "t.type = 'cape'")
	case "skin:default":
		conds = append(conds, "t.type = 'skin' AND t.model = 'default'")
	case "skin:slim":
		conds = append(conds, "t.type = 'skin' AND t.model = 'slim'")
	}
	if o.Keyword != "" {
		conds = append(conds, "t.description LIKE ?")
		args = append(args, "%"+o.Keyword+"%")
	}
	if o.Uploader != 0 {
		conds = append(conds, "t.uid = ?")
		args = append(args, o.Uploader)
	}
	return strings.Join(conds, " AND "), args
}

// SkinlibList returns one page of library entries plus the total match
// count (for pagination). Likes/liked come from the user_closet pivot.
func (m *Mysql) SkinlibList(ctx context.Context, opt SkinlibOpts) ([]SkinlibRow, int, error) {
	opt.normalize()
	where, args := opt.where()

	var total int
	if err := m.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM textures t WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("skinlib count: %w", err)
	}

	orderBy := "t.upload_at DESC, t.id DESC"
	if opt.Sort == "likes" {
		orderBy = "likes DESC, t.upload_at DESC, t.id DESC"
	}
	// The liked flag needs the viewer uid even for anonymous (→ 0 = never
	// matches a real uid, EXISTS is simply false).
	// COALESCE: migrated rows may carry NULL description and capes NULL
	// model (legacy name/type columns were nullable) — scanning NULL
	// into string errors at runtime while a zero-row filter masks it.
	sel := fmt.Sprintf(`
		SELECT t.id, t.uid, COALESCE(t.description,''), t.type,
		       COALESCE(t.model,'default'), t.hash, t.public,
		       t.upload_at, u.nickname,
		       (SELECT COUNT(*) FROM user_closet c WHERE c.texture_tid = t.id) AS likes,
		       EXISTS(SELECT 1 FROM user_closet c WHERE c.texture_tid = t.id AND c.user_uid = ?) AS liked,
		       COALESCE(t.detail_md,''), COALESCE(t.source,''), COALESCE(t.source_url,'')
		FROM textures t
		JOIN users u ON u.id = t.uid
		WHERE %s
		ORDER BY %s
		LIMIT ? OFFSET ?`, where, orderBy)
	qargs := append([]any{opt.ViewerUID}, args...)
	qargs = append(qargs, opt.PerPage, (opt.Page-1)*opt.PerPage)

	rows, err := m.db.QueryContext(ctx, sel, qargs...)
	if err != nil {
		return nil, 0, fmt.Errorf("skinlib list: %w", err)
	}
	defer rows.Close()
	var out []SkinlibRow
	for rows.Next() {
		var r SkinlibRow
		if err := rows.Scan(&r.TID, &r.UID, &r.Name, &r.Type, &r.Model, &r.Hash,
			&r.Public, &r.UploadAt, &r.Nickname, &r.Likes, &r.Liked,
			&r.DetailMD, &r.Source, &r.SourceURL); err != nil {
			return nil, 0, fmt.Errorf("skinlib scan: %w", err)
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// SkinlibOne returns a single library entry by tid with visibility applied
// (public → everyone; private → owner or admin). The public detail page and
// its sitemap entries rely on this gate — a private texture must 404 for
// anonymous viewers, never leak through the SEO page.
func (m *Mysql) SkinlibOne(ctx context.Context, tid, viewerUID int64, viewerAdmin bool) (SkinlibRow, error) {
	clause := "t.public = 1"
	args := []any{}
	switch {
	case viewerAdmin:
		clause = "1=1"
	case viewerUID != 0:
		clause = "(t.public = 1 OR t.uid = ?)"
		args = append(args, viewerUID)
	}
	sel := fmt.Sprintf(`
		SELECT t.id, t.uid, COALESCE(t.description,''), t.type,
		       COALESCE(t.model,'default'), t.hash, t.public,
		       t.upload_at, u.nickname,
		       (SELECT COUNT(*) FROM user_closet c WHERE c.texture_tid = t.id) AS likes,
		       EXISTS(SELECT 1 FROM user_closet c WHERE c.texture_tid = t.id AND c.user_uid = ?) AS liked,
		       COALESCE(t.detail_md,''), COALESCE(t.source,''), COALESCE(t.source_url,'')
		FROM textures t
		JOIN users u ON u.id = t.uid
		WHERE t.id = ? AND %s`, clause)
	qargs := append([]any{viewerUID}, args...)
	qargs = append(qargs, tid)

	var r SkinlibRow
	err := m.db.QueryRowContext(ctx, sel, qargs...).Scan(&r.TID, &r.UID, &r.Name, &r.Type,
		&r.Model, &r.Hash, &r.Public, &r.UploadAt, &r.Nickname, &r.Likes, &r.Liked,
		&r.DetailMD, &r.Source, &r.SourceURL)
	if errors.Is(err, sql.ErrNoRows) {
		return SkinlibRow{}, ErrNotFound
	}
	if err != nil {
		return SkinlibRow{}, fmt.Errorf("skinlib one: %w", err)
	}
	return r, nil
}

// TextureIDTime is one sitemap row: a public texture id plus its upload time
// (lastmod for the sitemap entry).
type TextureIDTime struct {
	TID      int64
	UploadAt sql.NullTime
}

// PublicTextureIDs lists public texture ids (bounded, ascending) — the
// sitemap source. Private textures never enumerate here.
func (m *Mysql) PublicTextureIDs(ctx context.Context, limit int) ([]TextureIDTime, error) {
	if limit <= 0 || limit > 5000 {
		limit = 5000
	}
	rows, err := m.db.QueryContext(ctx,
		`SELECT id, upload_at FROM textures WHERE public = 1 ORDER BY id LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("public texture ids: %w", err)
	}
	defer rows.Close()
	var out []TextureIDTime
	for rows.Next() {
		var r TextureIDTime
		if err := rows.Scan(&r.TID, &r.UploadAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ClosetLikes returns the like count of one texture (pivot count).
func (m *Mysql) ClosetLikes(ctx context.Context, tid int64) (int64, error) {
	var n int64
	err := m.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM user_closet WHERE texture_tid = ?`, tid).Scan(&n)
	return n, err
}

// RemoveClosetEntryByTID unlikes (removes the viewer's closet row for the
// texture). Unlike the row-id variant this is keyed by texture so the
// skinlib heart toggle needs no prior closet lookup.
func (m *Mysql) RemoveClosetEntryByTID(ctx context.Context, uid, tid int64) error {
	res, err := m.db.ExecContext(ctx,
		`DELETE FROM user_closet WHERE user_uid = ? AND texture_tid = ? LIMIT 1`,
		uid, tid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// TextureMetaPatch edits display metadata; nil fields stay unchanged.
type TextureMetaPatch struct {
	Name      *string
	DetailMD  *string
	Source    *string
	SourceURL *string
	Public    *bool
}

// UpdateTextureMeta edits display metadata and/or visibility of one texture.
// ownerUID scopes the UPDATE to the owner; pass 0 for admin.
func (m *Mysql) UpdateTextureMeta(ctx context.Context, tid, ownerUID int64, patch TextureMetaPatch) error {
	sets := make([]string, 0, 6)
	args := make([]any, 0, 8)
	if patch.Name != nil {
		sets = append(sets, "description = ?")
		args = append(args, *patch.Name)
	}
	if patch.DetailMD != nil {
		sets = append(sets, "detail_md = ?")
		args = append(args, nullableStr(*patch.DetailMD))
	}
	if patch.Source != nil {
		sets = append(sets, "source = ?")
		args = append(args, *patch.Source)
	}
	if patch.SourceURL != nil {
		sets = append(sets, "source_url = ?")
		args = append(args, nullableStr(*patch.SourceURL))
	}
	if patch.Public != nil {
		if *patch.Public {
			sets = append(sets, "public = 1", "public_dedupe = CONCAT(type, ':', hash)")
		} else {
			sets = append(sets, "public = 0", "public_dedupe = NULL")
		}
	}
	if len(sets) == 0 {
		return nil
	}
	q := "UPDATE textures SET " + strings.Join(sets, ", ") + " WHERE id = ?"
	args = append(args, tid)
	if ownerUID != 0 {
		q += " AND uid = ?"
		args = append(args, ownerUID)
	}
	res, err := m.db.ExecContext(ctx, q, args...)
	if err != nil {
		if isDuplicateKey(err) {
			return ErrDuplicatePublic
		}
		return fmt.Errorf("texture meta update: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// MySQL reports changed rows, not matched rows: a no-op edit (same
		// name/visibility) must still succeed for the authorized caller.
		var owner int64
		if err := m.db.QueryRowContext(ctx,
			`SELECT uid FROM textures WHERE id = ?`, tid).Scan(&owner); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if ownerUID != 0 && owner != ownerUID {
			return ErrForbidden
		}
	}
	return nil
}
