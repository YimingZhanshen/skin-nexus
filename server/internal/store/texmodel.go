package store

import (
	"context"
	"fmt"
	"strings"
)

// ModelGroup is one skin content address and the model values its rows
// currently carry. An empty string in Models stands for NULL. Rows sharing a
// hash hold identical pixels, so they must end up with one model.
type ModelGroup struct {
	Hash   string
	Models []string
}

// SkinModelGroups lists skin hashes for the model backfill. limit <= 0 means
// every group; ordering by hash keeps two runs comparable.
func (m *Mysql) SkinModelGroups(ctx context.Context, limit int) ([]ModelGroup, error) {
	q := `SELECT hash, GROUP_CONCAT(DISTINCT COALESCE(model, '') SEPARATOR ',')
	      FROM textures WHERE type = 'skin' GROUP BY hash ORDER BY hash`
	args := []any{}
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := m.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("texmodel: list skin groups: %w", err)
	}
	defer rows.Close()

	var out []ModelGroup
	for rows.Next() {
		var hash, joined string
		if err := rows.Scan(&hash, &joined); err != nil {
			return nil, fmt.Errorf("texmodel: scan group: %w", err)
		}
		out = append(out, ModelGroup{Hash: hash, Models: strings.Split(joined, ",")})
	}
	return out, rows.Err()
}

// SetModelByHash stamps one model on every skin row of that content address
// whose model is NULL or different, and reports how many rows changed. Rows
// already agreeing are left alone, so re-running costs nothing.
func (m *Mysql) SetModelByHash(ctx context.Context, hash, model string) (int64, error) {
	res, err := m.db.ExecContext(ctx,
		`UPDATE textures SET model = ?
		 WHERE type = 'skin' AND hash = ? AND (model IS NULL OR model <> ?)`,
		model, hash, model)
	if err != nil {
		return 0, fmt.Errorf("texmodel: set model for %s: %w", hash[:12], err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("texmodel: rows affected for %s: %w", hash, err)
	}
	return n, nil
}
