package dbx

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"skin-nexus/server/internal/dialect"
)

func openMemSQLite(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), "sqlite", "sqlite::memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// A real engine, no external server: this is the proof that the wrapper
// actually executes rewritten SQL and returns driver-correct Go types, not
// just that it compiles.
func TestOpenSQLiteAndRoundTrip(t *testing.T) {
	db := openMemSQLite(t)
	if db.Engine() != dialect.SQLite {
		t.Fatalf("engine = %q", db.Engine())
	}
	// SQLite is opened as one connection on purpose (writers serialise anyway,
	// and a read-then-upgrade transaction cannot retry inside itself).
	if got := db.Stats().MaxOpenConnections; got != 1 {
		t.Errorf("sqlite pool size = %d, want 1", got)
	}
	ctx := context.Background()
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE opts (k TEXT PRIMARY KEY, v TEXT, flag INTEGER NOT NULL DEFAULT 0, at TIMESTAMP)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	at := time.Date(2026, 11, 2, 3, 4, 5, 600000000, time.UTC)
	res, err := db.ExecContext(ctx, `INSERT INTO opts (k, v, flag, at) VALUES (?,?,?,?)`, "a", "1", true, at)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		t.Errorf("rows affected = (%d, %v)", n, err)
	}
	var (
		v    string
		flag bool
		got  time.Time
	)
	if err := db.QueryRowContext(ctx, `SELECT v, flag, at FROM opts WHERE k = ?`, "a").Scan(&v, &flag, &got); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if v != "1" || !flag {
		t.Errorf("scanned (%q, %v)", v, flag)
	}
	// Timestamps must come back as the same instant, so the "one UTC clock in
	// Go" rule the stores rely on holds on this engine too.
	if !got.UTC().Equal(at) {
		t.Errorf("timestamp round trip = %v, want %v", got.UTC(), at)
	}
}

