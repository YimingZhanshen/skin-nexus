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
		c := query[i]
		switch c {
		case '\'', '"', '`':
			end := skipQuoted(query, i, c)
			b.WriteString(query[i:end])
			i = end
		case '-':
			if strings.HasPrefix(query[i:], "--") {
				end := strings.IndexByte(query[i:], '\n')
				if end < 0 {
					end = len(query) - i
				}
				b.WriteString(query[i : i+end])
				i += end
				continue
			}
			b.WriteByte(c)
			i++
		case '/':
			if strings.HasPrefix(query[i:], "/*") {
				end := strings.Index(query[i+2:], "*/")
				if end < 0 {
					end = len(query)
				} else {
					end += 4 // through the closing */
				}
				b.WriteString(query[i : i+end])
				i += end
				continue
			}
			b.WriteByte(c)
			i++
		case '?':
			n++
			fmt.Fprintf(&b, "$%d", n)
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
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

// NullSafeEq compares two expressions treating NULL = NULL as true. Written
// out rather than per-engine (`<=>` on MySQL, `IS` on SQLite,
// `IS NOT DISTINCT FROM` on PG) because the long form is legal everywhere.
func NullSafeEq(left, right string) string {
	return fmt.Sprintf("(%s = %s OR (%s IS NULL AND %s IS NULL))", left, right, left, right)
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

// LockSQL is the pessimistic row lock. SQLite is single-writer by design
// (writers serialise on the write lock), so it gets no clause rather than a
// statement that would not parse.
func (e Engine) LockSQL() string {
	if e == SQLite {
		return ""
	}
	return " FOR UPDATE"
}
