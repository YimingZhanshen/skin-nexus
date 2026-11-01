package dbx

import (
	"context"
	"database/sql"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"skin-nexus/server/internal/dialect"
)

// TestSQLiteRewritesTimeArgsToUTC covers the one place where SQLite's text
// storage can turn a correct query into a wrong answer: a deadline written
// from a +08 clock and one written from UTC are different strings, and SQLite
// compares those strings, not instants.
func TestSQLiteRewritesTimeArgsToUTC(t *testing.T) {
	// A file database, because that is what a site actually runs: a memory
	// database is one private database per pooled connection.
	dsn := filepath.Join(t.TempDir(), "utc.db")
	db, err := Open(context.Background(), string(dialect.SQLite), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	const ddl = `CREATE TABLE t (id INTEGER PRIMARY KEY, at DATETIME NOT NULL)`
	if _, err := db.ExecContext(ctx, ddl); err != nil {
		t.Fatal(err)
	}
	east := time.FixedZone("CST", 8*3600)
	noon := time.Date(2026, 11, 1, 20, 0, 0, 0, east) // 12:00 UTC
	if _, err := db.ExecContext(ctx, `INSERT INTO t (at) VALUES (?)`, noon); err != nil {
		t.Fatal(err)
	}
	// CAST keeps the driver from parsing the column, so this is the stored text.
	var stored string
	if err := db.QueryRowContext(ctx, `SELECT CAST(at AS TEXT) FROM t`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(stored, "+0000 UTC") {
		t.Errorf("non-UTC argument stored as %q, want a UTC rendering", stored)
	}

	// The instant must compare against UTC bounds on both sides — every TTL
	// check in the stores is this shape.
	for _, c := range []struct {
		op    string
		bound time.Time
		want  int
	}{
		{">", time.Date(2026, 11, 1, 11, 0, 0, 0, time.UTC), 1},
		{"<", time.Date(2026, 11, 1, 13, 0, 0, 0, time.UTC), 1},
		{"=", time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), 1},
		{">", time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), 0},
	} {
		var n int
		q := `SELECT COUNT(*) FROM t WHERE at ` + c.op + ` ?`
		if err := db.QueryRowContext(ctx, q, c.bound).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		if n != c.want {
			t.Errorf("at %s %v => %d rows, want %d", c.op, c.bound, n, c.want)
		}
	}

	var got time.Time
	if err := db.QueryRowContext(ctx, `SELECT at FROM t`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if !got.UTC().Equal(noon.UTC()) {
		t.Errorf("scanned %v, want %v", got, noon)
	}

	// A column default written by SQLite itself must land in the same shape,
	// or DB-defaulted rows sort differently from Go-written ones.
	defDDL := `CREATE TABLE d (id INTEGER PRIMARY KEY, created DATETIME NOT NULL DEFAULT ` +
		`(strftime('%Y-%m-%d %H:%M:%f +0000 UTC','now')))`
	if _, err := db.ExecContext(ctx, defDDL); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO d DEFAULT VALUES`); err != nil {
		t.Fatal(err)
	}
	var rawDef string
	if err := db.QueryRowContext(ctx, `SELECT CAST(created AS TEXT) FROM d`).Scan(&rawDef); err != nil {
		t.Fatal(err)
	}
	// One shape for both writers: SQLite's own default and the value the
	// driver stores for a Go time have to sort against each other.
	shape := regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}(\.\d+)? \+0000 UTC$`)
	for label, v := range map[string]string{"go-written": stored, "db-default": rawDef} {
		if !shape.MatchString(v) {
			t.Errorf("%s value stored as %q, want e.g. %q", label, v, "2026-11-01 12:00:00.000 +0000 UTC")
		}
	}
	var def time.Time
	if err := db.QueryRowContext(ctx, `SELECT created FROM d`).Scan(&def); err != nil {
		t.Fatalf("db-defaulted row did not scan as time.Time: %v", err)
	}
	var ahead int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM d WHERE created > ?`, time.Now().UTC().Add(time.Hour)).Scan(&ahead); err != nil {
		t.Fatal(err)
	}
	if ahead != 0 {
		t.Error("DB-defaulted row compared as more than an hour in the future")
	}

	// Transactions normalise identically — the stores write through Tx.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO t (at) VALUES (?)`, noon.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	var txStored string
	if err := tx.QueryRowContext(ctx, `SELECT CAST(at AS TEXT) FROM t WHERE id = 2`).Scan(&txStored); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(txStored, "+0000 UTC") {
		t.Errorf("tx write stored %q, want a UTC rendering", txStored)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// TestTimeArgsPassThroughOnInstantEngines pins the other half: MySQL and
// PostgreSQL store instants, so rewriting their arguments would only add
// allocations and could fight the driver's own conversion.
func TestTimeArgsPassThroughOnInstantEngines(t *testing.T) {
	args := []any{time.Now(), 1, "x"}
	for _, eng := range []dialect.Engine{dialect.MySQL, dialect.Postgres} {
		d := &DB{eng: eng}
		if got := d.args(args); &got[0] != &args[0] {
			t.Errorf("%s: args copied; only SQLite needs normalisation", eng)
		}
	}
	d := &DB{eng: dialect.SQLite}
	east := time.FixedZone("CST", 8*3600)
	in := []any{time.Date(2026, 11, 1, 20, 0, 0, 0, east)}
	out := d.args(in)
	if got, ok := out[0].(time.Time); !ok || got.Location() != time.UTC {
		t.Errorf("sqlite arg not normalised to UTC: %#v", out[0])
	}
	if in[0].(time.Time).Location() != east {
		t.Error("normalisation mutated the caller's slice")
	}
}

// TestSQLiteNormalisesPointerAndNullTimeArgs covers the shapes an instant can
// arrive in. The plugin KV passes *time.Time for an expiry, and a normaliser
// that only matched time.Time let an expired key stay readable on SQLite while
// MySQL rejected the same row — the store tests caught it, this test pins it.
func TestSQLiteNormalisesPointerAndNullTimeArgs(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "ptr.db")
	db, err := Open(context.Background(), string(dialect.SQLite), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE k (id INTEGER PRIMARY KEY, exp DATETIME NULL)`); err != nil {
		t.Fatal(err)
	}

	east := time.FixedZone("CST", 8*3600)
	past := time.Date(2026, 11, 1, 5, 0, 0, 0, east) // 21:00 UTC the day before
	if _, err := db.ExecContext(ctx, `INSERT INTO k (exp) VALUES (?)`, &past); err != nil {
		t.Fatalf("pointer argument: %v", err)
	}
	var raw string
	if err := db.QueryRowContext(ctx, `SELECT CAST(exp AS TEXT) FROM k WHERE id = 1`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(raw, "+0000 UTC") {
		t.Errorf("*time.Time stored as %q, want a UTC rendering", raw)
	}
	now := time.Date(2026, 11, 1, 22, 0, 0, 0, time.UTC)
	var live int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM k WHERE exp > ?`, now).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 0 {
		t.Errorf("a past deadline bound as *time.Time compared as future (%d rows live)", live)
	}

	// A +08 instant that is genuinely in the future must compare as future once
	// it is stored in UTC — the shape alone is not the point, the ordering is.
	pastEast := time.Date(2026, 11, 1, 23, 0, 0, 0, east)  // 15:00 UTC, before `now`
	futureEast := time.Date(2026, 11, 2, 7, 0, 0, 0, east) // 23:00 UTC, after `now`
	if _, err := db.ExecContext(ctx, `INSERT INTO k (exp) VALUES (?)`, sql.NullTime{Time: pastEast, Valid: true}); err != nil {
		t.Fatalf("NullTime argument: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO k (exp) VALUES (?)`, sql.NullTime{Time: futureEast, Valid: true}); err != nil {
		t.Fatalf("NullTime argument: %v", err)
	}
	for _, id := range []int{2, 3} {
		if err := db.QueryRowContext(ctx, `SELECT CAST(exp AS TEXT) FROM k WHERE id = ?`, id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(raw, "+0000 UTC") {
			t.Errorf("NullTime row %d stored as %q, want a UTC rendering", id, raw)
		}
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM k WHERE exp > ?`, now).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 1 {
		t.Errorf("live NullTime rows = %d, want only the genuinely future one", live)
	}

	// NULL stays NULL: normalising must not invent a timestamp.
	if _, err := db.ExecContext(ctx, `INSERT INTO k (exp) VALUES (?)`, (*time.Time)(nil)); err != nil {
		t.Fatal(err)
	}
	var nullCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM k WHERE exp IS NULL`).Scan(&nullCount); err != nil {
		t.Fatal(err)
	}
	if nullCount != 1 {
		t.Errorf("nil pointer rows = %d, want 1", nullCount)
	}
}

// TestPostgresMapsBoolFlagsToSmallInt covers the other engine's argument
// mismatch: every flag column in this schema is 0/1 (MySQL TINYINT(1) heritage)
// and pgx refuses to encode a Go bool into int2 — the OIDC backend writes
// done/denied/is_public as booleans and failed on the first round against
// PostgreSQL.
func TestPostgresMapsBoolFlagsToSmallInt(t *testing.T) {
	d := &DB{eng: dialect.Postgres}
	in := []any{true, false, "argon2id", 7}
	out := d.args(in)
	if out[0] != 1 || out[1] != 0 {
		t.Errorf("bools mapped to %#v/%#v, want 1 and 0", out[0], out[1])
	}
	if out[2] != "argon2id" || out[3] != 7 {
		t.Errorf("non-bool arguments changed: %#v %#v", out[2], out[3])
	}
	if in[0] != true {
		t.Error("normalisation mutated the caller's slice")
	}
	// Nothing to convert means no allocation and the original slice.
	if got := d.args([]any{"a", 1}); len(got) != 2 || got[0] != "a" {
		t.Errorf("passthrough broken: %#v", got)
	}
	// MySQL and SQLite take booleans from the driver as they please.
	for _, eng := range []dialect.Engine{dialect.MySQL, dialect.SQLite} {
		d := &DB{eng: eng}
		if got := d.args([]any{true}); got[0] != true {
			t.Errorf("%s rewrote a bool the driver can handle itself: %#v", eng, got[0])
		}
	}
}

// TestFilePoolKeepsReadersRunning documents the deadlock that a pinned pool
// would cause: rows still open while another query is issued.
func TestFilePoolKeepsReadersRunning(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "pool.db")
	db, err := Open(context.Background(), string(dialect.SQLite), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE p (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := db.ExecContext(ctx, `INSERT INTO p (v) VALUES (?)`, "v"); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := db.QueryContext(ctx, `SELECT id, v FROM p ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	// While the cursor above is still open, ask the same pool another question.
	// On a single-connection pool this blocks forever.
	var n int
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM p`).Scan(&n); err != nil {
			t.Errorf("nested query while a cursor is open: %v", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("deadlock: the SQLite pool cannot answer a second query while a cursor is open")
	}
	if n != 5 {
		t.Errorf("count = %d, want 5", n)
	}
}

// TestArityGuardCatchesMisbind is the boundary check that would have caught the
// double-bind bug behind the PostgreSQL failures: a statement whose
// placeholders and arguments disagree must fail loudly instead of being bound
// by index with the extras dropped.
func TestArityGuardCatchesMisbind(t *testing.T) {
	for _, eng := range []dialect.Engine{dialect.MySQL, dialect.SQLite, dialect.Postgres} {
		d := &DB{eng: eng}
		if err := d.check(`UPDATE t SET a = ? WHERE id = ?`, []any{1, 2}); err != nil {
			t.Errorf("%s: correct arity rejected: %v", eng, err)
		}
		err := d.check(`UPDATE t SET a = ? WHERE id = ?`, []any{1, 2, 3})
		if err == nil || !strings.Contains(err.Error(), "2 placeholder(s) but got 3") {
			t.Errorf("%s: extra argument accepted, got %v", eng, err)
		}
		// Already-numbered statements are the norm on PostgreSQL after Bind.
		if err := d.check(`UPDATE t SET a = $1 WHERE id = $2`, []any{1, 2}); err != nil {
			t.Errorf("%s: numbered statement rejected: %v", eng, err)
		}
		// A `?` inside a literal is data.
		if err := d.check(`SELECT 'why?' , ?`, []any{1}); err != nil {
			t.Errorf("%s: literal question mark counted: %v", eng, err)
		}
	}
}
