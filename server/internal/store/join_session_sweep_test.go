package store

// hasJoined lookups and the row lifetime of ygg_join_sessions. The independent
// source of truth for the row counts here is the SQL the read path writes, read
// back straight from the table — not a re-run of the implementation.

import (
	"context"
	"strings"
	"testing"
	"time"

	"skin-nexus/server/internal/ygg"
)

func plantJoin(t *testing.T, st *Mysql, uuid, serverID string, expiresAt time.Time) {
	t.Helper()
	if err := st.SaveJoin(context.Background(), ygg.JoinSession{
		AccessToken: "tok-" + serverID, UUID: uuid, ServerID: serverID,
		ServerIP: "127.0.0.1", CreatedAt: time.Now().UTC(), ExpiresAt: expiresAt,
	}); err != nil {
		t.Fatalf("plant join %s: %v", serverID, err)
	}
}

// A join session lives 120 seconds, and the expiry only hides it: nothing has
// ever deleted these rows, so the table grows with traffic forever while the
// lookup that reads it had no index on the column it matches.
func TestPurgeJoinSessionsDropsOnlyExpiredRows(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	plantJoin(t, st, "aabbccddee00112233445566778899aa", "srv-dead-1", now.Add(-2*time.Minute))
	plantJoin(t, st, "aabbccddee00112233445566778899aa", "srv-dead-2", now.Add(-time.Second))
	plantJoin(t, st, "ff00ff00ff00ff00ff00ff00ff00ff00", "srv-live", now.Add(90*time.Second))
	if got := countRows(t, st, "ygg_join_sessions"); got != 3 {
		t.Fatalf("fixture rows = %d, want 3", got)
	}

	n, err := st.PurgeJoinSessions(ctx)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 2 {
		t.Fatalf("purged = %d, want the 2 expired rows", n)
	}
	if got := countRows(t, st, "ygg_join_sessions"); got != 1 {
		t.Fatalf("rows left = %d, want only the live one", got)
	}

	// The sweep must not be a truncation: the live session still answers, and
	// the expired uuid no longer drags a dead row into the result.
	live, err := st.FindLiveJoins(ctx, "ff00ff00ff00ff00ff00ff00ff00ff00", now)
	if err != nil {
		t.Fatalf("FindLiveJoins: %v", err)
	}
	if len(live) != 1 || live[0].ServerID != "srv-live" {
		t.Fatalf("live joins = %+v, want the one unexpired row", live)
	}
	dead, err := st.FindLiveJoins(ctx, "aabbccddee00112233445566778899aa", now)
	if err != nil {
		t.Fatalf("FindLiveJoins (dead uuid): %v", err)
	}
	if len(dead) != 0 {
		t.Fatalf("expired uuid still resolves %d join row(s)", len(dead))
	}

	// Idempotent: a second sweep with nothing past expiry deletes nothing.
	if n, err := st.PurgeJoinSessions(ctx); err != nil || n != 0 {
		t.Fatalf("second purge = (%d, %v), want no rows and no error", n, err)
	}
}

// The lookup filters on uuid; the migration tree indexes only access_token, so
// every hasJoined was a full scan. Same statement for the sweep's own column.
func TestJoinSessionsAreIndexedForLookupAndSweep(t *testing.T) {
	st := openSQLiteStore(t)
	indexes := map[string]string{}
	rows, err := st.db.Query(`SELECT name, sql FROM sqlite_master WHERE type='index' AND tbl_name='ygg_join_sessions'`)
	if err != nil {
		t.Fatalf("read indexes: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, sql string
		if err := rows.Scan(&name, &sql); err != nil {
			t.Fatalf("scan index row: %v", err)
		}
		indexes[name] = sql
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate indexes: %v", err)
	}

	for _, want := range []struct{ name, column string }{
		{"idx_join_uuid", "uuid"},
		{"idx_join_expiry", "expires_at"},
	} {
		ddl, ok := indexes[want.name]
		if !ok {
			t.Fatalf("no %s on ygg_join_sessions; found %v", want.name, indexes)
		}
		if !strings.Contains(strings.ToLower(ddl), want.column) {
			t.Fatalf("%s = %q, want it to cover %s", want.name, ddl, want.column)
		}
	}
}
