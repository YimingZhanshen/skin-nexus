//go:build integration

// The boot rewrite builds its own SQL from a table/column list (digest.go), so
// "it works" is a claim about four engines, not one. The local rig can only
// ever prove the SQLite leg; this file is what MySQL, MariaDB and PostgreSQL
// answer it with.

package store

import (
	"context"
	"testing"
	"time"
)

func TestIntegrationTokenDigestRewriteIsPortable(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	uid := seedUser(t, m)
	now := time.Now().UTC()
	deferNow := now.Add(time.Hour)

	// Rows in the shape a real upgrade finds them: raw credentials written by
	// the version that predated digests, inserted straight past the store API.
	if _, err := m.db.ExecContext(ctx,
		`INSERT INTO ygg_tokens (user_id, client_token, access_token, created_at, expires_at, invalidated) VALUES (?,?,?,?,?,0)`,
		uid, testClientToken32, rawAccessToken, now, deferNow); err != nil {
		t.Fatalf("plant token: %v", err)
	}
	if _, err := m.db.ExecContext(ctx,
		`INSERT INTO web_sessions (token, user_id, created_at, expires_at) VALUES (?,?,?,?)`,
		rawSessionToken, uid, now, deferNow); err != nil {
		t.Fatalf("plant session: %v", err)
	}
	if _, err := m.db.ExecContext(ctx,
		`INSERT INTO ygg_join_sessions (access_token, uuid, server_id, server_ip, created_at, expires_at) VALUES (?,?,?,?,?,?)`,
		rawLauncherToken, "069a79f444e94726a5befca90e38aaf5", "srv-portable", "203.0.113.9", now, deferNow); err != nil {
		t.Fatalf("plant join: %v", err)
	}
	before := countRows(t, m, "ygg_tokens") + countRows(t, m, "web_sessions") + countRows(t, m, "ygg_join_sessions")

	rewritten, err := m.EnsureTokenDigests(ctx)
	if err != nil {
		t.Fatalf("%s: rewrite: %v", m.Engine(), err)
	}
	if rewritten != 3 {
		t.Errorf("%s: rewrote %d rows, want 3 (one per credential column)", m.Engine(), rewritten)
	}
	after := countRows(t, m, "ygg_tokens") + countRows(t, m, "web_sessions") + countRows(t, m, "ygg_join_sessions")
	if after != before {
		t.Errorf("%s: the rewrite changed the row count: %d → %d", m.Engine(), before, after)
	}

	// Hand-computed digests, same constants the unit leg uses.
	if got := selectString(t, m, `SELECT access_token FROM ygg_tokens WHERE user_id = ?`, uid); got != digestAccessToken {
		t.Errorf("%s: token column = %q, want %q", m.Engine(), got, digestAccessToken)
	}
	if got := selectString(t, m, `SELECT token FROM web_sessions WHERE user_id = ?`, uid); got != digestSessionToken {
		t.Errorf("%s: session column = %q, want %q", m.Engine(), got, digestSessionToken)
	}
	if got := selectString(t, m, `SELECT access_token FROM ygg_join_sessions WHERE server_id = ?`, "srv-portable"); got != digestLauncherTok {
		t.Errorf("%s: join column = %q, want %q", m.Engine(), got, digestLauncherTok)
	}
	// web_sessions is rewritten through its own primary key. If the key form
	// differed between engines, this is the lookup that would miss.
	if got, err := m.SessionUser(ctx, rawSessionToken); err != nil || got != uid {
		t.Errorf("%s: in-flight session = %d %v, want uid %d", m.Engine(), got, err, uid)
	}
	if _, err := m.TokenByAccess(ctx, rawAccessToken); err != nil {
		t.Errorf("%s: in-flight launcher token broke across the rewrite: %v", m.Engine(), err)
	}
	if _, err := m.JoinByTokenServer(ctx, rawLauncherToken, "srv-portable"); err != nil {
		t.Errorf("%s: in-flight join broke across the rewrite: %v", m.Engine(), err)
	}

	again, err := m.EnsureTokenDigests(ctx)
	if err != nil {
		t.Fatalf("%s: second pass: %v", m.Engine(), err)
	}
	if again != 0 {
		t.Errorf("%s: second pass rewrote %d rows, want 0 (re-running would digest the digests)", m.Engine(), again)
	}
	if got := selectString(t, m, `SELECT access_token FROM ygg_tokens WHERE user_id = ?`, uid); got != digestAccessToken {
		t.Errorf("%s: token column after the second pass = %q, want it unchanged", m.Engine(), got)
	}
}
