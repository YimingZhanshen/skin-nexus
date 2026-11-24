package migrate

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"skin-nexus/server/internal/dialect"
)

func TestServerAccessCoreSchemaShape(t *testing.T) {
	wantTables := map[string]bool{"sa_tenants": true, "sa_tenant_members": true, "sa_servers": true}
	wantColumns := map[string]bool{
		"sa_tenants.id": true, "sa_tenants.name": true, "sa_tenants.status": true,
		"sa_tenants.created_at": true, "sa_tenants.deleted_at": true,
		"sa_tenant_members.tenant_id": true, "sa_tenant_members.uid": true,
		"sa_tenant_members.role": true, "sa_tenant_members.created_at": true,
		"sa_servers.id": true, "sa_servers.tenant_id": true, "sa_servers.public_id": true,
		"sa_servers.name": true, "sa_servers.status": true, "sa_servers.source_mode": true,
		"sa_servers.created_at": true,
	}
	wantIndexes := map[string]bool{
		"idx_sa_tenants_status": true, "idx_sa_tenants_deleted_at": true,
		"idx_sa_tenant_members_uid": true, "uq_sa_servers_public_id": true,
		"idx_sa_servers_tenant_status": true,
	}
	wantForeignKeys := map[string]bool{
		"fk_sa_tenant_members_tenant": true,
		"fk_sa_tenant_members_user":   true,
		"fk_sa_servers_tenant":        true,
	}
	reForeignKey := regexp.MustCompile(`CONSTRAINT\s+(\w+)\s+FOREIGN KEY`)
	for _, eng := range []dialect.Engine{dialect.MySQL, dialect.Postgres, dialect.SQLite} {
		s := readShape(t, eng)
		for table := range wantTables {
			if !s.tables[table] {
				t.Errorf("%s missing %s", eng, table)
			}
		}
		for col := range wantColumns {
			if !s.columns[col] {
				t.Errorf("%s missing %s", eng, col)
			}
		}
		for idx := range wantIndexes {
			if !s.indexes[idx] {
				t.Errorf("%s missing %s", eng, idx)
			}
		}

		raw, err := os.ReadFile(filepath.Join(TreeDir(trees, eng), "0033_serveraccess_core.sql"))
		if err != nil {
			t.Fatal(err)
		}
		flat := strings.Join(strings.Fields(stripComments(string(raw))), " ")
		var tables []string
		for _, stmt := range SplitSQL(string(raw)) {
			if m := reCreateTable.FindStringSubmatch(strings.Join(strings.Fields(stripComments(stmt)), " ")); m != nil {
				tables = append(tables, m[2])
			}
		}
		if len(tables) != 3 {
			t.Errorf("%s 0033 creates %v, want exactly the three core tables", eng, tables)
		}
		for fk := range wantForeignKeys {
			if !reForeignKey.MatchString(flat) || !strings.Contains(flat, "CONSTRAINT "+fk+" FOREIGN KEY") {
				t.Errorf("%s 0033 missing foreign key %s", eng, fk)
			}
		}
	}
}

func TestSQLiteUpgradeAddsServerAccessCoreWithoutLosingData(t *testing.T) {
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
		name := filepath.Base(file)
		if strings.Compare(name, "0033_serveraccess_core.sql") > 0 {
			continue
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(oldDir, name), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := ApplySQLDirTracked(db, root, dialect.SQLite); err != nil {
		t.Fatalf("apply pre-0033 tree: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO options (k, v) VALUES ('upgrade_sentinel', 'kept')`); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(trees, "sqlite", "0033_serveraccess_core.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "0033_serveraccess_core.sql"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ApplySQLDirTracked(db, root, dialect.SQLite); err != nil {
		t.Fatalf("upgrade with 0033: %v", err)
	}
	var sentinel string
	if err := db.QueryRow(`SELECT v FROM options WHERE k = 'upgrade_sentinel'`).Scan(&sentinel); err != nil || sentinel != "kept" {
		t.Fatalf("old data after upgrade = %q, err %v", sentinel, err)
	}
	for _, table := range []string{"sa_tenants", "sa_tenant_members", "sa_servers"} {
		if _, err := db.Exec(`SELECT * FROM ` + table + ` LIMIT 0`); err != nil {
			t.Errorf("%s unusable after upgrade: %v", table, err)
		}
	}

	if _, err := db.Exec(`INSERT INTO users (nickname, password) VALUES ('owner', 'hash')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sa_tenants (id, name) VALUES (1, 'tenant')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sa_tenant_members (tenant_id, uid, role) VALUES (1, 1, 'owner')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sa_servers (tenant_id, public_id, name) VALUES (1, 'srv_public', 'server')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM sa_tenants WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"sa_tenant_members", "sa_servers"} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s rows after tenant delete = %d, err %v; want cascade", table, n, err)
		}
	}
	var users int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users WHERE id = 1`).Scan(&users); err != nil || users != 1 {
		t.Fatalf("global user after tenant delete = %d, err %v; must be retained", users, err)
	}
}
