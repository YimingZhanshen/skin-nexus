package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"skin-nexus/server/internal/dialect"
)

// rowIDTables are the SQLite tables whose ids outlive their rows. A bare
// INTEGER PRIMARY KEY *is* the rowid, so SQLite hands a freed id to the next
// insert; AUTOINCREMENT makes the counter monotone instead.
var rowIDTables = []string{"textures", "users", "players"}

type idReference struct{ table, column string }

// rowIDHistory lists, per table, the columns that still name rows which may
// already be gone. A hot table emptied before this fix keeps no counter of its
// own, so the ids history cites are what the counter has to be lifted past.
var rowIDHistory = map[string][]idReference{
	"textures": {
		{"texture_tombstones", "texture_id"},
		{"texture_reports", "texture_id"},
		{"user_closet", "texture_tid"},
		{"notifications", "tid"},
		{"players", "tid_skin"},
		{"players", "tid_cape"},
		{"users", "avatar_tid"},
	},
	"users": {
		{"textures", "uid"},
		{"players", "uid"},
		{"user_closet", "user_uid"},
		{"audit_log", "actor_uid"},
		{"texture_reports", "reporter_uid"},
		{"texture_reports", "handled_by"},
		{"texture_tombstones", "uid"},
		{"texture_tombstones", "deleted_by"},
		{"notifications", "uid"},
	},
	"players": {
		{"uuid_map", "player_id"},
	},
}

// EnsureNoRowIDReuse stops SQLite from handing a deleted row's id to the next
// insert. MySQL and PostgreSQL never move their counters backwards, so they get a
// nil here; MariaDB re-seeds from MAX(id) after a restart, which is recorded in
// docs/compat-matrix.md instead of being fixed from this side.
func EnsureNoRowIDReuse(db *sql.DB, eng dialect.Engine) error {
	if eng != dialect.SQLite {
		return nil
	}
	return rebuildRowIDTables(context.Background(), db, rowIDTables)
}

// rebuildRowIDTables converts each table in order and stops at the first failure,
// leaving the tables that come after it in the shape they arrived in.
//
// Every table name here is one this package declares, never one a caller or a
// request supplied, which is what makes the fmt.Sprintf DDL below safe.
func rebuildRowIDTables(ctx context.Context, db *sql.DB, tables []string) error {
	// One connection for the whole run: the migration loader hands statements to the
	// pool, and a rebuild spread over two connections is two transactions wearing
	// one name.
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("rowid reuse fix: %w", err)
	}
	defer conn.Close()

	for _, name := range tables {
		if err := rebuildOneRowID(ctx, conn, name); err != nil {
			return fmt.Errorf("rowid reuse fix for %s: %w", name, err)
		}
	}
	return nil
}

type execQuerier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func rebuildOneRowID(ctx context.Context, conn *sql.Conn, name string) error {
	ddl, err := tableDDLText(ctx, conn, name)
	if err != nil {
		return err
	}
	floor, err := rowIDFloor(ctx, conn, name)
	if err != nil {
		return err
	}
	if strings.Contains(ddl, "AUTOINCREMENT") {
		return liftRowIDSequence(ctx, conn, name, floor)
	}
	indexes, err := indexDDLTexts(ctx, conn, name)
	if err != nil {
		return err
	}
	rebuilt, err := withAutoIncrement(ddl, name)
	if err != nil {
		return err
	}
	staging := name + "_rowid_rebuild"

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, stmt := range []string{
		fmt.Sprintf("ALTER TABLE %s RENAME TO %s", name, staging),
		rebuilt,
		fmt.Sprintf("INSERT INTO %s SELECT * FROM %s", name, staging),
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("%s: %w", firstWords(stmt, 4), err)
		}
	}

	// The copy has to arrive whole. Anything else rolls the table back to the shape
	// it had and leaves the database unconverted rather than half converted.
	var moved, kept int64
	if err := tx.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", staging)).Scan(&moved); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", name)).Scan(&kept); err != nil {
		return err
	}
	if moved != kept {
		return fmt.Errorf("copy moved %d rows into a table holding %d", moved, kept)
	}

	if err := liftRowIDSequence(ctx, tx, name, floor); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("DROP TABLE %s", staging)); err != nil {
		return err
	}
	for _, create := range indexes {
		if _, err := tx.ExecContext(ctx, create); err != nil {
			return fmt.Errorf("restore index: %w", err)
		}
	}
	return tx.Commit()
}

