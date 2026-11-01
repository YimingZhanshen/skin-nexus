// Package dialect keeps the three supported database engines (MySQL/MariaDB,
// SQLite, PostgreSQL) behind one small surface. Everything engine-specific in
// this project lives here or in migrations/<engine>/ — nowhere else — so a
// shared query can be read once and be true on all three.
package dialect

import (
	"fmt"
	"strings"
)

// Engine is a supported SQL dialect.
type Engine string

const (
	MySQL    Engine = "mysql"    // covers MariaDB: same wire, same SQL subset we use
	SQLite   Engine = "sqlite"   // CGO-free driver (modernc.org/sqlite)
	Postgres Engine = "postgres" // also accepts the historical "pgsql" spelling
)

// Valid reports whether e is one of the three supported engines.
func (e Engine) Valid() bool {
	switch e {
	case MySQL, SQLite, Postgres:
		return true
	}
	return false
}

// String implements fmt.Stringer.
func (e Engine) String() string { return string(e) }

// Driver is the database/sql driver name to open this engine with.
func (e Engine) Driver() string {
	switch e {
	case SQLite:
		return "sqlite"
	case Postgres:
		return "pgx"
	default:
		return "mysql"
	}
}

// Normalize maps user spellings onto a supported engine and rejects the rest.
// MySQL is the default when nothing is set, which is what every deployment
// before multi-engine support already ran on.
func Normalize(raw string) (Engine, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "mysql", "mariadb", "tidb":
		return MySQL, nil
	case "sqlite", "sqlite3":
		return SQLite, nil
	case "postgres", "postgresql", "pgsql", "pg":
		return Postgres, nil
	}
	return "", fmt.Errorf("unsupported database engine %q (want mysql, sqlite or postgres)", raw)
}

// FromDSN infers the engine from a DSN that carries its own scheme
// ("postgres://…", "sqlite:/path/x.db"). Returns ok=false when the DSN is
// opaque (the MySQL driver's user@tcp(host)/db form) and the caller must
// state the engine.
func FromDSN(dsn string) (Engine, bool) {
	switch {
	case strings.HasPrefix(dsn, "postgres://"), strings.HasPrefix(dsn, "postgresql://"):
		return Postgres, true
	case strings.HasPrefix(dsn, "sqlite:"), strings.HasPrefix(dsn, "file:"):
		return SQLite, true
	}
	return "", false
}

// Bind rewrites positional placeholders into the form this engine's driver
// wants. Queries in this codebase are written with `?` everywhere; only
// PostgreSQL needs the `$n` form, so MySQL and SQLite get the string back
// untouched.
//
// Literals, quoted identifiers and comments are skipped: a `?` inside
// `'why?'` is data, and rewriting it breaks the statement.
func (e Engine) Bind(query string) string {
	if e != Postgres {
		return query
	}
	var b strings.Builder
	b.Grow(len(query) + 8)
	n := 0
	for i := 0; i < len(query); {
		at, kind, past := nextSpecial(query, i)
		b.WriteString(query[i:at])
		switch kind {
		case '?':
			n++
			fmt.Fprintf(&b, "$%d", n)
		case 0:
			// exhausted
		default:
			b.WriteString(query[at:past])
		}
		i = past
	}
	return b.String()
}

// CountParams reports how many placeholders a statement carries, using the same
// scan Bind uses. The store layer checks it against its argument list before
// the driver sees the statement: SQLite's driver accepts extra arguments and
// ignores them, so an off-by-one binding silently reads the wrong column
// instead of failing.
//
// Both spellings count, because a statement may legitimately arrive already
// numbered — `?` when it is written the project way, `$n` after Bind.
func CountParams(query string) int {
	q := 0
	dollar := 0
	for i := 0; i < len(query); {
		at, kind, past := nextSpecial(query, i)
		switch kind {
		case '?':
			q++
		case '$':
			n := 0
			j := at + 1
			for j < len(query) && query[j] >= '0' && query[j] <= '9' {
				n = n*10 + int(query[j]-'0')
				j++
			}
			if j > at+1 {
				if n > dollar {
					dollar = n
				}
				i = j
				continue
			}
		case 0:
		}
		i = past
	}
	if q > 0 {
		return q
	}
	return dollar
}

