//go:build integration

// reused_name_integration_test.go — ticket slice 3 (P2).
//
// Reproduced chain: create a player, rename it, then create a player with the
// ABANDONED name. The abandoned name is free in players.name, but its
// OfflineUUID value is still owned by the renamed row (铁律 2 keeps uuid_map.uuid
// stable across a rename), so the new uuid_map insert collides on the UNIQUE
// uuid key (analysis/g10-e2-uuid-chain.md §2, the sharpest E2 finding).
//
// Before the fix that collision escaped store.CreatePlayer as a RAW driver
// error (web.go never called isDupKey on the uuid_map insert, unlike
// AdminRenamePlayer), so account.CreatePlayer fell past its duplicate branch
// and answered CodeInternal — a 500-shaped answer for a user-level condition.
// This test asserts the response is the already-declared "name not available"
// code (CodeNameTaken → HTTP 409), never an internal error, and that nothing in
// the chain yields a 500. It runs on SQLite through SKIN_TEST_DSN, exactly like
// the MySQL integration suite; the names carry a per-run suffix so the collision
// is reproduced cleanly on a shared/reused database without dropping tables.

package account

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"skin-nexus/server/internal/migrate"
	"skin-nexus/server/internal/siteurl"
	"skin-nexus/server/internal/store"
)

func openIntegrationStore(t *testing.T) *store.Mysql {
	t.Helper()
	dsn := os.Getenv("SKIN_TEST_DSN")
	if dsn == "" {
		t.Skip("SKIN_TEST_DSN not set; skipping the reused-name integration test")
	}
	siteurl.Set("https://skin.test")
	m, err := store.Open("", dsn, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	// The tracked migration path is idempotent, so this is safe against both a
	// fresh disposable file and a database a previous run left behind.
	if err := migrate.ApplySQLDirTracked(m.DB(), filepath.Join("..", "..", "migrations"), m.Engine()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return m
}

func TestIntegrationReusedNameReportsNameTakenNotInternal(t *testing.T) {
	m := openIntegrationStore(t)
	svc := &Service{Store: m, MaxChars: 3}
	ctx := context.Background()

	suffix := time.Now().UnixNano()
	email := fmt.Sprintf("p2-reused-%d@example.com", suffix)
	nick := fmt.Sprintf("p2_reused_%d", suffix)
	uid, err := m.CreateUser(ctx, email, nick, "x", 3, "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	const oldName = "P2ReusedOldName"
	const newName = "P2ReusedNewName"

	view, created, e := svc.CreatePlayer(ctx, uid, oldName, CallMeta{})
	if e != nil || !created || view.ID == 0 {
		t.Fatalf("first create must succeed: created=%v view=%+v err=%v", created, view, e)
	}

	// Rename frees players.name for the old name but keeps uuid_map.uuid (铁律 2).
	if _, _, e := svc.RenamePlayer(ctx, uid, view.ID, newName, CallMeta{}); e != nil {
		t.Fatalf("rename failed: %v", e)
	}

	// Recreating the abandoned name collides on uuid_map.uuid. The response must
	// be the clean "name not available" code, not an internal error.
	_, _, e = svc.CreatePlayer(ctx, uid, oldName, CallMeta{})
	if e == nil {
		t.Fatalf("recreating a renamed-away name unexpectedly succeeded — uuid_map UNIQUE(uuid) collision did not reproduce; revisit this test")
	}
	if e.Code != CodeNameTaken {
		t.Fatalf("reused-name create answered code %q (message %q), want %q", e.Code, e.Message, CodeNameTaken)
	}
	if e.Code == CodeInternal {
		t.Fatalf("reused-name create regressed to an internal error: %v", e)
	}
	// No 500 anywhere on this path: the name-not-available answer is 409.
	if status := e.Status(); status != http.StatusConflict {
		t.Fatalf("reused-name create status = %d (code %q), want 409 — never a 500 for a user-level condition", status, e.Code)
	}
	if status := e.Status(); status >= 500 {
		t.Fatalf("response is a 5xx: %d", status)
	}

	// The renamed player is untouched by the failed recreate: name and UUID both
	// still resolve (铁律 2 — nothing here may delete rows or recompute the UUID).
	p, err := m.PlayerByName(ctx, newName)
	if err != nil || p.UUID != store.OfflineUUID(oldName) {
		t.Fatalf("renamed player lost its identity: %+v err=%v", p, err)
	}
}
