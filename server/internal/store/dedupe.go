package store

import (
	"context"
	"fmt"
)

// BackfillPublicDedupe fills public_dedupe for one winner per (type, hash).
// Legacy duplicate public rows stay NULL so the unique index can be created
// without deleting or changing visibility; future writes still go through
// PublicTextureByHash and the unique constraint. Idempotent.
func (m *Mysql) BackfillPublicDedupe(ctx context.Context) (int, error) {
	rows, err := m.db.QueryContext(ctx,
		`SELECT MIN(id), type, hash FROM textures WHERE public = 1 GROUP BY type, hash`)
	if err != nil {
		return 0, fmt.Errorf("dedupe: list winners: %w", err)
	}
	defer rows.Close()
	type winner struct {
		id  int64
		key string
	}
	var winners []winner
	for rows.Next() {
		var id int64
		var typ, hash string
		if err := rows.Scan(&id, &typ, &hash); err != nil {
			return 0, err
		}
		winners = append(winners, winner{id: id, key: typ + ":" + hash})
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	updated := 0
	for _, w := range winners {
		res, err := m.db.ExecContext(ctx,
			`UPDATE textures SET public_dedupe = ? WHERE id = ? AND public = 1 AND public_dedupe IS NULL`,
			w.key, w.id)
		if err != nil {
			return updated, fmt.Errorf("dedupe: fill id=%d: %w", w.id, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			updated++
		}
	}
	return updated, nil
}
