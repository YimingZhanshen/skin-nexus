//go:build integration

package store

import (
	"context"
	"reflect"
	"testing"

	"skin-nexus/server/internal/migrate"
)

func TestIntegrationImportDryRunLeavesEveryTargetTableUntouched(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	seedLegacy(t, m, phpBcryptHash(t))

	existing := seedUserAs(t, m, "dry-run-existing@example.com", "dry_run_existing")
	tables := []string{"users", "players", "textures", "uuid_map", "options", "user_closet", "invitation_codes"}
	before := make(map[string][][]any, len(tables))
	for _, table := range tables {
		before[table] = snapshotTableRows(t, m, table)
	}

	rep, err := migrate.RunImport(ctx, migrate.ImportOptions{
		Source: m.DBX(), Target: m.DBX(), SourcePrefix: "legacy_", DryRun: true,
	})
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if got := rep.Tables["users"]; got.Selected != 2 || got.Inserted != 0 {
		t.Fatalf("dry-run users = %+v, want selected=2 inserted=0", got)
	}
	for _, table := range tables {
		after := snapshotTableRows(t, m, table)
		if !reflect.DeepEqual(after, before[table]) {
			t.Errorf("dry-run changed %s rows", table)
		}
	}
	if _, err := m.UserByID(ctx, existing); err != nil {
		t.Fatalf("dry-run removed the pre-existing user: %v", err)
	}
}

func snapshotTableRows(t *testing.T, m *Mysql, table string) [][]any {
	t.Helper()
	rows, err := m.db.QueryContext(context.Background(), "SELECT * FROM "+table)
	if err != nil {
		t.Fatalf("snapshot %s: %v", table, err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatalf("snapshot %s columns: %v", table, err)
	}
	var out [][]any
	for rows.Next() {
		values := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for i := range values {
			destinations[i] = &values[i]
		}
		if err := rows.Scan(destinations...); err != nil {
			t.Fatalf("snapshot %s row: %v", table, err)
		}
		out = append(out, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("snapshot %s rows: %v", table, err)
	}
	return out
}

func TestIntegrationEnsureSuperAdminPromotesOldestLegacyAdmin(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	oldest := seedUser(t, m)
	newer := seedUser(t, m)
	if err := m.SetUserPermission(ctx, newer, 1); err != nil {
		t.Fatal(err)
	}
	if err := m.SetUserPermission(ctx, oldest, 1); err != nil {
		t.Fatal(err)
	}

	promoted, changed, err := m.EnsureSuperAdmin(ctx)
	if err != nil || !changed || promoted != oldest {
		t.Fatalf("promote = uid:%d changed:%v err:%v, want uid:%d changed:true", promoted, changed, err, oldest)
	}
	if permission, err := m.UserPermission(ctx, oldest); err != nil || permission != 2 {
		t.Fatalf("promoted permission = %d err:%v, want 2", permission, err)
	}
	if permission, err := m.UserPermission(ctx, newer); err != nil || permission != 1 {
		t.Fatalf("other admin permission = %d err:%v, want 1", permission, err)
	}
	if promoted, changed, err := m.EnsureSuperAdmin(ctx); err != nil || changed || promoted != 0 {
		t.Fatalf("second ensure = uid:%d changed:%v err:%v, want uid:0 changed:false", promoted, changed, err)
	}
}

func TestIntegrationSkinlibOnePrivateVisibilityIsOwnerScoped(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	owner := seedUser(t, m)
	other := seedUser(t, m)
	privateID, err := m.AddTexturePrivate(ctx, owner, "cape", "portability-private-cape", "", TextureMeta{Description: "private cape"})
	if err != nil {
		t.Fatalf("add private cape: %v", err)
	}

	for _, tc := range []struct {
		name        string
		viewerUID   int64
		viewerAdmin bool
		wantFound   bool
	}{
		{name: "anonymous", viewerUID: 0},
		{name: "other user", viewerUID: other},
		{name: "owner", viewerUID: owner, wantFound: true},
		{name: "admin", viewerUID: 0, viewerAdmin: true, wantFound: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row, err := m.SkinlibOne(ctx, privateID, tc.viewerUID, tc.viewerAdmin)
			if !tc.wantFound {
				if err != ErrNotFound {
					t.Fatalf("SkinlibOne err = %v, want ErrNotFound", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("SkinlibOne: %v", err)
			}
			if row.TID != privateID || row.UID != owner || row.Public {
				t.Fatalf("SkinlibOne = %+v, want private tid=%d owner=%d", row, privateID, owner)
			}
		})
	}
}
