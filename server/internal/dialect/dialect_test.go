package dialect

import (
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := map[string]Engine{
		"": MySQL, "mysql": MySQL, "MySQL": MySQL, "mariadb": MySQL,
		"sqlite": SQLite, " sqlite3 ": SQLite,
		"postgres": Postgres, "pgsql": Postgres, "postgresql": Postgres, "PG": Postgres,
	}
	for raw, want := range cases {
		got, err := Normalize(raw)
		if err != nil || got != want {
			t.Errorf("Normalize(%q) = (%q, %v), want %q", raw, got, err, want)
		}
	}
	if _, err := Normalize("oracle"); err == nil {
		t.Error("Normalize(oracle): want an error")
	}
}

func TestFromDSN(t *testing.T) {
	for dsn, want := range map[string]Engine{
		"postgres://u@h/db":             Postgres,
		"postgresql://u@h/db":           Postgres,
		"sqlite:/var/lib/skin/nexus.db": SQLite,
		"file:/tmp/x.db?mode=ro":        SQLite,
	} {
		got, ok := FromDSN(dsn)
		if !ok || got != want {
			t.Errorf("FromDSN(%q) = (%q, %v)", dsn, got, ok)
		}
	}
	// The MySQL DSN form carries no scheme, so the caller must state the engine.
	if _, ok := FromDSN("skin:pass@tcp(127.0.0.1:3306)/skin_nexus"); ok {
		t.Error("mysql DSN must not be inferred")
	}
}

func TestDriverNames(t *testing.T) {
	for e, want := range map[Engine]string{MySQL: "mysql", SQLite: "sqlite", Postgres: "pgx"} {
		if got := e.Driver(); got != want {
			t.Errorf("%s.Driver() = %q, want %q", e, got, want)
		}
	}
}

// Only PostgreSQL needs numbered placeholders, and the rewrite must not touch
// anything that looks like a placeholder inside a literal, an identifier or a
// comment — that is how a rewritten query breaks in production but not in a
// unit test that only uses plain SQL.
func TestBindNumbering(t *testing.T) {
	cases := []struct{ in, want string }{
		{"SELECT 1", "SELECT 1"},
		{"WHERE a = ? AND b = ?", "WHERE a = $1 AND b = $2"},
		{"INSERT INTO t (a,b) VALUES (?,?)", "INSERT INTO t (a,b) VALUES ($1,$2)"},
		{"WHERE note = 'why?' AND a = ?", "WHERE note = 'why?' AND a = $1"},
		{"WHERE q = \"col?\" AND a = ?", "WHERE q = \"col?\" AND a = $1"},
		{"WHERE a = ? -- trailing?\nAND b = ?", "WHERE a = $1 -- trailing?\nAND b = $2"},
		{"WHERE a = ? /* block? */ AND b = ?", "WHERE a = $1 /* block? */ AND b = $2"},
		{"WHERE it = 'a\\'b?' AND c = ?", "WHERE it = 'a\\'b?' AND c = $1"},
	}
	for _, c := range cases {
		if got := Postgres.Bind(c.in); got != c.want {
			t.Errorf("postgres Bind(%q)\n = %q\nwant %q", c.in, got, c.want)
		}
	}
	for _, e := range []Engine{MySQL, SQLite} {
		if got := e.Bind("WHERE a = ? AND b = ?"); got != "WHERE a = ? AND b = ?" {
			t.Errorf("%s.Bind changed the query: %q", e, got)
		}
	}
}

func TestPlaceholders(t *testing.T) {
	if got := Placeholders(3); got != "(?, ?, ?)" {
		t.Errorf("Placeholders(3) = %q", got)
	}
	if got := Placeholders(0); got != "()" {
		t.Errorf("Placeholders(0) = %q", got)
	}
}

