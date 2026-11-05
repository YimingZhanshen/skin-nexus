package store

import (
	"context"
)

// RecycledSlot is one id that a tombstone still cites while a different texture
// occupies it. It can only come from before SQLite stopped handing a deleted row's
// id to the next insert (see internal/migrate/rowid.go), and it is the one history
// mismatch this database can prove: the tombstone carries the content address, the
// report does not.
type RecycledSlot struct {
	TextureID     int64
	TombstoneHash string
	LiveHash      string
}

// FSCKHashUsage counts the live rows per content address. A file whose address is
// absent here is a file no library entry can reach.
func (m *Mysql) FSCKHashUsage(ctx context.Context) (map[string]int, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT hash, COUNT(*) FROM textures GROUP BY hash`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var hash string
		var n int
		if err := rows.Scan(&hash, &n); err != nil {
			return nil, err
		}
		out[hash] = n
	}
	return out, rows.Err()
}

// FSCKTombstonedHashes lists the addresses a deletion left as evidence. Their files
// are invisible to the site but they are the record of what was struck, so the
// repair pass leaves them alone instead of unlinking the only proof.
func (m *Mysql) FSCKTombstonedHashes(ctx context.Context) (map[string]bool, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT DISTINCT hash FROM texture_tombstones`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			return nil, err
		}
		out[hash] = true
	}
	return out, rows.Err()
}

// FSCKRecycledSlots reports every tombstone that shares an id with a live row of
// different content.
func (m *Mysql) FSCKRecycledSlots(ctx context.Context) ([]RecycledSlot, error) {
	rows, err := m.db.QueryContext(ctx,
		`SELECT ts.texture_id, ts.hash, t.hash
		   FROM texture_tombstones ts JOIN textures t ON t.id = ts.texture_id
		  WHERE ts.hash <> t.hash ORDER BY ts.texture_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RecycledSlot
	for rows.Next() {
		var s RecycledSlot
		if err := rows.Scan(&s.TextureID, &s.TombstoneHash, &s.LiveHash); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// FSCKCounts answers the questions the operator cannot ask from the panel: how much
// of the library is there, and how much deletion evidence has piled up behind it.
func (m *Mysql) FSCKCounts(ctx context.Context) (textures, tombstones, openReports int, err error) {
	if err = m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM textures`).Scan(&textures); err != nil {
		return
	}
	if err = m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM texture_tombstones`).Scan(&tombstones); err != nil {
		return
	}
	err = m.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM texture_reports WHERE status = 'open'`).Scan(&openReports)
	return
}

// FSCKDuplicateCloset counts (account, texture) pairs stored more than once, and
// the number of rows above the one each pair needs.
//
// The library's like count is a COUNT over this pivot, so duplicates are not just
// spare bytes — they inflate the number every visitor sees. The cause is known
// (AddClosetEntry does SELECT then INSERT with no transaction and no unique key);
// the repair is a unique index, which cannot be added before these rows are
// resolved, so this tool reports the size of that decision instead of making it.
func (m *Mysql) FSCKDuplicateCloset(ctx context.Context) (pairs, extra int, err error) {
	err = m.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(c - 1), 0)
		   FROM (SELECT COUNT(*) AS c FROM user_closet GROUP BY user_uid, texture_tid HAVING COUNT(*) > 1) d`).
		Scan(&pairs, &extra)
	return
}

// FSCKClosetOnMissingTextures counts wardrobe rows whose texture no longer exists.
// The rows are invisible in the UI (the join drops them) but they keep the
// texture's tombstone cited, so an operator reading「已彻底删除」sees the file
// reappear as still-referenced.
func (m *Mysql) FSCKClosetOnMissingTextures(ctx context.Context) (int, error) {
	var n int
	err := m.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM user_closet c
		  LEFT JOIN textures t ON t.id = c.texture_tid
		 WHERE t.id IS NULL`).Scan(&n)
	return n, err
}

// FSCKCredentialDigestState answers「一份库备份里能不能直接读到活的登录态」.
//
// After the digest build has booted, those columns hold hex(sha256(value));
// before it they hold the exact string a client presents — and nothing about the
// stored value separates the two forms (same length, same alphabet), which is the
// reason the reads were rewritten instead of doubled. So this reports the marker
// the rewrite commits, next to the number of credential rows it would have
// covered: a count without the marker proves nothing, and the marker without the
// count hides a site whose tables are still full of live sessions.
// `ygg_tokens.client_token` is deliberately absent: it stays verbatim.
func (m *Mysql) FSCKCredentialDigestState(ctx context.Context) (rows int, migrated bool, err error) {
	for _, q := range []string{
		`SELECT COUNT(*) FROM ygg_tokens WHERE access_token <> ''`,
		`SELECT COUNT(*) FROM ygg_join_sessions WHERE access_token <> ''`,
		`SELECT COUNT(*) FROM web_sessions`,
		`SELECT COUNT(*) FROM oidc_refresh_tokens WHERE token <> ''`,
	} {
		var n int
		if err = m.db.QueryRowContext(ctx, q).Scan(&n); err != nil {
			return
		}
		rows += n
	}
	var marked int
	if err = m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM options WHERE k = ?`, tokenDigestMarker).Scan(&marked); err != nil {
		return
	}
	return rows, marked > 0, nil
}

// FSCKSitemapCoverage compares the public library with what the sitemap can
// enumerate. PublicTextureIDs takes the newest ids up to SitemapIDLimit, so on a
// site past that ceiling the omitted pages are the oldest skins — content a
// crawler has most likely already been told about, which is why this order is the
// cheap one. Read-only by design: which urls get published is the operator's
// call, not this pass's.
func (m *Mysql) FSCKSitemapCoverage(ctx context.Context) (public, listed int, err error) {
	if err = m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM textures WHERE public = 1`).Scan(&public); err != nil {
		return
	}
	listed = public
	if listed > SitemapIDLimit {
		listed = SitemapIDLimit
	}
	return
}
