package account

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"skin-nexus/server/internal/migrate"
	"skin-nexus/server/internal/siteurl"
	"skin-nexus/server/internal/store"
)

// newUnionSyncService builds the same real-database stack the upload tests use.
// The promise under test spans three layers — the rename write, the uuid the
// store kept through it, and the announcement the hub needs — so a fake store
// would only prove that the call site exists where I wrote it.
func newUnionSyncService(t *testing.T) (*Service, *[]string) {
	t.Helper()
	siteurl.Set("https://skin.test")

	dir := t.TempDir()
	m, err := store.Open("sqlite", "sqlite:"+filepath.Join(dir, "site.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	if err := migrate.ApplySQLDirTracked(m.DB(), filepath.Join("..", "..", "migrations"), m.Engine()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	calls := &[]string{}
	svc := &Service{
		Store:    m,
		MaxChars: 3,
		UnionCreate: func(_ context.Context, uuid, name string) {
			*calls = append(*calls, "create "+uuid+" "+name)
		},
		UnionRename: func(_ context.Context, uuid, name string) {
			*calls = append(*calls, "rename "+uuid+" "+name)
		},
		UnionRetire: func(_ context.Context, uuid, name string) {
			*calls = append(*calls, "delete "+uuid+" "+name)
		},
	}
	return svc, calls
}

// A member site that renames or deletes a character has to say so: the hub
// publishes profiles by name, and a member that stops talking keeps a name on
// the hub that resolves to nothing here. The reference implementation fires all
// three legs from core player events (yggdrasil-connect bootstrap.php:84-119),
// which is why the announcement belongs on the service — every way a player is
// renamed or retired in SkinNexus goes through it, including a plugin doing it
// through the account bridge.
//
// The uuid must be identical across all three lines: renaming a player never
// recomputes it (iron rule 2, and the reference deliberately commented its own
// regeneration out for exactly this reason).
func TestPlayerLifecycleAnnouncesRenameAndRetirementToTheHub(t *testing.T) {
	svc, calls := newUnionSyncService(t)
	ctx := context.Background()

	uid, err := svc.Store.CreateUser(ctx,
		fmt.Sprintf("union-sync-%d@example.com", time.Now().UnixNano()), "union_sync", "x", 3, "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	created, _, e := svc.CreatePlayer(ctx, uid, "unionsteve", CallMeta{})
	if e != nil {
		t.Fatalf("create player: %v", e)
	}
	uuid := created.UUID

	renamed, _, e := svc.RenamePlayer(ctx, uid, created.ID, "unionsteve2", CallMeta{})
	if e != nil {
		t.Fatalf("rename player: %v", e)
	}
	if renamed.UUID != uuid {
		t.Fatalf("rename drifted the uuid: %s -> %s", uuid, renamed.UUID)
	}

	if e := svc.DeletePlayer(ctx, uid, created.ID, CallMeta{}); e != nil {
		t.Fatalf("delete player: %v", e)
	}

	want := []string{
		"create " + uuid + " unionsteve",
		"rename " + uuid + " unionsteve2",
		"delete " + uuid + " unionsteve2",
	}
	if strings.Join(*calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("hub announcements:\n got: %v\nwant: %v", *calls, want)
	}
}

// Silence is correct when nobody is listening: with no hub wired the service
// must not invent announcements (the fields are nil on every station that has
// Union off, and a nil check missing here is a panic on a rename).
func TestPlayerLifecycleWithoutUnionStaysQuiet(t *testing.T) {
	svc, calls := newUnionSyncService(t)
	svc.UnionCreate, svc.UnionRename, svc.UnionRetire = nil, nil, nil
	ctx := context.Background()

	uid, err := svc.Store.CreateUser(ctx,
		fmt.Sprintf("union-off-%d@example.com", time.Now().UnixNano()), "union_off", "x", 3, "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	p, _, e := svc.CreatePlayer(ctx, uid, "unionquiet", CallMeta{})
	if e != nil {
		t.Fatalf("create player: %v", e)
	}
	if _, _, e := svc.RenamePlayer(ctx, uid, p.ID, "unionquiet2", CallMeta{}); e != nil {
		t.Fatalf("rename player: %v", e)
	}
	if e := svc.DeletePlayer(ctx, uid, p.ID, CallMeta{}); e != nil {
		t.Fatalf("delete player: %v", e)
	}
	if len(*calls) != 0 {
		t.Fatalf("announced %d times with no hub configured", len(*calls))
	}
}