// The three engines spell "insert or overwrite" differently; every store that
// needs one must go through here rather than hand-writing a clause.
func TestUpsertShapes(t *testing.T) {
	cols := []string{"k", "v", "n"}
	keys := []string{"k"}
	update := []string{"v", "n"}

	got, err := MySQL.Upsert("options", cols, keys, update)
	if err != nil {
		t.Fatal(err)
	}
	if want := "INSERT INTO options (k, v, n) VALUES (?, ?, ?) ON DUPLICATE KEY UPDATE v = VALUES(v), n = VALUES(n)"; got != want {
		t.Errorf("mysql upsert\n = %q\nwant %q", got, want)
	}

	got, err = Postgres.Upsert("options", cols, keys, update)
	if err != nil {
		t.Fatal(err)
	}
	if want := "INSERT INTO options (k, v, n) VALUES (?, ?, ?) ON CONFLICT (k) DO UPDATE SET v = excluded.v, n = excluded.n"; got != want {
		t.Errorf("postgres upsert\n = %q\nwant %q", got, want)
	}
	if got := Postgres.Bind(mustUpsert(t, Postgres, cols, keys, update)); !contains(got, "$3") {
		t.Errorf("upsert must be bindable: %q", got)
	}

	if _, err := SQLite.Upsert("options", cols, keys, update); err != nil {
		t.Fatal(err)
	}
	// No columns to overwrite means "keep the row", which MySQL cannot spell
	// with this clause — the caller must use InsertIgnore there instead.
	sqliteNoop := mustUpsert(t, SQLite, cols, keys, nil)
	if !contains(sqliteNoop, "DO NOTHING") {
		t.Errorf("sqlite upsert with no update cols = %q", sqliteNoop)
	}
	mysqlNoop := mustUpsert(t, MySQL, cols, keys, nil)
	if !contains(mysqlNoop, "k = VALUES(k)") {
		t.Errorf("mysql upsert with no update cols must self-assign: %q", mysqlNoop)
	}
	if _, err := MySQL.Upsert("t", nil, keys, update); err == nil {
		t.Error("upsert with no columns should error")
	}
}

func TestInsertIgnore(t *testing.T) {
	igs, err := MySQL.InsertIgnore("user_closet", []string{"user_uid", "texture_tid"})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(igs, "INSERT IGNORE INTO user_closet (user_uid, texture_tid) VALUES (?, ?)") {
		t.Errorf("mysql insert ignore = %q", igs)
	}
	for _, e := range []Engine{SQLite, Postgres} {
		s, err := e.InsertIgnore("user_closet", []string{"user_uid", "texture_tid"})
		if err != nil {
			t.Fatal(err)
		}
		if !contains(s, "ON CONFLICT DO NOTHING") {
			t.Errorf("%s insert ignore = %q", e, s)
		}
	}
}

func TestPortablePredicates(t *testing.T) {
	if got := MySQL.CIEquals("email"); got != "LOWER(email) = LOWER(?)" {
		t.Errorf("CIEquals = %q", got)
	}
	if got := SQLite.CIContains("nickname"); got != "LOWER(nickname) LIKE LOWER(?)" {
		t.Errorf("CIContains = %q", got)
	}
	// Each engine gets its own native null-safe comparison: the long
	// `a = b OR (a IS NULL AND b IS NULL)` form is what PostgreSQL cannot type
	// when one side is a bound placeholder (SQLSTATE 42P18).
	nsCases := []struct {
		eng  Engine
		want string
	}{
		{MySQL, "(a <=> b)"},
		{SQLite, "(a IS b)"},
		{Postgres, "(NOT (a IS DISTINCT FROM b))"},
	}
	for _, c := range nsCases {
		if got := c.eng.NullSafeEq("a", "b"); got != c.want {
			t.Errorf("%s NullSafeEq = %q, want %q", c.eng, got, c.want)
		}
	}
	if got := Postgres.ConcatList("id"); got != "string_agg(id::text, ',')" {
		t.Errorf("ConcatList(pg) = %q", got)
	}
	if got := MySQL.ConcatList("id"); got != "GROUP_CONCAT(id)" {
		t.Errorf("ConcatList(mysql) = %q", got)
	}
	if got := SQLite.LockSQL(); got != "" {
		t.Errorf("SQLite must not emit FOR UPDATE: %q", got)
	}
	for _, e := range []Engine{MySQL, Postgres} {
		if got := e.LockSQL(); got != " FOR UPDATE" {
			t.Errorf("%s.LockSQL = %q", e, got)
		}
	}
}

func mustUpsert(t *testing.T, e Engine, cols, keys, update []string) string {
	t.Helper()
	s, err := e.Upsert("options", cols, keys, update)
	if err != nil {
		t.Fatalf("%s upsert: %v", e, err)
	}
	return s
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
