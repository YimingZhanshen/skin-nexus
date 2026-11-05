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
	plantRefresh := rawRefreshToken("portable")
	// selected_pid stays NULL: an empty string would land in an INTEGER column,
	// which SQLite swallows and MySQL rejects — and this file is the leg that
	// has to stay honest across all four engines.
	if _, err := m.db.ExecContext(ctx,
		`INSERT INTO oidc_refresh_tokens (token, uid, client_id, scopes, selected_pid, auth_time, expires_at, created_at, revoked)
		 VALUES (?,?,?, ?,NULL,?,?,?,0)`,
		plantRefresh, uid, "launcher", `["openid"]`, now, deferNow, now); err != nil {
		t.Fatalf("plant refresh token: %v", err)
	}
	before := countRows(t, m, "ygg_tokens") + countRows(t, m, "web_sessions") +
		countRows(t, m, "ygg_join_sessions") + countRows(t, m, "oidc_refresh_tokens")

	rewritten, err := m.EnsureTokenDigests(ctx)
	if err != nil {
		t.Fatalf("%s: rewrite: %v", m.Engine(), err)
	}
	if rewritten != 4 {
		t.Errorf("%s: rewrote %d rows, want 4 (one per credential column)", m.Engine(), rewritten)
	}
	after := countRows(t, m, "ygg_tokens") + countRows(t, m, "web_sessions") +
		countRows(t, m, "ygg_join_sessions") + countRows(t, m, "oidc_refresh_tokens")
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
	if got := selectString(t, m, `SELECT token FROM oidc_refresh_tokens WHERE uid = ?`, uid); got != digestOf(plantRefresh) {
		t.Errorf("%s: refresh column = %q, want %q", m.Engine(), got, digestOf(plantRefresh))
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
	// A launcher holding the old refresh token must be able to rotate it after
	// the rewrite, and the record it gets back has to carry the value it
	// presented — not the column's digest, which the rotation would hash twice.
	got, err := m.GetRefreshToken(ctx, plantRefresh)
	if err != nil {
		t.Errorf("%s: in-flight refresh token broke across the rewrite: %v", m.Engine(), err)
	} else if got.Token != plantRefresh {
		t.Errorf("%s: GetRefreshToken handed back %q, want the presented value", m.Engine(), got.Token)
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