// liftRowIDSequence raises the counter to floor without ever lowering it, so a
// rerun on a database that has moved ahead stays a no-op.
func liftRowIDSequence(ctx context.Context, q execQuerier, name string, floor int64) error {
	if floor <= 0 {
		return nil
	}
	if _, err := q.ExecContext(ctx,
		`UPDATE sqlite_sequence SET seq = ? WHERE name = ? AND seq < ?`, floor, name, floor); err != nil {
		return fmt.Errorf("lift counter: %w", err)
	}
	if _, err := q.ExecContext(ctx,
		`INSERT INTO sqlite_sequence (name, seq)
		 SELECT ?, ? WHERE NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name = ?)`,
		name, floor, name); err != nil {
		return fmt.Errorf("seed counter: %w", err)
	}
	return nil
}

func tableDDLText(ctx context.Context, q execQuerier, name string) (string, error) {
	var ddl string
	err := q.QueryRowContext(ctx,
		`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&ddl)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("no such table %s", name)
	}
	if err != nil {
		return "", err
	}
	return ddl, nil
}

func indexDDLTexts(ctx context.Context, conn *sql.Conn, name string) ([]string, error) {
	rows, err := conn.QueryContext(ctx,
		`SELECT sql FROM sqlite_master WHERE type = 'index' AND tbl_name = ? AND sql IS NOT NULL`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			return nil, err
		}
		out = append(out, text)
	}
	return out, rows.Err()
}

// rowIDFloor is the highest id this table has been seen to hold, counting the rows
// still here and every row that only survives as a reference elsewhere.
func rowIDFloor(ctx context.Context, q execQuerier, name string) (int64, error) {
	var floor int64
	for _, probe := range append([]idReference{{table: name, column: "id"}}, rowIDHistory[name]...) {
		var v sql.NullInt64
		err := q.QueryRowContext(ctx,
			fmt.Sprintf("SELECT MAX(%s) FROM %s", probe.column, probe.table)).Scan(&v)
		if err != nil {
			return 0, fmt.Errorf("highest id in %s.%s: %w", probe.table, probe.column, err)
		}
		if v.Valid && v.Int64 > floor {
			floor = v.Int64
		}
	}
	return floor, nil
}

// tableDDLRe matches the DDL of exactly the table being rebuilt: CREATE TABLE,
// optional IF NOT EXISTS, then the name as its own token.
var tableDDLRe = map[string]*regexp.Regexp{}

func tableMatcher(name string) *regexp.Regexp {
	if rx, ok := tableDDLRe[name]; ok {
		return rx
	}
	rx := regexp.MustCompile(`(?i)^CREATE TABLE\s+(IF NOT EXISTS\s+)?["` + "`" + `\[]?` + regexp.QuoteMeta(name) + `["` + "`" + `\]]?\s*\(`)
	tableDDLRe[name] = rx
	return rx
}

// withAutoIncrement rewrites the captured DDL instead of restating the column list,
// so a column added by a later migration cannot be dropped by an out-of-date copy.
func withAutoIncrement(ddl, name string) (string, error) {
	if n := strings.Count(ddl, "INTEGER PRIMARY KEY"); n != 1 {
		return "", fmt.Errorf("table %s declares %d INTEGER PRIMARY KEY columns; the rebuild expects one", name, n)
	}
	out := strings.Replace(ddl, "INTEGER PRIMARY KEY", "INTEGER PRIMARY KEY AUTOINCREMENT", 1)
	if !strings.Contains(out, "AUTOINCREMENT") {
		return "", fmt.Errorf("rebuild DDL for %s lost its primary key", name)
	}
	// Depth, not a live threat: sqlite_master holds one statement per row, so a
	// second statement cannot ride along, and reaching this code at all already
	// means the operator can write the database. What must not happen is a future
	// caller feeding this a string that is not the table it names.
	if !tableMatcher(name).MatchString(strings.TrimSpace(out)) {
		return "", fmt.Errorf("rebuild DDL for %s is not its own CREATE TABLE statement", name)
	}
	if strings.Contains(out, ";") {
		return "", fmt.Errorf("rebuild DDL for %s carries a statement separator", name)
	}
	return out, nil
}

func firstWords(s string, n int) string {
	fields := strings.Fields(s)
	if len(fields) < n {
		n = len(fields)
	}
	return strings.Join(fields[:n], " ")
}
