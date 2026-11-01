// schema.go: schema-bootstrap helpers for the empty-database init flow
// (docker first-run / fresh install).
package store

import (
	"context"
	"database/sql"
	"errors"

	"skin-nexus/server/internal/dbx"
)

// DB exposes the raw handle for the migrate package (schema bootstrap only;
// regular queries stay behind typed methods).
func (m *Mysql) DB() *sql.DB { return m.db.Underlying() }

// DBX hands the rewritten-statement pool to the subsystems that run their own
// SQL (the job queue). Anything taking DB() gets no placeholder rewriting and
// no SQLite UTC normalisation, so it is a MySQL-only path by accident.
func (m *Mysql) DBX() *dbx.DB { return m.db }

// TableExists reports whether a base table exists in the current schema.
func (m *Mysql) TableExists(ctx context.Context, name string) (bool, error) {
	var one int
	err := m.db.QueryRowContext(ctx, m.db.Dialect().TableExistsQuery(), name).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// NeedsSetup reports whether the site is uninitialized: schema missing or no
// user rows yet and setup_done not set. Backs the /setup install wizard gate.
func (m *Mysql) NeedsSetup(ctx context.Context) (bool, error) {
	ok, err := m.TableExists(ctx, "users")
	if err != nil {
		return false, err
	}
	if !ok {
		return true, nil
	}
	if done, found, err := m.GetOption(ctx, "setup_done"); err == nil && found && done == "1" {
		return false, nil
	}
	var n int
	if err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return false, err
	}
	return n == 0, nil
}
