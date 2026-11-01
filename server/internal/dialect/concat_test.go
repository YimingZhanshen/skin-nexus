package dialect

import (
	"strings"
	"testing"
)

func TestConcat(t *testing.T) {
	cases := []struct {
		eng  Engine
		want string
	}{
		{MySQL, "CONCAT(type, ':', hash)"},
		{SQLite, "type || ':' || hash"},
		{Postgres, "type || ':' || hash"},
	}
	for _, c := range cases {
		if got := c.eng.Concat("type", "':'", "hash"); got != c.want {
			t.Errorf("%s Concat = %q, want %q", c.eng, got, c.want)
		}
	}
	if got := MySQL.Concat(); got != "''" {
		t.Errorf("empty Concat = %q, want the empty string literal", got)
	}
}

// TestSessionLock pins the three spellings of the cross-connection lock and,
// for SQLite, that the guard is reported as unavailable rather than silently
// succeeding — a caller that assumed it held a lock would delete a file that
// another operation is still writing.
func TestSessionLock(t *testing.T) {
	acq, rel, ok := MySQL.SessionLock(5)
	if !ok || acq != "SELECT GET_LOCK(?, 5)" || rel != "SELECT RELEASE_LOCK(?)" {
		t.Errorf("mysql session lock = %q / %q (%v)", acq, rel, ok)
	}
	acq, rel, ok = Postgres.SessionLock(5)
	if !ok || !strings.Contains(acq, "pg_try_advisory_lock(hashtext(?))") ||
		!strings.Contains(rel, "pg_advisory_unlock(hashtext(?))") {
		t.Errorf("postgres session lock = %q / %q (%v)", acq, rel, ok)
	}
	// MySQL answers 1/0/NULL and PostgreSQL a boolean wrapped into 1/0, so the
	// caller's `sql.NullInt64` scan stays one shape on both.
	acq, rel, ok = SQLite.SessionLock(5)
	if ok || acq != "" || rel != "" {
		t.Errorf("sqlite must report no advisory lock, got %q / %q (%v)", acq, rel, ok)
	}
}
