package migrate

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"skin-nexus/server/internal/dialect"
)

// applyTreeWithoutAutoincrement builds a database that looks exactly like one
// installed before the three hot tables declared AUTOINCREMENT: the shipped SQLite
// tree with that keyword stripped out of the DDL text.
func applyTreeWithoutAutoincrement(t *testing.T, db *sql.DB) {
	t.Helper()
	dir := TreeDir(trees, dialect.SQLite)
	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no files under %s (err %v)", dir, err)
	}
	sort.Strings(files)
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		text := strings.ReplaceAll(string(raw), " AUTOINCREMENT", "")
		for _, stmt := range SplitSQL(text) {
			// Tolerated exactly the way the tracked loader tolerates it, so this
			// fixture is the database a real install would have.
			if _, err := db.Exec(stmt); err != nil && !isDuplicateCollision(err) {
				t.Fatalf("%s: %v\nstatement: %s", filepath.Base(f), err, stmt)
			}
		}
	}
}

func openFileSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "rowid.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// tableSQL and indexNames are the two facts a rebuild must get right: the table has
// to end up declaring AUTOINCREMENT, and nothing else about its shape may change.
func tableSQL(t *testing.T, db *sql.DB, name string) string {
	t.Helper()
	var s string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&s); err != nil {
		t.Fatalf("read %s DDL: %v", name, err)
	}
	return s
}

func indexNames(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='index' AND tbl_name=? ORDER BY name`, table)
	if err != nil {
		t.Fatalf("index list for %s: %v", table, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("index scan: %v", err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("index rows: %v", err)
	}
	return out
}

func countRows(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", query, err)
	}
	return n
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %s: %v", query, err)
	}
}

// The failure this exists for: content was hard-deleted, so a tombstone still cites
// the id it had, and the next upload takes that same id on an old-shape database.
// The rebuild has to stop that, keep every row, and keep every column and index name.
func TestEnsureNoRowIDReuseRebuildsOldShapeDatabase(t *testing.T) {
	db := openFileSQLite(t)
	applyTreeWithoutAutoincrement(t, db)

	if strings.Contains(tableSQL(t, db, "textures"), "AUTOINCREMENT") {
		t.Fatal("fixture is not old-shape; the test would prove nothing")
	}
	mustExec(t, db, `INSERT INTO users (id, nickname, password) VALUES (1,'one','x'),(2,'two','x'),(3,'three','x')`)
	mustExec(t, db, `INSERT INTO textures (id, uid, type, hash) VALUES (1,1,'skin','a'),(2,1,'skin','b'),(3,1,'skin','c')`)
	// The product's own hard delete: the row goes, the tombstone keeps the id.
	mustExec(t, db, `DELETE FROM textures WHERE id = 3`)
	mustExec(t, db, `INSERT INTO texture_tombstones (texture_id, uid, type, hash, deleted_by, upload_at)
	                 VALUES (3, 1, 'skin', 'c', 1, '2026-01-01 00:00:00')`)

	beforeIdx := indexNames(t, db, "textures")
	if err := EnsureNoRowIDReuse(db, dialect.SQLite); err != nil {
		t.Fatalf("EnsureNoRowIDReuse: %v", err)
	}

	for _, name := range rowIDTables {
		if !strings.Contains(tableSQL(t, db, name), "AUTOINCREMENT") {
			t.Errorf("%s still lacks AUTOINCREMENT", name)
		}
	}
	if got := indexNames(t, db, "textures"); strings.Join(got, ",") != strings.Join(beforeIdx, ",") {
		t.Errorf("index set changed: after=%v before=%v", got, beforeIdx)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM textures`); n != 2 {
		t.Errorf("rows after rebuild = %d, want the 2 survivors", n)
	}
	var kept string
	if err := db.QueryRow(`SELECT hash FROM textures WHERE id = 2`).Scan(&kept); err != nil || kept != "b" {
		t.Errorf("row 2 = %q (%v), want hash b", kept, err)
	}

	// The next upload must land past the cited id 3, or its delete would hit the
	// tombstone primary key and its report history would be wrong.
	mustExec(t, db, `INSERT INTO textures (uid, type, hash) VALUES (1,'skin','d')`)
	var next int64
	if err := db.QueryRow(`SELECT id FROM textures WHERE hash = 'd'`).Scan(&next); err != nil {
		t.Fatalf("read new id: %v", err)
	}
	if next != 4 {
		t.Errorf("new texture id = %d, want 4: the counter must clear every id history still cites", next)
	}
	// And a second hard delete of that slot must actually work.
	mustExec(t, db, `INSERT INTO texture_tombstones (texture_id, uid, type, hash, deleted_by, upload_at)
	                 SELECT id, uid, type, hash, 1, '2026-01-02 00:00:00' FROM textures WHERE hash = 'd'`)
	mustExec(t, db, `DELETE FROM textures WHERE hash = 'd'`)

	// users keeps its own guarantee: after the rebuild the counter knows id 3.
	mustExec(t, db, `DELETE FROM users WHERE id = 3`)
	mustExec(t, db, `INSERT INTO users (nickname, password) VALUES ('fourth','x')`)
	var uid int64
	if err := db.QueryRow(`SELECT id FROM users WHERE nickname = 'fourth'`).Scan(&uid); err != nil {
		t.Fatalf("read new uid: %v", err)
	}
	if uid == 3 {
		t.Error("user id 3 was reused after the rebuild")
	}

	// Idempotent: a second run must be a no-op, not a second rebuild.
	snapshot := tableSQL(t, db, "textures")
	if err := EnsureNoRowIDReuse(db, dialect.SQLite); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if got := tableSQL(t, db, "textures"); got != snapshot {
		t.Errorf("second run changed the DDL:\n before %s\n after  %s", snapshot, got)
	}
}

