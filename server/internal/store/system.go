package store

import "context"

// System facts for the admin status page (B1, 2026-10-24): read-only,
// MySQL 5.7-safe (no window functions, no CTEs).

// ServerVersion reports the database server's own version string ("5.7.44-log",
// "PostgreSQL 16.4 …", "3.50.2"). The panel shows it next to the engine name, so
// a station operator can tell which of the three they are sitting on.
func (m *Mysql) ServerVersion(ctx context.Context) (string, error) {
	var v string
	err := m.db.QueryRowContext(ctx, m.db.Dialect().VersionQuery()).Scan(&v)
	return v, err
}

// MigrationVersion reports the highest applied migration file marker
// (schema_migrations.file is lexicographically ordered: 0001_… < 0009_…).
func (m *Mysql) MigrationVersion(ctx context.Context) (string, error) {
	var v string
	err := m.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(file), '') FROM schema_migrations`).Scan(&v)
	return v, err
}