func TestSQLiteUpsertAndInsertIgnore(t *testing.T) {
	db := openMemSQLite(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE opts (k TEXT PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	up, err := dialect.SQLite.Upsert("opts", []string{"k", "v"}, []string{"k"}, []string{"v"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, up, "a", "one"); err != nil {
		t.Fatalf("upsert insert: %v", err)
	}
	if _, err = db.ExecContext(ctx, up, "a", "two"); err != nil {
		t.Fatalf("upsert update: %v", err)
	}
	var v string
	if err := db.QueryRowContext(ctx, `SELECT v FROM opts WHERE k = ?`, "a").Scan(&v); err != nil || v != "two" {
		t.Errorf("after upsert v = %q (%v), want two", v, err)
	}
	ig, err := dialect.SQLite.InsertIgnore("opts", []string{"k", "v"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, ig, "a", "ignored"); err != nil {
		t.Fatalf("insert ignore: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT v FROM opts WHERE k = ?`, "a").Scan(&v); err != nil || v != "two" {
		t.Errorf("insert ignore overwrote the row: %q (%v)", v, err)
	}
	var rows int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM opts`).Scan(&rows); err != nil || rows != 1 {
		t.Errorf("rows = %d (%v), want 1", rows, err)
	}
}

func TestSQLiteTransactionRollbackAndCommit(t *testing.T) {
	db := openMemSQLite(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO t (v) VALUES (?)`, "gone"); err != nil {
		t.Fatalf("tx insert: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM t`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rolled-back row survived: %d (%v)", n, err)
	}
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO t (v) VALUES (?)`, "kept"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM t`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("committed row missing: %d (%v)", n, err)
	}
}

// Case-insensitive lookups used to be spelled with a MySQL collation on the
// column; the portable predicate has to behave the same on SQLite.
func TestSQLiteCaseInsensitiveLookup(t *testing.T) {
	db := openMemSQLite(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE u (id INTEGER PRIMARY KEY, email TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO u (email) VALUES (?)`, "MixedCase@Example.COM"); err != nil {
		t.Fatal(err)
	}
	var id int64
	q := "SELECT id FROM u WHERE " + dialect.SQLite.CIEquals("email")
	if err := db.QueryRowContext(ctx, q, "mixedcase@example.com").Scan(&id); err != nil {
		t.Fatalf("CI lookup failed: %v", err)
	}
}

func TestDSNTranslation(t *testing.T) {
	// SQLite: pragmas are appended, WAL only for file databases.
	_, out, _, err := dsnFor(dialect.SQLite, "sqlite:/var/lib/skin/nexus.db")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "/var/lib/skin/nexus.db?") || !strings.Contains(out, "journal_mode(WAL)") {
		t.Errorf("sqlite file DSN = %q", out)
	}
	_, mem, _, err := dsnFor(dialect.SQLite, "sqlite::memory:")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(mem, "journal_mode") {
		t.Errorf(":memory: must not ask for WAL: %q", mem)
	}
	if !strings.Contains(mem, "busy_timeout") || !strings.Contains(mem, "foreign_keys(1)") {
		t.Errorf("sqlite DSN lost its pragmas: %q", mem)
	}
	// A caller who already put a query on the path must not end up with two.
	if _, dup, _, _ := dsnFor(dialect.SQLite, "sqlite:/tmp/x.db?_pragma=x(1)"); strings.Count(dup, "?") != 1 {
		t.Errorf("DSN query doubled: %q", dup)
	}
	// PostgreSQL takes its DSN verbatim (URL or keyword/value form).
	if name, out2, tune, err := dsnFor(dialect.Postgres, "postgres://u@[::1]:5432/skin_nexus"); err != nil ||
		name != "pgx" || out2 != "postgres://u@[::1]:5432/skin_nexus" || tune == nil {
		t.Errorf("postgres dsnFor = (%q, %q, %v)", name, out2, err)
	}
	// MySQL must come back normalised: ParseTime on, UTC location, UTC session
	// zone. These three are what made every time.Time scan and every expiry
	// comparison correct on a +08 server; the guard used to live in store.Open.
	name, out2, tune, err := dsnFor(dialect.MySQL, "skin:pw@tcp(127.0.0.1:3306)/skin_nexus")
	if err != nil || name != "mysql" || tune == nil {
		t.Fatalf("mysql dsnFor = (%q, %v)", name, err)
	}
	if !strings.Contains(out2, "parseTime=true") || !strings.Contains(out2, "time_zone") {
		t.Errorf("mysql DSN not normalised: %q", out2)
	}
	// loc=UTC is what the driver omits when it is already the default, so check
	// the parsed form rather than the text.
	back, err := mysql.ParseDSN(out2)
	if err != nil {
		t.Fatalf("normalised dsn must parse back: %v", err)
	}
	if !back.ParseTime || back.Loc != time.UTC {
		t.Errorf("normalised mysql dsn: ParseTime=%v Loc=%v, want true/UTC", back.ParseTime, back.Loc)
	}
	if back.Params["time_zone"] != "'+00:00'" {
		t.Errorf("session zone param = %q", back.Params["time_zone"])
	}
	if _, _, _, err := dsnFor(dialect.MySQL, "not-a-dsn"); err == nil {
		t.Error("an unparseable mysql dsn must fail early, not at first query")
	}
}

func TestOpenRejectsBadInput(t *testing.T) {
	ctx := context.Background()
	if _, err := Open(ctx, "oracle", "whatever"); err == nil {
		t.Error("unknown engine must fail")
	} else if !strings.Contains(err.Error(), "unsupported database engine") {
		t.Errorf("error text = %v", err)
	}
	if _, err := Open(ctx, "sqlite", "  "); err == nil {
		t.Error("empty DSN must fail")
	}
	// A file that cannot be created proves the ping is real, not decorative.
	if _, err := Open(ctx, "sqlite", "sqlite:/no/such/dir/x.db"); err == nil {
		t.Error("unreachable sqlite path must fail to open")
	}
	// Infer-the-engine path (scheme-carrying DSN, no SKIN_DB_ENGINE).
	db, err := Open(ctx, "", "sqlite::memory:")
	if err != nil {
		t.Fatalf("infer engine: %v", err)
	}
	defer db.Close()
	if db.Engine() != dialect.SQLite {
		t.Errorf("inferred engine = %q", db.Engine())
	}
}

// Bind must delegate to the engine: a store that builds one string and hands it
// to the wrapper must get `$n` on PostgreSQL without knowing why.
func TestDBBindDelegates(t *testing.T) {
	pg := &DB{eng: dialect.Postgres}
	if got := pg.Bind("SELECT ? WHERE b = ?"); got != "SELECT $1 WHERE b = $2" {
		t.Errorf("pg Bind = %q", got)
	}
	my := &DB{eng: dialect.MySQL}
	if got := my.Bind("SELECT ? WHERE b = ?"); got != "SELECT ? WHERE b = ?" {
		t.Errorf("mysql Bind must be identity, got %q", got)
	}
	if my.Dialect() != dialect.MySQL {
		t.Error("Dialect() must report the engine")
	}
}

// The migration CLI and schema probes need the untouched pool.
func TestUnderlyingIsTheSamePool(t *testing.T) {
	db := openMemSQLite(t)
	var raw *sql.DB = db.Underlying()
	if raw != db.DB {
		t.Error("Underlying() must return the embedded pool")
	}
}

// A page number comes straight from a query parameter. (page-1)*size overflows
// int64 near 2^63/size and the product wraps negative, which every engine
// rejects — an anonymous request would turn into a 500.
func TestOffsetNeverWraps(t *testing.T) {
	if got := Offset(1, 20); got != 0 {
		t.Fatalf("Offset(1) = %d", got)
	}
	if got := Offset(3, 20); got != 40 {
		t.Fatalf("Offset(3,20) = %d", got)
	}
	if got := Offset(0, 20); got != 0 {
		t.Fatalf("a page below 1 must clamp, got %d", got)
	}
	for _, page := range []int{9223372036854775807, 922337203685477580, 1 << 40} {
		if got := Offset(page, 20); got < 0 {
			t.Fatalf("Offset(%d,20) = %d, must never be negative", page, got)
		}
	}
	if got := Offset(9223372036854775807, 0); got < 0 {
		t.Fatalf("a zero page size must not produce a negative offset, got %d", got)
	}
}
