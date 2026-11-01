package dialect

import "testing"

// TestCountParamsShareScanWithBind pins the arity contract the dbx boundary
// checks against: a placeholder is anything outside a literal, a quoted
// identifier or a comment — and every engine spelling of a helper fragment must
// produce the same number of them.
func TestCountParamsShareScanWithBind(t *testing.T) {
	cases := []struct {
		q        string
		want     int
		bindWant string
	}{
		{`SELECT a FROM t WHERE b = ? AND c = ?`, 2,
			`SELECT a FROM t WHERE b = $1 AND c = $2`},
		{`SELECT '?' , ?`, 1, `SELECT '?' , $1`},
		{"SELECT ? -- why?\n", 1, "SELECT $1 -- why?\n"},
		{"SELECT /* ? */ ?", 1, "SELECT /* ? */ $1"},
		{"INSERT INTO t (a) VALUES (?) ON CONFLICT DO NOTHING", 1,
			"INSERT INTO t (a) VALUES ($1) ON CONFLICT DO NOTHING"},
		{`UPDATE t SET a = (NOT (a IS DISTINCT FROM ?)) WHERE id = ?`, 2,
			`UPDATE t SET a = (NOT (a IS DISTINCT FROM $1)) WHERE id = $2`},
	}
	for _, c := range cases {
		if got := CountParams(c.q); got != c.want {
			t.Errorf("CountParams(%q) = %d, want %d", c.q, got, c.want)
		}
		if got := Postgres.Bind(c.q); got != c.bindWant {
			t.Errorf("Bind(%q) = %q, want %q", c.q, got, c.bindWant)
		}
	}
}

// TestHelperFragmentsKeepArity guards the rule that makes the dbx argument
// check meaningful: switching engines must never change how many placeholders a
// statement carries.
func TestHelperFragmentsKeepArity(t *testing.T) {
	fragments := []struct {
		name  string
		build func(Engine) string
		want  int
	}{
		{"NullSafeEq", func(e Engine) string { return e.NullSafeEq("email", "?") }, 1},
		{"CIEquals", func(e Engine) string { return e.CIEquals("email") }, 1},
		{"CIContains", func(e Engine) string { return e.CIContains("name") }, 1},
	}
	for _, f := range fragments {
		for _, eng := range []Engine{MySQL, SQLite, Postgres} {
			text := f.build(eng)
			if n := CountParams(text); n != f.want {
				t.Errorf("%s on %s carries %d placeholder(s), want %d: %q", f.name, eng, n, f.want, text)
			}
		}
	}
	// The advisory lock is not a fragment, but the same rule applies to it: the
	// caller binds one name and both statements must ask for exactly that.
	for _, eng := range []Engine{MySQL, Postgres} {
		acq, rel, ok := eng.SessionLock(5)
		if !ok {
			t.Fatalf("%s must support the advisory lock", eng)
		}
		for label, q := range map[string]string{"acquire": acq, "release": rel} {
			if n := CountParams(q); n != 1 {
				t.Errorf("%s %s carries %d placeholder(s), want 1: %q", eng, label, n, q)
			}
		}
	}
	if _, _, ok := SQLite.SessionLock(5); ok {
		t.Error("SQLite must not claim an advisory lock")
	}
}
