//go:build integration

// notifications_integration_test.go: the inbox is partitioned by uid and the
// read transitions must not leak across accounts. See integration_test.go for
// the harness.
package store

import (
	"context"
	"testing"
)

func TestIntegrationNotificationInboxAndUnread(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	uid := seedUser(t, m)
	other := seedUser(t, m)

	for _, n := range []struct {
		kind string
		tid  int64
		ref  string
	}{
		{NotifReportResolved, 7, "round cat"},
		{NotifTextureHidden, 8, "bad cape"},
		{NotifAccountBanned, 0, ""},
	} {
		if err := m.InsertNotification(ctx, uid, n.kind, n.tid, n.ref); err != nil {
			t.Fatalf("insert %s: %v", n.kind, err)
		}
	}
	if err := m.InsertNotification(ctx, other, NotifAccountUnbanned, 0, ""); err != nil {
		t.Fatalf("insert for other: %v", err)
	}

	rows, total, err := m.ListNotifications(ctx, uid, false, 1, 20)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 3 || len(rows) != 3 {
		t.Fatalf("inbox = %d rows / total %d, want 3/3", len(rows), total)
	}
	// Newest first, and the snapshot survived the round trip.
	if rows[0].Kind != NotifAccountBanned {
		t.Errorf("newest-first: rows[0].kind = %s, want %s", rows[0].Kind, NotifAccountBanned)
	}
	if rows[1].Kind != NotifTextureHidden || rows[1].TextureID != 8 || rows[1].Ref != "bad cape" {
		t.Errorf("texture row = %+v, want hidden/8/bad cape", rows[1])
	}
	for _, r := range rows {
		if r.Read {
			t.Errorf("row %d should start unread", r.ID)
		}
	}

	if n, err := m.UnreadNotificationCount(ctx, uid); err != nil || n != 3 {
		t.Fatalf("unread = (%d, %v), want (3, nil)", n, err)
	}

	if err := m.MarkNotificationRead(ctx, uid, rows[0].ID); err != nil {
		t.Fatalf("mark read: %v", err)
	}
	if n, err := m.UnreadNotificationCount(ctx, uid); err != nil || n != 2 {
		t.Fatalf("unread after one read = (%d, %v), want (2, nil)", n, err)
	}
	// onlyUnread narrows the list to what the badge counts.
	unread, uTotal, err := m.ListNotifications(ctx, uid, true, 1, 20)
	if err != nil || uTotal != 2 || len(unread) != 2 {
		t.Fatalf("unread filter = (%d rows, total %d, %v), want 2/2/nil", len(unread), uTotal, err)
	}
	for _, r := range unread {
		if r.Read {
			t.Errorf("unread filter returned a read row %d", r.ID)
		}
	}
}

// A guessed id from another inbox must not mark anything: the write is scoped by
// uid, so it matches zero rows rather than somebody else's.
func TestIntegrationNotificationReadIsScopedToOwner(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	uid := seedUser(t, m)
	intruder := seedUser(t, m)

	if err := m.InsertNotification(ctx, uid, NotifReportDismissed, 3, "sunny skin"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	rows, _, err := m.ListNotifications(ctx, uid, false, 1, 20)
	if err != nil || len(rows) != 1 {
		t.Fatalf("list = (%d rows, %v), want 1/nil", len(rows), err)
	}

	if err := m.MarkNotificationRead(ctx, intruder, rows[0].ID); err != nil {
		t.Fatalf("intruder mark: %v", err)
	}
	if n, err := m.UnreadNotificationCount(ctx, uid); err != nil || n != 1 {
		t.Fatalf("owner unread = (%d, %v), want (1, nil) — foreign uid marked someone else's row", n, err)
	}
	if n, err := m.UnreadNotificationCount(ctx, intruder); err != nil || n != 0 {
		t.Fatalf("intruder unread = (%d, %v), want (0, nil)", n, err)
	}

	if err := m.MarkAllNotificationsRead(ctx, uid); err != nil {
		t.Fatalf("mark all: %v", err)
	}
	if n, err := m.UnreadNotificationCount(ctx, uid); err != nil || n != 0 {
		t.Fatalf("owner unread after mark-all = (%d, %v), want (0, nil)", n, err)
	}
}

func TestIntegrationNotificationPaging(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	uid := seedUser(t, m)

	for i := 0; i < 5; i++ {
		if err := m.InsertNotification(ctx, uid, NotifReportResolved, int64(i+1), "t"); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	page2, total, err := m.ListNotifications(ctx, uid, false, 2, 2)
	if err != nil {
		t.Fatalf("list page 2: %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}
	if len(page2) != 2 {
		t.Errorf("page 2 = %d rows, want 2", len(page2))
	}
	// Page beyond the end is empty, not an error.
	beyond, _, err := m.ListNotifications(ctx, uid, false, 9, 2)
	if err != nil || len(beyond) != 0 {
		t.Fatalf("beyond end = (%d rows, %v), want (0, nil)", len(beyond), err)
	}
}
