// Package dbx is the thin database/sql layer every store call goes through.
//
// It exists so engine support costs no churn: DB embeds *sql.DB, so a call site
// reading `m.db.ExecContext(ctx, q, args...)` keeps compiling while the query
// gets its placeholders rewritten and the connection pool gets its per-engine
// setup. Anything that cannot be rewritten (upsert shape, locks, aggregates)
// comes from the dialect package instead of being sprinkled through the stores.
package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"skin-nexus/server/internal/dialect"
)

func init() {
	// The MySQL driver's loc=UTC keeps DATETIME round-trips in UTC no matter
	// what the server session timezone is; the other engines have no such
	// setting and are handled by passing Go-side UTC values everywhere.
	_ = mysql.SetLogger(nopLogger{})
}

type nopLogger struct{}

func (nopLogger) Print(v ...any) {}

// DB is a connection pool bound to one engine.
type DB struct {
	*sql.DB
	eng dialect.Engine
}

// Open connects using the named engine. An empty engine is inferred from a
// DSN that carries a scheme, and falls back to MySQL — the pre-existing
// default, so every current deployment keeps working unchanged.
func Open(ctx context.Context, engine, dsn string) (*DB, error) {
	eng := dialect.Engine(strings.TrimSpace(engine))
	if eng == "" {
		inferred, ok := dialect.FromDSN(dsn)
		if !ok {
			eng = dialect.MySQL
		} else {
			eng = inferred
		}
	} else if !eng.Valid() {
		return nil, fmt.Errorf("dbx: %w", engineErr(engine))
	}
	if strings.TrimSpace(dsn) == "" {
		return nil, fmt.Errorf("dbx: empty DSN for engine %q", eng)
	}
	name, dsnOut, tune, err := dsnFor(eng, dsn)
	if err != nil {
		return nil, fmt.Errorf("dbx: %w", err)
	}
	db, err := sql.Open(name, dsnOut)
	if err != nil {
		return nil, fmt.Errorf("dbx: open %s: %w", eng, err)
	}
	tune(db)
	pingCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("dbx: ping %s: %w", eng, err)
	}
	return &DB{DB: db, eng: eng}, nil
}

// Engine reports which dialect this pool speaks.
func (d *DB) Engine() dialect.Engine { return d.eng }

// Underlying returns the raw pool for callers that must not go through the
// rewrite layer (the migration CLI's schema probes, sql.Tx isolation levels).
func (d *DB) Underlying() *sql.DB { return d.DB }

// Bind rewrites a `?`-style query for this engine.
func (d *DB) Bind(query string) string { return d.eng.Bind(query) }

// Dialect exposes the engine's SQL helpers to the stores.
func (d *DB) Dialect() dialect.Engine { return d.eng }

// ExecContext runs a statement after placeholder rewriting.
func (d *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return d.DB.ExecContext(ctx, d.Bind(query), args...)
}

// QueryContext runs a query after placeholder rewriting.
func (d *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return d.DB.QueryContext(ctx, d.Bind(query), args...)
}

// QueryRowContext runs a row-returning query after placeholder rewriting.
func (d *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return d.DB.QueryRowContext(ctx, d.Bind(query), args...)
}

// BeginTx starts a transaction whose statements are rewritten the same way.
func (d *DB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*Tx, error) {
	tx, err := d.DB.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &Tx{Tx: tx, db: d}, nil
}

// Tx is a transaction on a DB. It shadows sql.Tx's statement methods so no
// call site can accidentally send `?` placeholders to PostgreSQL.
type Tx struct {
	*sql.Tx
	db *DB
}

// Engine reports the parent pool's dialect.
func (t *Tx) Engine() dialect.Engine { return t.db.eng }

// Bind rewrites a `?`-style query for this engine.
func (t *Tx) Bind(query string) string { return t.db.eng.Bind(query) }

// ExecContext runs a statement after placeholder rewriting.
func (t *Tx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return t.Tx.ExecContext(ctx, t.Bind(query), args...)
}

// QueryContext runs a query after placeholder rewriting.
func (t *Tx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return t.Tx.QueryContext(ctx, t.Bind(query), args...)
}

// QueryRowContext runs a row-returning query after placeholder rewriting.
func (t *Tx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return t.Tx.QueryRowContext(ctx, t.Bind(query), args...)
}

// dsnFor maps the portable DSN onto the driver name plus driver specifics.
func dsnFor(e dialect.Engine, dsn string) (driver string, out string, tune func(*sql.DB), err error) {
	switch e {
	case dialect.MySQL:
		// Two foot-guns are closed here rather than in every store: without
		// ParseTime every time.Time scan fails at runtime while Ping still
		// succeeds, and without a forced UTC session zone the Go layer writes
		// UTC while SQL clock functions follow the server's +08 — which put
		// every session/TTL/job expiry off by eight hours once.
		cfg, err := mysql.ParseDSN(dsn)
		if err != nil {
			return "", "", nil, fmt.Errorf("mysql dsn: %w", err)
		}
		cfg.ParseTime = true
		cfg.Loc = time.UTC
		if cfg.Params == nil {
			cfg.Params = map[string]string{}
		}
		cfg.Params["time_zone"] = "'+00:00'"
		return e.Driver(), cfg.FormatDSN(), func(db *sql.DB) {
			db.SetConnMaxLifetime(30 * time.Minute)
			db.SetMaxOpenConns(24)
			db.SetMaxIdleConns(8)
		}, nil
	case dialect.Postgres:
		return e.Driver(), dsn, func(db *sql.DB) {
			db.SetConnMaxLifetime(30 * time.Minute)
			db.SetMaxOpenConns(24)
			db.SetMaxIdleConns(8)
		}, nil
	case dialect.SQLite:
		return e.Driver(), sqliteDSN(dsn), func(db *sql.DB) {
			// One connection: SQLite serialises writers anyway, and a
			// read-then-upgrade transaction cannot retry inside itself —
			// a pool would turn contention into SQLITE_BUSY errors. Sites
			// that need concurrency take MySQL or PostgreSQL.
			db.SetMaxOpenConns(1)
			db.SetMaxIdleConns(1)
		}, nil
	}
	return "", "", nil, fmt.Errorf("unsupported engine %q", e)
}

// sqliteDSN normalises `sqlite:/tmp/x.db`, `file:/tmp/x.db`, a bare path and
// `sqlite::memory:` into a driver DSN carrying the pragmas this project needs.
// WAL is asked for only on file databases — it means nothing in memory.
func sqliteDSN(dsn string) string {
	p := strings.TrimPrefix(strings.TrimPrefix(dsn, "sqlite:"), "file:")
	// Drop any query the caller already set, so re-normalising is idempotent.
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	if p == "" {
		p = "skin-nexus.db"
	}
	q := []string{"_pragma=busy_timeout(5000)", "_pragma=foreign_keys(1)", "_pragma=synchronous(NORMAL)"}
	if !isMemoryPath(p) {
		q = append(q, "_pragma=journal_mode(WAL)")
	}
	return p + "?" + strings.Join(q, "&")
}

func isMemoryPath(p string) bool {
	return strings.Contains(strings.ToLower(p), ":memory:")
}

func engineErr(raw string) error {
	return fmt.Errorf("unsupported database engine %q (want mysql, sqlite or postgres)", raw)
}
