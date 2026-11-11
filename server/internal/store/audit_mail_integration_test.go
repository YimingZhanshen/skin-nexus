//go:build integration

package store

import (
	"context"
	"testing"
	"time"
)

// The same clock/window assertions on every engine: DATETIME(3) truncation,
// TIMESTAMPTZ zones and SQLite text times must all agree with the Go clock.
func TestAuditMailClockIntegration(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	m.now = frozenClock(base)
	for _, a := range MailFailureActions {
		m.InsertAudit(ctx, 1, a, nil)
	}
	m.InsertAudit(ctx, 1, "mail_test_sent", nil)
	m.now = frozenClock(base.Add(-25 * time.Hour))
	m.InsertAudit(ctx, 1, "mail_test_failed", nil)
	m.now = frozenClock(base)

	var got time.Time
	// m.db (the dbx wrapper) rebinds ? to $n on Postgres; raw m.DB() would not.
	if err := m.db.QueryRowContext(ctx, `SELECT created_at FROM audit_log WHERE action = ? AND actor_uid = 1`, "mail_test_sent").Scan(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Equal(base) {
		t.Fatalf("created_at = %v, want store clock %v", got, base)
	}
	n, err := m.CountMailFailuresSince(ctx, base.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("count = %d, want 3", n)
	}
}