// nextSpecial finds the next structurally interesting position from i and
// returns it, what kind of construct starts there (0 when the input runs out)
// and the index just past that construct. A `?` inside `'why?'`, a backticked
// identifier or a comment is data, not a placeholder.
func nextSpecial(s string, i int) (at int, kind byte, past int) {
	for ; i < len(s); i++ {
		switch s[i] {
		case '\'', '"', '`':
			return i, s[i], skipQuoted(s, i, s[i])
		case '-':
			if strings.HasPrefix(s[i:], "--") {
				e := strings.IndexByte(s[i:], '\n')
				if e < 0 {
					e = len(s) - i
				}
				return i, '-', i + e
			}
		case '/':
			if strings.HasPrefix(s[i:], "/*") {
				e := strings.Index(s[i+2:], "*/")
				if e < 0 {
					e = len(s)
				} else {
					e += 4 // through the closing */
				}
				return i, '/', i + e
			}
		case '?':
			return i, '?', i + 1
		case '$':
			return i, '$', i + 1
		}
	}
	return len(s), 0, len(s)
}

// skipQuoted returns the index just past the quoted run starting at i.
func skipQuoted(s string, i int, q byte) int {
	j := i + 1
	for j < len(s) {
		if s[j] == '\\' && q != '`' { // MySQL backslash escapes inside '…'
			j += 2
			continue
		}
		if s[j] == q {
			if j+1 < len(s) && s[j+1] == q { // doubled quote is an escaped quote
				j += 2
				continue
			}
			return j + 1
		}
		j++
	}
	return len(s)
}

// Placeholders returns "(?, ?, ?)" for n columns — the tail of an INSERT that
// Bind() will number per engine.
func Placeholders(n int) string {
	if n <= 0 {
		return "()"
	}
	return "(" + strings.TrimSuffix(strings.Repeat("?, ", n), ", ") + ")"
}

// CIEquals is the case-insensitive equality predicate. Every engine gets the
// same portable text: MySQL/SQLite compare with their CI collations by
// default, and LOWER() keeps the intent explicit (and index-expressible) on
// all three. `col` is a column expression, never user input.
func (e Engine) CIEquals(col string) string {
	return fmt.Sprintf("LOWER(%s) = LOWER(?)", col)
}

// CIContains is the case-insensitive substring test for LIKE filters.
func (e Engine) CIContains(col string) string {
	return fmt.Sprintf("LOWER(%s) LIKE LOWER(?)", col)
}

// NullSafeEq compares two expressions treating NULL = NULL as true.
//
// Spelled per engine on purpose. The "portable" long form
// `(a = b OR (a IS NULL AND b IS NULL))` is exactly what PostgreSQL cannot
// type-check when one side is an untyped placeholder: it answers
// "could not determine data type of parameter $n" (SQLSTATE 42P18) and the
// query never runs. Each engine has a native null-safe comparison instead —
// `<=>` on MySQL, `IS` on SQLite, and the negation of `IS DISTINCT FROM` on
// PostgreSQL, whose construct resolves the placeholder from the typed side.
func (e Engine) NullSafeEq(left, right string) string {
	switch e {
	case SQLite:
		return fmt.Sprintf("(%s IS %s)", left, right)
	case Postgres:
		return fmt.Sprintf("(NOT (%s IS DISTINCT FROM %s))", left, right)
	case MySQL:
		return fmt.Sprintf("(%s <=> %s)", left, right)
	}
	return fmt.Sprintf("(%s = %s)", left, right)
}

// Upsert builds an INSERT that resolves a key collision by overwriting.
// cols are the insert columns, keys the conflicting ones, update the columns
// to overwrite (empty = keep the existing row).
func (e Engine) Upsert(table string, cols, keys, update []string) (string, error) {
	if len(cols) == 0 || len(keys) == 0 {
		return "", fmt.Errorf("dialect: upsert needs at least one column and one key")
	}
	placeholders := Placeholders(len(cols))
	switch e {
	case MySQL:
		sets := make([]string, 0, len(update))
		for _, c := range update {
			sets = append(sets, c+" = VALUES("+c+")")
		}
		if len(sets) == 0 {
			// MySQL has no DO NOTHING on this clause; assigning the key to
			// itself is the idiomatic no-op.
			sets = append(sets, keys[0]+" = VALUES("+keys[0]+")")
		}
		return fmt.Sprintf("INSERT INTO %s (%s) VALUES %s ON DUPLICATE KEY UPDATE %s",
			table, strings.Join(cols, ", "), placeholders, strings.Join(sets, ", ")), nil
	case SQLite, Postgres:
		// Both speak ON CONFLICT. SQLite cannot reference VALUES(), so the
		// excluded row alias is required.
		var b strings.Builder
		fmt.Fprintf(&b, "INSERT INTO %s (%s) VALUES %s ON CONFLICT (%s)",
			table, strings.Join(cols, ", "), placeholders, strings.Join(keys, ", "))
		if len(update) == 0 {
			b.WriteString(" DO NOTHING")
			return b.String(), nil
		}
		sets := make([]string, 0, len(update))
		for _, c := range update {
			sets = append(sets, fmt.Sprintf("%s = excluded.%s", c, c))
		}
		b.WriteString(" DO UPDATE SET ")
		b.WriteString(strings.Join(sets, ", "))
		return b.String(), nil
	}
	return "", fmt.Errorf("dialect: unsupported engine %q", e)
}

