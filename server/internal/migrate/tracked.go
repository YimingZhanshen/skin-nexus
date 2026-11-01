// tracked.go: apply the per-engine migration tree with per-file markers, so
// EXISTING databases receive new migration files too (option A, 拍板
// 2026-10-23 — the empty-DB-only path left 0006 (invitation_codes) unapplied on
// the live test instance).
//
// Layout: <root>/<engine>/*.sql, one tree per engine (mysql | sqlite |
// postgres). The DDL is authored per engine rather than rewritten at runtime:
// inline KEY clauses, ENUM, unsigned integers, ON UPDATE and the case-folding
// collations differ too much for a textual translation to be trustworthy.
//
// Semantics:
//   - schema_migrations(file PK, engine, applied_at) records completed files;
//     `engine` is the tripwire that stops a boot from pointing one tree at a
//     database created by another one;
//   - unmarked files run on every SKIN_AUTO_MIGRATE boot, in filename order;
//   - a statement failing with a duplicate-OBJECT collision (table/index/
//     column already exists) is tolerated — pre-tracking databases
//     self-bootstrap — and the file still gets marked;
//   - any other error fails loud (log.Fatal at the caller);
//   - migration files stay DDL-only: no data INSERTs (a re-run on a
//     pre-tracking DB would otherwise double-apply).
package migrate

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"skin-nexus/server/internal/dialect"
)

