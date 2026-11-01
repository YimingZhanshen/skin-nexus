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
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
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
	if err := d.check(query, args); err != nil {
		return nil, err
	}
	return d.DB.ExecContext(ctx, d.Bind(query), d.args(args)...)
}

// QueryContext runs a query after placeholder rewriting.
func (d *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if err := d.check(query, args); err != nil {
		return nil, err
	}
	return d.DB.QueryContext(ctx, d.Bind(query), d.args(args)...)
}

// QueryRowContext runs a row-returning query after placeholder rewriting.
func (d *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if err := d.check(query, args); err != nil {
		// sql.Row carries no error of its own, so a mismatch would otherwise
		// reach the caller as ErrNoRows — indistinguishable from "no such row".
		// Report it and hand back an empty result instead of a wrong one.
		log.Printf("dbx: %v", err)
		return d.DB.QueryRowContext(ctx, `SELECT NULL WHERE 1 = 0`)
	}
	return d.DB.QueryRowContext(ctx, d.Bind(query), d.args(args)...)
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
	if err := t.db.check(query, args); err != nil {
		return nil, err
	}
	return t.Tx.ExecContext(ctx, t.Bind(query), t.db.args(args)...)
}

// QueryContext runs a query after placeholder rewriting.
func (t *Tx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if err := t.db.check(query, args); err != nil {
		return nil, err
	}
	return t.Tx.QueryContext(ctx, t.Bind(query), t.db.args(args)...)
}

// QueryRowContext runs a row-returning query after placeholder rewriting.
func (t *Tx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if err := t.db.check(query, args); err != nil {
		log.Printf("dbx: %v", err)
		return t.Tx.QueryRowContext(ctx, `SELECT NULL WHERE 1 = 0`)
	}
	return t.Tx.QueryRowContext(ctx, t.Bind(query), t.db.args(args)...)
}

// check compares a statement's placeholder count with its arguments.
//
// Queries are written with `?` on every engine, so an engine helper whose
// fragment spells the same predicate with a different number of placeholders
// changes the arity of the statement it is interpolated into. SQLite's driver
// does not reject the mismatch: it binds by index and drops the rest, which
// turns a refactor into a statement that silently matches no rows. Counting at
// the boundary makes it a failure that names the statement instead.
func (d *DB) check(query string, args []any) error {
	n := dialect.CountParams(query)
	if n == len(args) {
		return nil
	}
	return fmt.Errorf("dbx: statement has %d placeholder(s) but got %d argument(s): %s",
		n, len(args), clampLine(query))
}

func clampLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 160 {
		return s[:160] + "…"
	}
	return s
}

