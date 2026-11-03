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
