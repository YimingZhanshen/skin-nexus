package store

// One clock per column. These three writes let the database stamp created_at
// (column DEFAULT) while every reader, retention prune and rate-limit window
// binds m.now() — two clocks on the same value. On a split host (MySQL/MariaDB
// on its own machine, or a session with a non-UTC timezone) the row lands at
// the DB time and is then compared against the app time: reports silently
// outside the window, notifications pruned early or never, and the email-verify
// grace period either spent before it started or extended forever.
//
// The frozen clock is the independent truth here: 2026-01-02 is months away
// from the wall clock the test runs at, so a row still stamped by the column
// DEFAULT cannot match it.

import (
	"context"
	"testing"
	"time"
)

var stampBase = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func frozenClock(at time.Time) func() time.Time { return func() time.Time { return at } }

func stampedAt(t *testing.T, st *Mysql, query string, args ...any) time.Time {
	t.Helper()
	var got time.Time
	if err := st.db.QueryRowContext(context.Background(), query, args...).Scan(&got); err != nil {
		t.Fatalf("scan %s: %v", query, err)
	}
	return got.UTC().Truncate(time.Second)
}

func TestCreateReportStampsFromTheStoreClock(t *testing.T) {
	st := openSQLiteStore(t)
	st.now = frozenClock(stampBase)
	uid := newMeasureUser(t, st, "clock-report@example.com", "clockreport")

	rid, err := st.CreateReport(context.Background(), 1, uid, "spam", "detail")
	if err != nil {
		t.Fatalf("CreateReport: %v", err)
	}
	if got := stampedAt(t, st, `SELECT created_at FROM texture_reports WHERE id = ?`, rid); !got.Equal(stampBase) {
		t.Fatalf("texture_reports.created_at = %v, want the store clock %v", got, stampBase)
	}

	// The window that reads the column is the same clock, so a report filed
	// "now" is counted by a rate limit computed from "now".
	n, err := st.CountReportsByUserSince(context.Background(), uid, stampBase.Add(-time.Minute))
	if err != nil {
		t.Fatalf("CountReportsByUserSince: %v", err)
	}
	if n != 1 {
		t.Fatalf("reports inside the window = %d, want 1", n)
	}
}

func TestInsertNotificationStampsFromTheStoreClock(t *testing.T) {
	st := openSQLiteStore(t)
	st.now = frozenClock(stampBase)
	uid := newMeasureUser(t, st, "clock-notif@example.com", "clocknotif")

	if err := st.InsertNotification(context.Background(), uid, "report.handled", 0, "ref-1"); err != nil {
		t.Fatalf("InsertNotification: %v", err)
	}
	var id int64
	if err := st.db.QueryRowContext(context.Background(),
		`SELECT id FROM notifications WHERE uid = ?`, uid).Scan(&id); err != nil {
		t.Fatalf("read notification id: %v", err)
	}
	if got := stampedAt(t, st, `SELECT created_at FROM notifications WHERE id = ?`, id); !got.Equal(stampBase) {
		t.Fatalf("notifications.created_at = %v, want the store clock %v", got, stampBase)
	}
}

// The email-verify grace period is spent in the ygg service against
// users.created_at; a row stamped by the DB clock and aged by the app clock is
// the same mistake on the account that gates logins.
func TestCreateUserStampsFromTheStoreClock(t *testing.T) {
	st := openSQLiteStore(t)
	st.now = frozenClock(stampBase)

	uid, err := st.createUser(context.Background(), "clock-user@example.com", "clockuser",
		"hash", 3, 1, "")
	if err != nil {
		t.Fatalf("createUser: %v", err)
	}
	for _, col := range []string{"created_at", "updated_at"} {
		if got := stampedAt(t, st, `SELECT `+col+` FROM users WHERE id = ?`, uid); !got.Equal(stampBase) {
			t.Fatalf("users.%s = %v, want the store clock %v", col, got, stampBase)
		}
	}
}