// markerDDL is the schema_migrations bootstrap, one statement per engine. It
// lives here instead of in a migration file because the marker has to exist
// before the first file can be recorded.
var markerDDL = map[dialect.Engine]string{
	dialect.MySQL: `CREATE TABLE IF NOT EXISTS schema_migrations (
	file   VARCHAR(191) NOT NULL,
	engine VARCHAR(16)  NOT NULL DEFAULT 'mysql',
	applied_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
	PRIMARY KEY (file)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	dialect.SQLite: `CREATE TABLE IF NOT EXISTS schema_migrations (
	file   VARCHAR(191) NOT NULL,
	engine VARCHAR(16)  NOT NULL DEFAULT 'sqlite',
	applied_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f +0000 UTC','now')),
	PRIMARY KEY (file)
)`,
	dialect.Postgres: `CREATE TABLE IF NOT EXISTS schema_migrations (
	file   VARCHAR(191) NOT NULL,
	engine VARCHAR(16)  NOT NULL DEFAULT 'postgres',
	applied_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY (file)
)`,
}

// TreeDir is the migration directory for one engine inside root.
func TreeDir(root string, eng dialect.Engine) string {
	return filepath.Join(root, string(eng))
}

// ApplySQLDirTracked applies the pending *.sql files of this engine's tree
// under root, in filename order, skipping files already recorded.
func ApplySQLDirTracked(db *sql.DB, root string, eng dialect.Engine) error {
	if err := ensureMarkerTable(db, eng); err != nil {
		return err
	}
	dir := TreeDir(root, eng)
	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("migrate: no *.sql found in %s", dir)
	}
	sort.Strings(files)
	var recorded string
	var other int
	if err := db.QueryRow(eng.Bind(`SELECT COUNT(*) FROM schema_migrations WHERE engine <> ?`), string(eng)).Scan(&other); err != nil {
		return fmt.Errorf("migrate: marker read: %w", err)
	}
	if other > 0 {
		_ = db.QueryRow(eng.Bind(`SELECT MAX(engine) FROM schema_migrations WHERE engine <> ?`), string(eng)).Scan(&recorded)
		return fmt.Errorf("migrate: this database was migrated as %q, not %q — point the connection at its own engine", recorded, string(eng))
	}
	for _, f := range files {
		base := filepath.Base(f)
		var one int
		switch err := db.QueryRow(eng.Bind(`SELECT 1 FROM schema_migrations WHERE file = ?`), base).Scan(&one); {
		case err == nil:
			continue // already applied
		case err != sql.ErrNoRows:
			return fmt.Errorf("migrate: marker read %s: %w", base, err)
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		for _, stmt := range SplitSQL(string(raw)) {
			if _, err := db.Exec(stmt); err != nil {
				if isDuplicateCollision(err) {
					continue // already took effect (pre-tracking database)
				}
				return fmt.Errorf("migrate: %s: %w\nstmt: %s", base, err, stmt)
			}
		}
		if _, err := db.Exec(eng.Bind(`INSERT INTO schema_migrations (file) VALUES (?)`), base); err != nil {
			return fmt.Errorf("migrate: mark %s: %w", base, err)
		}
	}
	return nil
}

// markerEngineColumn backfills `engine` on a marker table that predates
// multi-engine support. Spelled out per engine instead of interpolated: the
// default value has to be a literal in DDL, and a fixed string per engine keeps
// the engine identifier out of the SQL text.
var markerEngineColumn = map[dialect.Engine]string{
	dialect.MySQL:    `ALTER TABLE schema_migrations ADD COLUMN engine VARCHAR(16) NOT NULL DEFAULT 'mysql'`,
	dialect.SQLite:   `ALTER TABLE schema_migrations ADD COLUMN engine VARCHAR(16) NOT NULL DEFAULT 'sqlite'`,
	dialect.Postgres: `ALTER TABLE schema_migrations ADD COLUMN engine VARCHAR(16) NOT NULL DEFAULT 'postgres'`,
}

// ensureMarkerTable creates the marker table when it is genuinely absent
// (review C P2: an unconditional CREATE blocked boot on read-only /
// least-privilege databases even when every file was already marked) and adds
// the engine column to a table that predates multi-engine support. Adding the
// column with this engine as its default also backfills the rows a
// single-engine database already carries.
func ensureMarkerTable(db *sql.DB, eng dialect.Engine) error {
	if _, err := db.Exec(`SELECT 1 FROM schema_migrations LIMIT 1`); err != nil {
		ddl, ok := markerDDL[eng]
		if !ok {
			return fmt.Errorf("migrate: no schema_migrations bootstrap for engine %q", string(eng))
		}
		if _, err := db.Exec(ddl); err != nil {
			return fmt.Errorf("migrate: schema_migrations bootstrap: %w", err)
		}
	}
	// COUNT, not a LIMIT 1 read: an empty marker table answers "no rows", which
	// would look identical to "no column" on a row-scan probe.
	var n int
	if err := db.QueryRow(`SELECT COUNT(engine) FROM schema_migrations`).Scan(&n); err == nil {
		return nil
	} else if !isMissingColumn(err) {
		return fmt.Errorf("migrate: schema_migrations engine probe: %w", err)
	}
	add, ok := markerEngineColumn[eng]
	if !ok {
		return fmt.Errorf("migrate: no schema_migrations engine column for %q", string(eng))
	}
	if _, err := db.Exec(add); err != nil {
		if isDuplicateCollision(err) {
			return nil
		}
		return fmt.Errorf("migrate: schema_migrations engine column: %w", err)
	}
	return nil
}

// isMissingColumn reports the "this column does not exist yet" errors of the
// three engines — the probe that decides whether to add the marker column.
func isMissingColumn(err error) bool {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code == "42703" // undefined_column
	}
	msg := err.Error()
	return strings.Contains(msg, "no such column") || // SQLite
		strings.Contains(msg, "Unknown column") || // MySQL
		strings.Contains(msg, "1054")
}

// isDuplicateCollision matches the duplicate-object errors that mean "this
// statement already took effect", per engine:
//   - MySQL formats them as "Error 1050 (42S01): Table 'users' already exists"
//     (SQLSTATE included — the pre-fix matcher required "Error 1050:" and
//     crash-looped the deploy);
//   - SQLite says "table users already exists" / "duplicate column name: x";
//   - PostgreSQL answers with the 42P07 / 42701 / 42710 SQLSTATEs.
//
// ER1062 / 23505 (duplicate DATA entry) are deliberately NOT tolerated — data
// collisions must fail loud.
func isDuplicateCollision(err error) bool {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		switch pe.Code {
		case "42P07", // duplicate_table
			"42701", // duplicate_column
			"42710": // duplicate_object (index/constraint/trigger)
			return true
		}
		return false
	}
	msg := err.Error()
	for _, code := range []string{"Error 1050", "Error 1060", "Error 1061"} {
		if strings.Contains(msg, code) {
			return true
		}
	}
	return strings.Contains(msg, "already exists") || strings.Contains(msg, "duplicate column name")
}
