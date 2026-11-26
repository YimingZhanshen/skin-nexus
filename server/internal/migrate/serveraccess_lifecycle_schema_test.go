package migrate

import (
	"os"
	"path/filepath"
	"testing"

	"skin-nexus/server/internal/dialect"
)

func TestSQLiteUpgradeAddsServerAccessLifecycleWithoutLosingData(t *testing.T) {
	db := openSQLite(t)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	oldDir := filepath.Join(root, "sqlite")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, file := range filesIn(t, dialect.SQLite) {
		if filepath.Base(file) >= "0036_serveraccess_lifecycle.sql" {
			continue
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(oldDir, filepath.Base(file)), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := ApplySQLDirTracked(db, root, dialect.SQLite); err != nil {
		t.Fatalf("apply pre-0036 tree: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (nickname, password) VALUES ('owner', 'hash')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sa_tenants (id, name, status) VALUES (1, 'retained', 'active')`); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(trees, "sqlite", "0036_serveraccess_lifecycle.sql"))
	if err != nil {
		t.Fatalf("read 0036 lifecycle migration: %v", err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "0036_serveraccess_lifecycle.sql"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ApplySQLDirTracked(db, root, dialect.SQLite); err != nil {
		t.Fatalf("upgrade with 0036: %v", err)
	}
	rows, err := db.Query(`PRAGMA table_info(sa_tenants)`)
	if err != nil {
		t.Fatal(err)
	}
	columns := map[string]bool{}
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		columns[name] = true
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"deletion_requested_at", "deletion_due_at"} {
		if !columns[column] {
			t.Errorf("sa_tenants missing %s after 0036", column)
		}
	}
	var name, status string
	var requested, due any
	if err := db.QueryRow(`SELECT name,status,deletion_requested_at,deletion_due_at FROM sa_tenants WHERE id=1`).Scan(&name, &status, &requested, &due); err != nil {
		t.Fatal(err)
	}
	if name != "retained" || status != "active" || requested != nil || due != nil {
		t.Fatalf("0036 changed existing tenant: name=%q status=%q requested=%v due=%v", name, status, requested, due)
	}
	if _, err := db.Exec(`UPDATE sa_tenants SET status='deleting',deletion_requested_at=datetime('now'),deletion_due_at=datetime('now','+7 days') WHERE id=1`); err != nil {
		t.Fatalf("write lifecycle timestamps: %v", err)
	}
}