// A table emptied before the fix has no counter of its own left, yet history still
// cites its ids. The counter has to be lifted past those references, and the
// already-rebuilt path has to do it too, since that is the state an upgraded
// database lands in.
func TestEnsureNoRowIDReuseLiftsTheCounterPastHistory(t *testing.T) {
	db := openFileSQLite(t)
	if err := ApplySQLDirTracked(db, trees, dialect.SQLite); err != nil {
		t.Fatalf("apply tree: %v", err)
	}
	mustExec(t, db, `INSERT INTO texture_tombstones (texture_id, uid, type, hash, deleted_by, upload_at)
	                 VALUES (7,1,'skin','old',1,'2026-01-01 00:00:00'),(9,1,'skin','older',1,'2026-01-01 00:00:00')`)
	if n := countRows(t, db, `SELECT COUNT(*) FROM textures`); n != 0 {
		t.Fatalf("fixture wants an empty textures table, got %d rows", n)
	}
	if err := EnsureNoRowIDReuse(db, dialect.SQLite); err != nil {
		t.Fatalf("EnsureNoRowIDReuse: %v", err)
	}
	mustExec(t, db, `INSERT INTO textures (uid, type, hash) VALUES (1,'skin','new')`)
	var id int64
	if err := db.QueryRow(`SELECT id FROM textures WHERE hash = 'new'`).Scan(&id); err != nil {
		t.Fatalf("read new id: %v", err)
	}
	if id <= 9 {
		t.Errorf("new id = %d, want past every id history still cites (9)", id)
	}
}

// A failure must leave the failing table alone and stop the run: a half-converted
// database is worse than an unconverted one, and tables later in the list must not
// have been touched.
func TestEnsureNoRowIDReuseStopsAtTheFirstFailure(t *testing.T) {
	db := openFileSQLite(t)
	applyTreeWithoutAutoincrement(t, db)
	mustExec(t, db, `INSERT INTO textures (uid, type, hash) VALUES (1,'skin','keepme')`)

	err := rebuildRowIDTables(context.Background(), db, []string{"textures", "no_such_table", "players"})
	if err == nil {
		t.Fatal("a missing table was accepted")
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM textures WHERE hash = 'keepme'`); n != 1 {
		t.Errorf("surviving row count = %d, want the seeded row intact", n)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE name LIKE '%_rowid_rebuild'`); n != 0 {
		t.Errorf("%d staging tables left behind", n)
	}
	if strings.Contains(tableSQL(t, db, "players"), "AUTOINCREMENT") {
		t.Error("players was rebuilt although an earlier table failed")
	}
}
