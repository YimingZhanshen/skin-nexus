//go:build integration

// accesslog_integration_test.go: access_logs round-trip — insert/list
// filters + pagination + retention purge. See integration_test.go harness.
package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"skin-nexus/server/internal/accesslog"
)

func TestIntegrationAccessLogInsertListPurge(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	uid := seedUser(t, m)

	seed := []accesslog.Entry{
		{Kind: accesslog.WebLogin, Outcome: accesslog.OutcomeOK, UID: uid, Who: "alice", IP: "10.0.0.1", UA: "Mozilla/5.0", Detail: "web"},
		{Kind: accesslog.WebLogin, Outcome: accesslog.OutcomeFail, Who: "alice", IP: "10.0.0.2", UA: "curl/8", Detail: "bad_password"},
		{Kind: accesslog.YggHasJoined, Outcome: accesslog.OutcomeMiss, Who: "Steve", IP: "203.0.113.7", UA: "Spigot", Detail: "serverId=abc"},
		{Kind: accesslog.YggProfile, Outcome: accesslog.OutcomeOK, Who: strings.Repeat("f", 32), IP: "203.0.113.7", UA: "launcher", Detail: "fallback=off"},
		{Kind: accesslog.UnionOut, Outcome: accesslog.OutcomeError, Who: "/api/union/serverlist", Detail: "GET status=0 err=timeout"},
	}
	for _, e := range seed {
		if err := m.InsertAccessLog(ctx, e); err != nil {
			t.Fatalf("insert %s: %v", e.Kind, err)
		}
	}

	// All rows, newest first.
	rows, total, err := m.ListAccessLogs(ctx, "", "", 1, 50)
	if err != nil || total != len(seed) {
		t.Fatalf("list all = (%d rows, total %d, %v); want %d", len(rows), total, err, len(seed))
	}
	if rows[0].Kind != accesslog.UnionOut {
		t.Errorf("newest-first violated: first = %s", rows[0].Kind)
	}

	// Kind filter.
	rows, total, err = m.ListAccessLogs(ctx, accesslog.WebLogin, "", 1, 50)
	if err != nil || total != 2 {
		t.Fatalf("kind filter = (total %d, %v); want 2", total, err)
	}
	for _, r := range rows {
		if r.Kind != accesslog.WebLogin {
			t.Errorf("kind filter leaked %s", r.Kind)
		}
	}

	// Substring search across who/ip/detail (the operator's grep).
	for _, q := range []string{"alice", "10.0.0.2", "bad_password"} {
		_, total, err := m.ListAccessLogs(ctx, "", q, 1, 50)
		if err != nil || total == 0 {
			t.Errorf("q=%q matched %d (err %v); want >0", q, total, err)
		}
	}
	if _, total, _ := m.ListAccessLogs(ctx, "", "no-such-fragment", 1, 50); total != 0 {
		t.Errorf("bogus q matched %d rows", total)
	}

	// Pagination: size2 → page2 must hold strictly older ids than page1.
	p1, _, err := m.ListAccessLogs(ctx, "", "", 1, 2)
	if err != nil || len(p1) != 2 {
		t.Fatalf("page1 = (%d, %v); want 2", len(p1), err)
	}
	p2, _, err := m.ListAccessLogs(ctx, "", "", 2, 2)
	if err != nil || len(p2) != 2 {
		t.Fatalf("page2 = (%d, %v); want 2", len(p2), err)
	}
	if p2[0].ID >= p1[0].ID || p2[0].ID >= p1[1].ID {
		t.Errorf("page ordering broken: p1=[%d,%d] p2=[%d,…]", p1[0].ID, p1[1].ID, p2[0].ID)
	}

	// uid round-trips for account-attributed rows.
	logged, _, err := m.ListAccessLogs(ctx, "", "alice", 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	var sawUID bool
	for _, r := range logged {
		if r.UID == uid {
			sawUID = true
		}
	}
	if !sawUID {
		t.Errorf("uid %d not returned on web_login rows", uid)
	}

	// Retention purge: rewind one row past 90 days, purge, assert only it died.
	if _, err := m.db.ExecContext(ctx,
		`UPDATE access_logs SET created_at = ? WHERE kind = ?`,
		time.Now().UTC().Add(-100*24*time.Hour), accesslog.UnionOut); err != nil {
		t.Fatalf("rewind: %v", err)
	}
	deleted, err := m.PurgeAccessLogs(ctx, 90*24*time.Hour)
	if err != nil || deleted != 1 {
		t.Fatalf("purge = (%d, %v); want 1", deleted, err)
	}
	if _, total, _ := m.ListAccessLogs(ctx, "", "", 1, 50); total != len(seed)-1 {
		t.Errorf("after purge total = %d, want %d", total, len(seed)-1)
	}
}