// InsertIgnore builds an INSERT that silently skips rows whose primary or
// unique key already exists — the idempotent-import primitive.
//
// The conflict target is deliberately left off: MySQL's IGNORE has no target to
// name, and SQLite/PostgreSQL accept a target-less ON CONFLICT DO NOTHING,
// which is the only spelling all three share. (Naming the columns instead would
// have to match a real index exactly — and blessing's user_closet has none.)
func (e Engine) InsertIgnore(table string, cols []string) (string, error) {
	if len(cols) == 0 {
		return "", fmt.Errorf("dialect: insert-ignore needs at least one column")
	}
	if e == MySQL {
		return fmt.Sprintf("INSERT IGNORE INTO %s (%s) VALUES %s",
			table, strings.Join(cols, ", "), Placeholders(len(cols))), nil
	}
	return fmt.Sprintf("INSERT INTO %s (%s) VALUES %s ON CONFLICT DO NOTHING",
		table, strings.Join(cols, ", "), Placeholders(len(cols))), nil
}

// ConcatList aggregates rows into one comma-joined string. MySQL and SQLite
// share GROUP_CONCAT; PostgreSQL renamed it.
func (e Engine) ConcatList(expr string) string {
	if e == Postgres {
		return fmt.Sprintf("string_agg(%s::text, ',')", expr)
	}
	return fmt.Sprintf("GROUP_CONCAT(%s)", expr)
}

// Concat joins scalar expressions into one string. MySQL spells it CONCAT(),
// the other two only guarantee the || operator (PG's || is strict-NULL like
// CONCAT, SQLite returns NULL for `x || NULL` too), so the operator form is
// the portable one and the argument list is joined with the separator MySQL
// would need anyway.
func (e Engine) Concat(parts ...string) string {
	if len(parts) == 0 {
		return "''"
	}
	if e == MySQL {
		return "CONCAT(" + strings.Join(parts, ", ") + ")"
	}
	return strings.Join(parts, " || ")
}

// VersionQuery reports the engine's own version string. The admin status page
// shows it, so "which database is this site on" is answerable per engine
// instead of being a MySQL-only function call.
func (e Engine) VersionQuery() string {
	switch e {
	case SQLite:
		return `SELECT sqlite_version()`
	case Postgres:
		return `SELECT version()`
	}
	return `SELECT VERSION()`
}

// SessionLock is the cross-connection advisory lock, keyed by name: the
// primitive for work that straddles the database (a texture file written
// before its row exists, deleted once no row refers to it). Both statements
// answer one row holding 1 when the lock is (or was) held, 0 when it is
// somebody else's, and NULL when the engine could not answer at all.
//
// SQLite answers supported=false: it has no such lock. Its writers already
// serialise on the file, so the guard is unnecessary inside one process — but a
// second process (the CLI) against a live server's file loses the
// serialisation, which is the same single-process rule SQLite databases carry
// everywhere.
func (e Engine) SessionLock(timeoutSeconds int) (acquire, release string, supported bool) {
	switch e {
	case MySQL:
		return fmt.Sprintf("SELECT GET_LOCK(?, %d)", timeoutSeconds),
			"SELECT RELEASE_LOCK(?)", true
	case Postgres:
		// hashtext maps the name to the bigint key the lock manager takes;
		// try-first keeps the caller's timeout semantics instead of blocking.
		return "SELECT CASE WHEN pg_try_advisory_lock(hashtext(?)) THEN 1 ELSE 0 END",
			"SELECT CASE WHEN pg_advisory_unlock(hashtext(?)) THEN 1 ELSE 0 END", true
	}
	return "", "", false
}

// TableExistsQuery asks whether one base table exists in the schema the
// connection is sitting on. Each engine keeps its catalogue somewhere else, and
// the MySQL spelling drags in two things the others do not have — `DATABASE()`
// and information_schema itself is absent on SQLite — so this cannot be one
// statement for all three. The table name is bound, not interpolated, so the
// statement keeps one placeholder on every engine.
func (e Engine) TableExistsQuery() string {
	switch e {
	case SQLite:
		return `SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ? LIMIT 1`
	case Postgres:
		return `SELECT 1 FROM information_schema.tables WHERE table_schema = current_schema AND table_name = ? LIMIT 1`
	}
	return `SELECT 1 FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ? LIMIT 1`
}

// LockSQL is the pessimistic row lock. SQLite is single-writer by design
// (writers serialise on the write lock), so it gets no clause rather than a
// statement that would not parse.
func (e Engine) LockSQL() string {
	if e == SQLite {
		return ""
	}
	return " FOR UPDATE"
}
