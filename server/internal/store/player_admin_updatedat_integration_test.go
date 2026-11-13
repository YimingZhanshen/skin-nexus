//go:build integration

package store

import (
	"context"
	"testing"
	"time"
)

// players.updated_at is DATETIME(3) WITH ON UPDATE CURRENT_TIMESTAMP on MySQL
// only — PG/SQLite have no auto-refresh, so an UPDATE that omits the column
// bumps it on one engine and leaves it stale on the other two. The panel's
// updatedAt column reads this field, so the admin rename/owner-change paths
// must write it explicitly (store/player_admin.go). Red on SQLite before the
// fix: the value simply does not move.
func TestIntegrationAdminPlayersUpdatedAtBumps(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	uid := seedUserAs(t, m, "padmin-stamp@example.com", "padmin_user_stamp")
	other := seedUserAs(t, m, "padmin-stamp2@example.com", "padmin_user_stamp2")
	pid, _, err := m.CreatePlayer(ctx, uid, "PAdminStamp")
	if err != nil {
		t.Fatalf("create player: %v", err)
	}
	readAt := func() time.Time {
		t.Helper()
		var ts time.Time
		if err := m.db.QueryRowContext(ctx,
			`SELECT updated_at FROM players WHERE id = ?`, pid).Scan(&ts); err != nil {
			t.Fatalf("read updated_at: %v", err)
		}
		return ts
	}

	// DATETIME(3): the clock must actually advance between write and read,
	// otherwise equal millisecond timestamps read as "no bump".
	base := readAt()
	time.Sleep(20 * time.Millisecond)
	if err := m.AdminRenamePlayer(ctx, pid, "PAdminStamped"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if after := readAt(); !after.After(base) {
		t.Fatalf("rename did not bump updated_at: %v -> %v", base, after)
	}

	mid := readAt()
	time.Sleep(20 * time.Millisecond)
	if err := m.AdminSetPlayerOwner(ctx, pid, other); err != nil {
		t.Fatalf("owner change: %v", err)
	}
	if after := readAt(); !after.After(mid) {
		t.Fatalf("owner change did not bump updated_at: %v -> %v", mid, after)
	}
}
