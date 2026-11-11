package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// audit_log.created_at used to come from the column DEFAULT (the DB clock)
// while every windowed read compares against the store clock. On a host whose
// clock differs from the database's, the 24h mail-failure count silently reads
// the wrong window. The write must stamp m.now().
func TestInsertAuditStampsFromTheStoreClock(t *testing.T) {
	st := openSQLiteStore(t)
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	st.now = frozenClock(base)
	st.InsertAudit(context.Background(), 7, "mail_test_failed", map[string]any{"host": "x"})
	var got time.Time
	if err := st.DB().QueryRow(`SELECT created_at FROM audit_log WHERE action = ?`, "mail_test_failed").Scan(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Equal(base) {
		t.Fatalf("created_at = %v, want the store clock %v (column DEFAULT would give the wall clock)", got, base)
	}
}

func TestCountMailFailuresSinceUsesTheSameClock(t *testing.T) {
	st := openSQLiteStore(t)
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	ctx := context.Background()
	st.now = frozenClock(base)
	for _, a := range MailFailureActions {
		st.InsertAudit(ctx, 1, a, nil)
	}
	st.InsertAudit(ctx, 1, "mail_test_sent", nil) // success must not count
	st.now = frozenClock(base.Add(-25 * time.Hour))
	st.InsertAudit(ctx, 1, "mail_test_failed", nil) // outside the window
	st.now = frozenClock(base)

	n, err := st.CountMailFailuresSince(ctx, base.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("count = %d, want 3 (three failure kinds, no success, no out-of-window row)", n)
	}
}

func TestCountMailFailuresSinceWindowBoundary(t *testing.T) {
	st := openSQLiteStore(t)
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	ctx := context.Background()
	st.now = frozenClock(base)
	st.InsertAudit(ctx, 1, "mail_test_failed", nil) // exactly at since: included
	st.now = frozenClock(base.Add(-time.Nanosecond))
	st.InsertAudit(ctx, 1, "mail_test_failed", nil) // one ns before: excluded
	st.now = frozenClock(base)
	n, err := st.CountMailFailuresSince(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("count = %d, want 1 (>= includes the boundary, > would drop it)", n)
	}
}

// The bilingual gate only sees depth-1 literals at InsertAudit call sites, so
// each name in MailFailureActions must really appear as a literal somewhere in
// the two API packages — a constant passed by name would make its locale key
// dead copy and the action invisible to the gate.
func TestMailFailureActionsAreActuallyWritten(t *testing.T) {
	var src strings.Builder
	for _, dir := range []string{"../webapi", "../adminapi"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			src.Write(b)
		}
	}
	all := src.String()
	for _, a := range MailFailureActions {
		if !strings.Contains(all, `"`+a+`"`) {
			t.Errorf("action %q is never written as an inline literal — the locale gate cannot see it", a)
		}
	}
}
