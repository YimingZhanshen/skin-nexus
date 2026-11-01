package migrate

import (
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"skin-nexus/server/internal/dialect"
)

// trees is the root the shipped DDL lives in (server/migrations).
var trees = filepath.Join("..", "..", "migrations")

// A migration tree is the ONLY place a table shape may diverge between engines,
// so the divergence has to be machine-checked: three hand-written trees drift
// silently, and the first symptom is a column that exists on MySQL and is NULL
// on SQLite forever. These tests compare the shapes without needing any server.

type shape struct {
	tables  map[string]bool
	columns map[string]bool // "table.column"
	indexes map[string]bool
}

var (
	reAlterTable  = regexp.MustCompile(`^ALTER TABLE\s+` + "`?" + `(\w+)`)
	reAddColumn   = regexp.MustCompile(`ADD COLUMN\s+` + "`?" + `(\w+)`)
	reCreateIndex = regexp.MustCompile(`^CREATE (UNIQUE )?INDEX (IF NOT EXISTS )?` + "`?" + `(\w+) ON ` + "`?" + `(\w+)`)
	reCreateTable = regexp.MustCompile(`^CREATE TABLE (IF NOT EXISTS )?` + "`?" + `(\w+)`)
)

func newShape() *shape {
	return &shape{map[string]bool{}, map[string]bool{}, map[string]bool{}}
}

// readShape walks one engine's tree and records every object the DDL creates.
func readShape(t *testing.T, eng dialect.Engine) *shape {
	t.Helper()
	s := newShape()
	dir := TreeDir(trees, eng)
	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no migration files under %s (err %v)", dir, err)
	}
	sort.Strings(files)
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for _, stmt := range SplitSQL(string(raw)) {
			// Comments are per-line: MySQL carries trailing notes inside its
			// CREATE TABLE clauses, and cutting at the first one would drop
			// the rest of the column list.
			oneLine := strings.Join(strings.Fields(stripComments(stmt)), " ")
			switch {
			case strings.HasPrefix(oneLine, "CREATE TABLE"):
				m := reCreateTable.FindStringSubmatch(oneLine)
				if m == nil {
					t.Fatalf("%s: unparsed CREATE TABLE: %s", filepath.Base(f), oneLine)
				}
				table := m[2]
				s.tables[table] = true
				cols, idx := parseCreateTable(oneLine)
				for _, col := range cols {
					s.columns[table+"."+col] = true
				}
				for _, name := range idx {
					s.indexes[name] = true
				}
			case strings.HasPrefix(oneLine, "ALTER TABLE"):
				m := reAlterTable.FindStringSubmatch(oneLine)
				if m == nil {
					t.Fatalf("%s: unparsed ALTER TABLE: %s", filepath.Base(f), oneLine)
				}
				// One ALTER can add several columns (MySQL merges them), so the
				// column names come from every ADD COLUMN in the statement.
				for _, cm := range reAddColumn.FindAllStringSubmatch(oneLine, -1) {
					s.columns[m[1]+"."+cm[1]] = true
				}
			case strings.HasPrefix(oneLine, "CREATE INDEX"), strings.HasPrefix(oneLine, "CREATE UNIQUE INDEX"):
				m := reCreateIndex.FindStringSubmatch(oneLine)
				if m == nil {
					t.Fatalf("%s: unparsed CREATE INDEX: %s", filepath.Base(f), oneLine)
				}
				s.indexes[m[3]] = true
			}
		}
	}
	return s
}

func stripComments(stmt string) string {
	lines := strings.Split(stmt, "\n")
	for i, l := range lines {
		if j := strings.Index(l, "--"); j >= 0 {
			// No DDL literal in these trees contains a double dash; the notes do.
			lines[i] = l[:j]
		}
	}
	return strings.Join(lines, "\n")
}

// parseCreateTable returns the declared columns of a CREATE TABLE and the names
// of any indexes spelled inside it — MySQL's inline KEY/UNIQUE KEY clauses,
// which the other two trees have to write as separate CREATE INDEX statements.
func parseCreateTable(oneLine string) (cols, idx []string) {
	open := strings.IndexByte(oneLine, '(')
	close := strings.LastIndexByte(oneLine, ')')
	if open < 0 || close < open {
		return nil, nil
	}
	depth := 0
	var part strings.Builder
	flush := func() {
		p := strings.TrimSpace(part.String())
		part.Reset()
		if p == "" {
			return
		}
		fields := strings.Fields(p)
		head := strings.ToUpper(fields[0])
		switch head {
		case "PRIMARY", "CONSTRAINT", "FOREIGN", "CHECK", "FULLTEXT":
			return
		case "UNIQUE":
			if len(fields) > 2 && strings.ToUpper(fields[1]) == "KEY" {
				idx = append(idx, strings.Trim(fields[2], "`"))
			}
			return
		case "KEY", "INDEX":
			if len(fields) > 1 {
				idx = append(idx, strings.Trim(fields[1], "`"))
			}
			return
		}
		cols = append(cols, strings.Trim(fields[0], "`"))
	}
	for _, r := range oneLine[open+1 : close] {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				flush()
				continue
			}
		}
		part.WriteRune(r)
	}
	flush()
	return cols, idx
}