// args normalises arguments that SQLite stores as text.
//
// The driver writes a time.Time as "2006-01-02 15:04:05.999 -0700 MST", so on
// SQLite a deadline written from a +08 clock and one written from UTC are two
// different strings — and text comparison silently stops being time
// comparison. Forcing UTC here is what makes the DDL defaults
// (`strftime(... +0000 UTC','now')`) and every bound value order alike. MySQL
// and PostgreSQL store instants and need nothing, so they pass through.
//
// Three shapes carry an instant into this project: time.Time, *time.Time (the
// nullable expiries in the plugin KV) and sql.NullTime. A previous version
// matched only the first, and an expired key bound as *time.Time stayed
// readable on SQLite — so all three are handled, and the test pins it.
func (d *DB) args(args []any) []any {
	switch d.eng {
	case dialect.SQLite:
		out := make([]any, len(args))
		copy(out, args)
		for i, a := range out {
			switch v := a.(type) {
			case time.Time:
				out[i] = v.UTC()
			case *time.Time:
				if v != nil {
					utc := v.UTC()
					out[i] = &utc
				}
			case sql.NullTime:
				if v.Valid {
					v.Time = v.Time.UTC()
					out[i] = v
				}
			}
		}
		return out
	case dialect.Postgres:
		// Every flag in this schema is a 0/1 column, because that is what
		// MySQL's TINYINT(1) was. pgx refuses to encode a Go bool into int2, so
		// the boolean-shaped callers (the OIDC backend's done/denied/is_public)
		// land here; reads still work because database/sql converts 0/1 back to
		// a bool for a *bool destination.
		var out []any
		for i, a := range args {
			if b, ok := a.(bool); ok {
				if out == nil {
					out = make([]any, len(args))
					copy(out, args)
				}
				out[i] = 0
				if b {
					out[i] = 1
				}
			}
		}
		if out == nil {
			return args
		}
		return out
	}
	return args
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
		mem := isMemoryPath(dsn)
		return e.Driver(), sqliteDSN(dsn), func(db *sql.DB) {
			if mem {
				// Every connection to ":memory:" gets its OWN database, so a
				// pooled memory database splits in two: tables created on one
				// connection are missing on the next. One connection is the
				// only honest shape — and a memory database is a test fixture,
				// never a deployment.
				db.SetMaxOpenConns(1)
				db.SetMaxIdleConns(1)
				return
			}
			// A file database keeps a small pool. WAL lets readers run while one
			// writer holds the lock and busy_timeout makes that a wait instead
			// of an error. Pinning this to one connection would deadlock any
			// path that queries again while it is still draining a Rows — a
			// class of bug that MySQL and PostgreSQL never show.
			db.SetMaxOpenConns(4)
			db.SetMaxIdleConns(4)
			db.SetConnMaxLifetime(0) // a file database has no server to expire
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

// Wrap adopts an already-open pool. Tests use it to drive a connection with a
// deliberately hostile session (a +08 MySQL timezone, for instance), which
// Open would normalise away — the point of those tests is what the SQL looks
// like once the clock is the application's, so they need the wrapper without
// the DSN rewriting.
func Wrap(db *sql.DB, eng dialect.Engine) *DB { return &DB{DB: db, eng: eng} }

// IsDuplicateKey reports a unique/primary-key violation on any of the three
// engines. Every store turns one into a stable error code, so the mapping has
// to live in one place: a MySQL-only check would let a PostgreSQL collision
// escape as an opaque 500.
func IsDuplicateKey(err error) bool {
	if err == nil {
		return false // callers probe unconditionally
	}
	var me *mysql.MySQLError
	if errors.As(err, &me) {
		return me.Number == 1062
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code == "23505" // unique_violation
	}
	msg := err.Error()
	return strings.Contains(msg, "Error 1062") || // MariaDB over the text protocol
		strings.Contains(msg, "UNIQUE constraint failed") || // SQLite
		strings.Contains(msg, "duplicate key value violates unique constraint") // PostgreSQL
}

// returningID is the clause the non-MySQL engines need to read a generated key
// back: pgx refuses LastInsertId entirely and SQLite only answers it correctly
// when the row really used the rowid. Every generated-key table in this project
// names that column `id`.
const returningID = " RETURNING id"

// InsertReturningID runs an INSERT and hands back the generated primary key.
// MySQL reports it on the result. The insert must not already carry a RETURNING.
func (d *DB) InsertReturningID(ctx context.Context, query string, args ...any) (int64, error) {
	if d.eng == dialect.MySQL {
		res, err := d.ExecContext(ctx, query, args...)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	var id int64
	err := d.QueryRowContext(ctx, query+returningID, args...).Scan(&id)
	return id, err
}

// InsertReturningID is the transaction form of DB.InsertReturningID.
func (t *Tx) InsertReturningID(ctx context.Context, query string, args ...any) (int64, error) {
	if t.db.eng == dialect.MySQL {
		res, err := t.ExecContext(ctx, query, args...)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	// The query goes in `?`-shaped: Tx.QueryRowContext does the rewriting, and
	// pre-binding here would make it bind twice — a statement already in `$n`
	// form has no `?` left for the arity check to find.
	var id int64
	err := t.QueryRowContext(ctx, query+returningID, args...).Scan(&id)
	return id, err
}