func setDiff(a, b map[string]bool) []string {
	var out []string
	for k := range a {
		if !b[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func TestMigrationTreesAreParallel(t *testing.T) {
	ref := readShape(t, dialect.MySQL)
	if len(ref.tables) < 20 {
		t.Fatalf("mysql tree parsed %d tables; the extractor is broken", len(ref.tables))
	}
	for _, eng := range []dialect.Engine{dialect.SQLite, dialect.Postgres} {
		got := readShape(t, eng)
		if d := setDiff(ref.tables, got.tables); len(d) > 0 {
			t.Errorf("%s tree misses tables %v", eng, d)
		}
		if d := setDiff(got.tables, ref.tables); len(d) > 0 {
			t.Errorf("%s tree adds tables %v", eng, d)
		}
		if d := setDiff(ref.columns, got.columns); len(d) > 0 {
			t.Errorf("%s tree misses columns %v", eng, d)
		}
		if d := setDiff(got.columns, ref.columns); len(d) > 0 {
			t.Errorf("%s tree adds columns %v", eng, d)
		}
		if d := setDiff(ref.indexes, got.indexes); len(d) > 0 {
			t.Errorf("%s tree misses indexes %v", eng, d)
		}
		if d := setDiff(got.indexes, ref.indexes); len(d) > 0 {
			t.Errorf("%s tree adds indexes %v", eng, d)
		}
	}
}

// openSQLite pins a memory database to ONE connection: every connection to
// ":memory:" gets its own private database, so a pooled memory database splits
// in two and the migration appears to succeed against an empty schema.
func openSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func markerCount(t *testing.T, db *sql.DB) (int, string) {
	t.Helper()
	var n int
	var eng string
	if err := db.QueryRow(`SELECT COUNT(*), MAX(engine) FROM schema_migrations`).Scan(&n, &eng); err != nil {
		t.Fatalf("marker read: %v", err)
	}
	return n, eng
}

// TestSQLiteTreeBuildsAndMarks runs the SQLite tree for real — the parity test
// above proves the trees look alike, this one proves the statements execute.
func TestSQLiteTreeBuildsAndMarks(t *testing.T) {
	db := openSQLite(t)
	if err := ApplySQLDirTracked(db, trees, dialect.SQLite); err != nil {
		t.Fatalf("apply sqlite tree: %v", err)
	}
	n, eng := markerCount(t, db)
	if eng != string(dialect.SQLite) {
		t.Errorf("marker engine = %q, want sqlite", eng)
	}
	ref := readShape(t, dialect.MySQL)
	if n != len(filesIn(t, dialect.SQLite)) {
		t.Errorf("markers = %d, want %d files", n, len(filesIn(t, dialect.SQLite)))
	}
	// every table the tree claims to create must actually be queryable
	for table := range ref.tables {
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
			t.Errorf("table %s not usable after migration: %v", table, err)
		}
	}
	// a second boot must be a pure no-op
	if err := ApplySQLDirTracked(db, trees, dialect.SQLite); err != nil {
		t.Fatalf("re-apply sqlite tree: %v", err)
	}
	if n2, _ := markerCount(t, db); n2 != n {
		t.Errorf("re-apply wrote %d markers, want the same %d", n2, n)
	}
}

// TestSQLiteTreeRerunToleratesCollisions simulates a pre-tracking database:
// the schema is already there, the markers are not, so every statement of every
// file runs again and must be swallowed as a duplicate-object collision.
func TestSQLiteTreeRerunToleratesCollisions(t *testing.T) {
	db := openSQLite(t)
	if err := ApplySQLDirTracked(db, trees, dialect.SQLite); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations`); err != nil {
		t.Fatal(err)
	}
	if err := ApplySQLDirTracked(db, trees, dialect.SQLite); err != nil {
		t.Fatalf("untracked re-apply must tolerate every collision: %v", err)
	}
	if n, _ := markerCount(t, db); n != len(filesIn(t, dialect.SQLite)) {
		t.Errorf("markers after re-apply = %d, want %d", n, len(filesIn(t, dialect.SQLite)))
	}
}

// TestMarkerTableUpgradesWithoutEngineColumn covers the real upgrade path: a
// database whose markers were written before the engine column existed. Every
// file is already marked, so the only work left is the marker table itself.
func TestMarkerTableUpgradesWithoutEngineColumn(t *testing.T) {
	db := openSQLite(t)
	if _, err := db.Exec(`CREATE TABLE schema_migrations (file VARCHAR(191) NOT NULL, applied_at DATETIME, PRIMARY KEY (file))`); err != nil {
		t.Fatal(err)
	}
	files := filesIn(t, dialect.SQLite)
	for _, f := range files {
		if _, err := db.Exec(`INSERT INTO schema_migrations (file) VALUES (?)`, filepath.Base(f)); err != nil {
			t.Fatal(err)
		}
	}
	if err := ApplySQLDirTracked(db, trees, dialect.SQLite); err != nil {
		t.Fatalf("upgrade run on a marker table without engine: %v", err)
	}
	var backfilled string
	if err := db.QueryRow(`SELECT engine FROM schema_migrations WHERE file = '0001_init.sql'`).Scan(&backfilled); err != nil {
		t.Fatal(err)
	}
	if backfilled != string(dialect.SQLite) {
		t.Errorf("pre-existing marker backfilled as %q, want sqlite", backfilled)
	}
	if n, _ := markerCount(t, db); n != len(files) {
		t.Errorf("markers after upgrade = %d, want %d", n, len(files))
	}
}

// TestWrongEngineTreeIsRejected is the tripwire: pointing a foreign tree at a
// migrated database must fail before it starts executing DDL against it.
func TestWrongEngineTreeIsRejected(t *testing.T) {
	db := openSQLite(t)
	if err := ApplySQLDirTracked(db, trees, dialect.SQLite); err != nil {
		t.Fatal(err)
	}
	err := ApplySQLDirTracked(db, trees, dialect.MySQL)
	if err == nil || !strings.Contains(err.Error(), "migrated as") {
		t.Fatalf("mysql tree against a sqlite-migrated database: got %v, want the engine tripwire", err)
	}
}

func filesIn(t *testing.T, eng dialect.Engine) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(TreeDir(trees, eng), "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	return files
}
