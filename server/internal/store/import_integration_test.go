//go:build integration

// import_integration_test.go: drives migrate.RunImport against legacy-shaped
// shadow tables (prefix `legacy_`) inside the disposable test DB — the same run
// pattern the cutover uses with prefix `legacy.`.
//
// The fixture exists in three DDL spellings because a legacy blessing site can
// sit on any of them (the shipped compose image is SQLite, forks run PostgreSQL)
// and the importer's whole promise is that the data crosses that boundary.
package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"skin-nexus/server/internal/dialect"
	"skin-nexus/server/internal/migrate"
)

const legacyDDLMySQL = `
DROP TABLE IF EXISTS legacy_users;
DROP TABLE IF EXISTS legacy_players;
DROP TABLE IF EXISTS legacy_textures;
DROP TABLE IF EXISTS legacy_uuid;
DROP TABLE IF EXISTS legacy_options;
DROP TABLE IF EXISTS legacy_user_closet;
DROP TABLE IF EXISTS legacy_invitation_codes;
CREATE TABLE legacy_users (
  uid INT UNSIGNED NOT NULL AUTO_INCREMENT,
  email VARCHAR(100) NOT NULL, nickname VARCHAR(50) NOT NULL DEFAULT '',
  score INT NOT NULL, avatar INT NOT NULL DEFAULT 0, password VARCHAR(255) NOT NULL,
  ip VARCHAR(45) NOT NULL, permission INT NOT NULL DEFAULT 0,
  last_sign_at DATETIME NOT NULL, register_at DATETIME NOT NULL,
  verified TINYINT(1) NOT NULL DEFAULT 0,
  verification_token VARCHAR(255) NOT NULL DEFAULT '',
  is_dark_mode TINYINT(1) NOT NULL DEFAULT 0, locale VARCHAR(255) NULL,
  PRIMARY KEY (uid)) DEFAULT CHARSET=utf8mb4;
CREATE TABLE legacy_players (
  pid INT UNSIGNED NOT NULL AUTO_INCREMENT, uid INT NOT NULL,
  name VARCHAR(50) NOT NULL, tid_cape INT NOT NULL DEFAULT 0,
  last_modified DATETIME NOT NULL, tid_skin INT NOT NULL DEFAULT -1,
  PRIMARY KEY (pid)) DEFAULT CHARSET=utf8mb4;
CREATE TABLE legacy_textures (
  tid INT UNSIGNED NOT NULL AUTO_INCREMENT, name VARCHAR(50) NOT NULL,
  type VARCHAR(10) NOT NULL, hash VARCHAR(64) NOT NULL, size INT NOT NULL,
  uploader INT NOT NULL, public TINYINT(4) NOT NULL, upload_at DATETIME NOT NULL,
  likes INT UNSIGNED NOT NULL DEFAULT 0, PRIMARY KEY (tid)) DEFAULT CHARSET=utf8mb4;
CREATE TABLE legacy_uuid (
  id INT UNSIGNED NOT NULL AUTO_INCREMENT, pid INT UNSIGNED NULL,
  name VARCHAR(255) NOT NULL, uuid VARCHAR(255) NOT NULL,
  PRIMARY KEY (id)) DEFAULT CHARSET=utf8mb4;
CREATE TABLE legacy_options (
  id INT UNSIGNED NOT NULL AUTO_INCREMENT, option_name VARCHAR(50) NOT NULL,
  option_value LONGTEXT NOT NULL, PRIMARY KEY (id)) DEFAULT CHARSET=utf8mb4;
CREATE TABLE legacy_user_closet (user_uid INT NOT NULL, texture_tid INT NOT NULL, item_name TEXT NULL) DEFAULT CHARSET=utf8mb4;
CREATE TABLE legacy_invitation_codes (
  id INT UNSIGNED NOT NULL AUTO_INCREMENT, code VARCHAR(255) NOT NULL,
  generated_at DATETIME NOT NULL, used_by INT NOT NULL DEFAULT 0,
  used_at DATETIME NULL, description VARCHAR(255) NULL, PRIMARY KEY (id)) DEFAULT CHARSET=utf8mb4;
`

// legacyDDLSQLite mirrors blessing's Laravel schema as a SQLite site would hold
// it: DATETIME columns (the driver only parses a timestamp-ish declared type)
// and INTEGER primary keys, ids supplied by the fixture.
const legacyDDLSQLite = `
DROP TABLE IF EXISTS legacy_users;
DROP TABLE IF EXISTS legacy_players;
DROP TABLE IF EXISTS legacy_textures;
DROP TABLE IF EXISTS legacy_uuid;
DROP TABLE IF EXISTS legacy_options;
DROP TABLE IF EXISTS legacy_user_closet;
DROP TABLE IF EXISTS legacy_invitation_codes;
CREATE TABLE legacy_users (
  uid INTEGER PRIMARY KEY,
  email VARCHAR(100) NOT NULL, nickname VARCHAR(50) NOT NULL DEFAULT '',
  score INTEGER NOT NULL, avatar INTEGER NOT NULL DEFAULT 0, password VARCHAR(255) NOT NULL,
  ip VARCHAR(45) NOT NULL, permission INTEGER NOT NULL DEFAULT 0,
  last_sign_at DATETIME NOT NULL, register_at DATETIME NOT NULL,
  verified INTEGER NOT NULL DEFAULT 0,
  verification_token VARCHAR(255) NOT NULL DEFAULT '',
  is_dark_mode INTEGER NOT NULL DEFAULT 0, locale VARCHAR(255) NULL);
CREATE TABLE legacy_players (
  pid INTEGER PRIMARY KEY, uid INTEGER NOT NULL,
  name VARCHAR(50) NOT NULL, tid_cape INTEGER NOT NULL DEFAULT 0,
  last_modified DATETIME NOT NULL, tid_skin INTEGER NOT NULL DEFAULT -1);
CREATE TABLE legacy_textures (
  tid INTEGER PRIMARY KEY, name VARCHAR(50) NOT NULL,
  type VARCHAR(10) NOT NULL, hash VARCHAR(64) NOT NULL, size INTEGER NOT NULL,
  uploader INTEGER NOT NULL, public INTEGER NOT NULL, upload_at DATETIME NOT NULL,
  likes INTEGER NOT NULL DEFAULT 0);
CREATE TABLE legacy_uuid (
  id INTEGER PRIMARY KEY, pid INTEGER NULL,
  name VARCHAR(255) NOT NULL, uuid VARCHAR(255) NOT NULL);
CREATE TABLE legacy_options (
  id INTEGER PRIMARY KEY, option_name VARCHAR(50) NOT NULL, option_value TEXT NOT NULL);
CREATE TABLE legacy_user_closet (user_uid INTEGER NOT NULL, texture_tid INTEGER NOT NULL, item_name TEXT NULL);
CREATE TABLE legacy_invitation_codes (
  id INTEGER PRIMARY KEY, code VARCHAR(255) NOT NULL,
  generated_at DATETIME NOT NULL, used_by INTEGER NOT NULL DEFAULT 0,
  used_at DATETIME NULL, description VARCHAR(255) NULL);
`

// legacyDDLPostgres uses TIMESTAMPTZ so the instants arrive unambiguously, and
// plain integer primary keys because the fixture supplies ids the way a real
// legacy dump does.
const legacyDDLPostgres = `
DROP TABLE IF EXISTS legacy_users;
DROP TABLE IF EXISTS legacy_players;
DROP TABLE IF EXISTS legacy_textures;
DROP TABLE IF EXISTS legacy_uuid;
DROP TABLE IF EXISTS legacy_options;
DROP TABLE IF EXISTS legacy_user_closet;
DROP TABLE IF EXISTS legacy_invitation_codes;
CREATE TABLE legacy_users (
  uid INTEGER PRIMARY KEY,
  email VARCHAR(100) NOT NULL, nickname VARCHAR(50) NOT NULL DEFAULT '',
  score INTEGER NOT NULL, avatar INTEGER NOT NULL DEFAULT 0, password VARCHAR(255) NOT NULL,
  ip VARCHAR(45) NOT NULL, permission INTEGER NOT NULL DEFAULT 0,
  last_sign_at TIMESTAMPTZ NOT NULL, register_at TIMESTAMPTZ NOT NULL,
  verified SMALLINT NOT NULL DEFAULT 0,
  verification_token VARCHAR(255) NOT NULL DEFAULT '',
  is_dark_mode SMALLINT NOT NULL DEFAULT 0, locale VARCHAR(255) NULL);
CREATE TABLE legacy_players (
  pid INTEGER PRIMARY KEY, uid INTEGER NOT NULL,
  name VARCHAR(50) NOT NULL, tid_cape INTEGER NOT NULL DEFAULT 0,
  last_modified TIMESTAMPTZ NOT NULL, tid_skin INTEGER NOT NULL DEFAULT -1);
CREATE TABLE legacy_textures (
  tid INTEGER PRIMARY KEY, name VARCHAR(50) NOT NULL,
  type VARCHAR(10) NOT NULL, hash VARCHAR(64) NOT NULL, size INTEGER NOT NULL,
  uploader INTEGER NOT NULL, public SMALLINT NOT NULL, upload_at TIMESTAMPTZ NOT NULL,
  likes INTEGER NOT NULL DEFAULT 0);
CREATE TABLE legacy_uuid (
  id INTEGER PRIMARY KEY, pid INTEGER NULL,
  name VARCHAR(255) NOT NULL, uuid VARCHAR(255) NOT NULL);
CREATE TABLE legacy_options (
  id INTEGER PRIMARY KEY, option_name VARCHAR(50) NOT NULL, option_value TEXT NOT NULL);
CREATE TABLE legacy_user_closet (user_uid INTEGER NOT NULL, texture_tid INTEGER NOT NULL, item_name TEXT NULL);
CREATE TABLE legacy_invitation_codes (
  id INTEGER PRIMARY KEY, code VARCHAR(255) NOT NULL,
  generated_at TIMESTAMPTZ NOT NULL, used_by INTEGER NOT NULL DEFAULT 0,
  used_at TIMESTAMPTZ NULL, description VARCHAR(255) NULL);
`

func legacyDDLFor(eng dialect.Engine) string {
	switch eng {
	case dialect.SQLite:
		return legacyDDLSQLite
	case dialect.Postgres:
		return legacyDDLPostgres
	}
	return legacyDDLMySQL
}

// seedLegacy builds the fixture and one real PHP-style bcrypt credential, so a
// caller can assert the imported accounts still log in afterwards.
func seedLegacy(t *testing.T, m *Mysql, phpHash string) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range splitDDL(legacyDDLFor(m.Engine())) {
		if _, err := m.db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("legacy ddl: %v", err)
		}
	}
	exec := func(q string, a ...any) {
		t.Helper()
		if _, err := m.db.ExecContext(ctx, q, a...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	exec(`INSERT INTO legacy_users (uid,email,nickname,score,avatar,password,ip,permission,last_sign_at,register_at,verified,verification_token,is_dark_mode,locale) VALUES
		(1,'a@x.com','alice',10,7,?, '1.2.3.4',0,'2026-01-01','2025-06-01',1,'tok',1,'zh-CN'),
		(2,'b@x.com','bob',0,0,'$2y$10$zyxwvutsrqponmlkjihgfe','5.6.7.8',1,'2026-01-02','2025-07-01',0,'',0,NULL)`, phpHash)
	exec(`INSERT INTO legacy_players (pid,uid,name,tid_cape,last_modified,tid_skin) VALUES
		(10,1,'Alice',0,'2026-02-01',5), (11,2,'Bob',6,'2026-02-02',-1)`)
	exec(`INSERT INTO legacy_textures (tid,name,type,hash,size,uploader,public,upload_at) VALUES
		(5,'old steve','steve','5a5a5a',100,1,1,'2025-08-01'),
		(6,'old cape','cape','6b6b6b',50,2,0,'2025-08-02'),
		(7,'old alex','alex','7c7c7c',120,1,0,'2025-08-03')`)
	exec(`INSERT INTO legacy_uuid (id,pid,name,uuid) VALUES
		(1,10,'Alice','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'),
		(2,10,'Alice','bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'),
		(3,11,'Bob','cccccccccccccccccccccccccccccccc'),
		(4,NULL,'Ghost','dddddddddddddddddddddddddddddddd')`)
	exec(`INSERT INTO legacy_options (id,option_name,option_value) VALUES (1,'site_name','老站名'),(2,'ygg_uuid_algorithm','v3')`)
	exec(`INSERT INTO legacy_user_closet (user_uid,texture_tid,item_name) VALUES (1,5,'我的皮肤'),(1,5,'我的皮肤'),(2,6,NULL)`)
	exec(`INSERT INTO legacy_invitation_codes (id,code,generated_at,used_by,used_at,description) VALUES
		(1,'aaaabbbbccccddddeeeeffff00001111','2025-09-01',0,NULL,'张三 20210001'),
		(2,'11112222333344445555666677778888','2025-09-02',1,'2025-09-03','李四 20210002')`)
}

func phpBcryptHash(t *testing.T) string {
	t.Helper()
	raw, err := bcrypt.GenerateFromPassword([]byte("s3cret-pw-x"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return "$2y$" + string(raw)[4:]
}

func TestIntegrationImport(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	phpHash := phpBcryptHash(t)
	seedLegacy(t, m, phpHash)

	opt := migrate.ImportOptions{Source: m.DBX(), Target: m.DBX(), SourcePrefix: "legacy_"}

	// dry-run: counts only, nothing written
	dry, err := migrate.RunImport(ctx, migrate.ImportOptions{
		Source: m.DBX(), Target: m.DBX(), SourcePrefix: "legacy_", DryRun: true,
	})
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if dry.Tables["users"].Selected != 2 || dry.Tables["users"].Inserted != 0 {
		t.Fatalf("dry-run users = %+v", dry.Tables["users"])
	}
	var n int
	if err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("dry-run must not write: users=%d %v", n, err)
	}

	// real run
	rep, err := migrate.RunImport(ctx, opt)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if rep.Tables["users"].Inserted != 2 || rep.Tables["players"].Inserted != 2 ||
		rep.Tables["textures"].Inserted != 3 || rep.Tables["invitation_codes"].Inserted != 2 {
		t.Fatalf("import counts = %+v", rep.Tables)
	}
	if rep.UUID.Output != 2 || rep.UUID.DupNameDropped != 1 || rep.UUID.OrphansDropped != 1 {
		t.Fatalf("uuid clean = %+v", rep.UUID)
	}

	// users: ID preserved, legacy bcrypt marker, avatar 0->NULL, verified carried.
	// `CASE WHEN … IS NULL` rather than a bare `IS NULL`: MySQL answers 0/1,
	// PostgreSQL answers a boolean, and the projection must scan the same way.
	var algo string
	var avatarNull, dark, verified, score int
	var locale string
	if err := m.db.QueryRowContext(ctx,
		`SELECT password_algo, CASE WHEN avatar_tid IS NULL THEN 1 ELSE 0 END,
		        is_dark_mode, email_verified, score, locale
		 FROM users WHERE id = 1`).
		Scan(&algo, &avatarNull, &dark, &verified, &score, &locale); err != nil {
		t.Fatalf("user1: %v", err)
	}
	if algo != "legacy:bcrypt" || avatarNull != 0 || dark != 1 || verified != 1 || score != 10 || locale != "zh-CN" {
		t.Fatalf("user1 mapping: %s %d %d %d %d %s", algo, avatarNull, dark, verified, score, locale)
	}
	if err := m.db.QueryRowContext(ctx,
		`SELECT CASE WHEN avatar_tid IS NULL THEN 1 ELSE 0 END FROM users WHERE id = 2`).
		Scan(&avatarNull); err != nil || avatarNull != 1 {
		t.Fatalf("user2 avatar must be NULL: %d %v", avatarNull, err)
	}

	// Credential compatibility (user priority 2026-10-23): a real PHP-style
	// $2y$ bcrypt row verifies through store.VerifyPassword with NO legacy
	// salt chain mounted (m.legacy == nil here) — the PHC short-circuit.
	if ok, err := m.VerifyPassword(ctx, 1, "s3cret-pw-x"); err != nil || !ok {
		t.Fatalf("legacy bcrypt verify ok=%v err=%v", ok, err)
	}
	if ok, err := m.VerifyPassword(ctx, 1, "wrong-pass"); err != nil || ok {
		t.Fatalf("wrong password must fail: ok=%v err=%v", ok, err)
	}
	var algoMark string
	if err := m.db.QueryRowContext(ctx, `SELECT password_algo FROM users WHERE id = 1`).
		Scan(&algoMark); err != nil || algoMark != "legacy:bcrypt" {
		t.Fatalf("algo marker = %q %v", algoMark, err)
	}

	// players: -1/0 unset normalized to NULL, real ids kept
	var skinNull, capeNull, capeVal int
	if err := m.db.QueryRowContext(ctx,
		`SELECT CASE WHEN tid_skin IS NULL THEN 1 ELSE 0 END,
		        CASE WHEN tid_cape IS NULL THEN 1 ELSE 0 END, tid_cape
		 FROM players WHERE id = 11`).
		Scan(&skinNull, &capeNull, &capeVal); err != nil || skinNull != 1 || capeNull != 0 || capeVal != 6 {
		t.Fatalf("player11 mapping: skinNull=%d capeNull=%d cape=%d err=%v", skinNull, capeNull, capeVal, err)
	}
	var skinVal int
	if err := m.db.QueryRowContext(ctx,
		`SELECT tid_skin, CASE WHEN tid_cape IS NULL THEN 1 ELSE 0 END FROM players WHERE id = 10`).
		Scan(&skinVal, &capeNull); err != nil || skinVal != 5 || capeNull != 1 {
		t.Fatalf("player10 mapping: skin=%d capeNull=%d err=%v", skinVal, capeNull, err)
	}

	// textures: steve/alex/cape -> type+model, public carried
	var typ, model string
	var pub int
	if err := m.db.QueryRowContext(ctx,
		`SELECT type, model, public FROM textures WHERE id = 7`).
		Scan(&typ, &model, &pub); err != nil || typ != "skin" || model != "slim" || pub != 0 {
		t.Fatalf("texture7 alex->skin/slim/private: %s %s %d %v", typ, model, pub, err)
	}

	// closet dedup: 3 selected -> 2 inserted (identical dup collapsed)
	if st := rep.Tables["user_closet"]; st.Inserted != 2 || st.Skipped != 1 {
		t.Fatalf("closet dedup = %+v", st)
	}

	// options carried
	var v string
	if err := m.db.QueryRowContext(ctx, `SELECT v FROM options WHERE k = 'ygg_uuid_algorithm'`).
		Scan(&v); err != nil || v != "v3" {
		t.Fatalf("options carry: %q %v", v, err)
	}

	// idempotent rerun: nothing new inserted (closet NOT EXISTS, rest PK)
	rep2, err := migrate.RunImport(ctx, opt)
	if err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if rep2.Tables["users"].Inserted != 0 || rep2.Tables["players"].Inserted != 0 ||
		rep2.Tables["textures"].Inserted != 0 || rep2.Tables["user_closet"].Inserted != 0 ||
		rep2.Tables["invitation_codes"].Inserted != 0 {
		t.Fatalf("rerun must be idempotent: %+v", rep2.Tables)
	}

	// Registration after cutover must not collide with imported ids. This is
	// the PostgreSQL sequence resync: PG leaves the counter at 1 when rows
	// arrive with explicit ids, while MySQL and SQLite derive it from the data.
	newUID, err := m.CreateUser(ctx, "cutover@example.com", "cutover", "x", 3, "")
	if err != nil {
		t.Fatalf("register after import (sequence resync?): %v", err)
	}
	if newUID <= 2 {
		t.Fatalf("new user got id %d, must be past the imported 1 and 2", newUID)
	}
}

// TestIntegrationImportToSQLiteTarget is the cutover in one sentence: a site on
// any engine becomes the source, and a brand-new SQLite file is the target.
// Nothing else in the suite proves that the data crosses the engine boundary —
// every other import test keeps source and target on one database.
func TestIntegrationImportToSQLiteTarget(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	phpHash := phpBcryptHash(t)
	seedLegacy(t, m, phpHash)

	targetPath := filepath.Join(t.TempDir(), "cutover.db")
	target, err := Open("sqlite", targetPath, nil)
	if err != nil {
		t.Fatalf("open sqlite target: %v", err)
	}
	t.Cleanup(func() { _ = target.Close() })
	if err := migrate.ApplySQLDirTracked(target.DB(), filepath.Join("..", "..", "migrations"), dialect.SQLite); err != nil {
		t.Fatalf("migrate sqlite target: %v", err)
	}

	rep, err := migrate.RunImport(ctx, migrate.ImportOptions{
		Source: m.DBX(), Target: target.DBX(), SourcePrefix: "legacy_",
	})
	if err != nil {
		t.Fatalf("cross-engine import: %v", err)
	}
	if rep.Tables["users"].Inserted != 2 || rep.Tables["textures"].Inserted != 3 {
		t.Fatalf("cross-engine counts = %+v", rep.Tables)
	}

	// The credential has to survive the trip: the hash text moves verbatim and
	// the classification marker tells the new site how to check it.
	if ok, err := target.VerifyPassword(ctx, 1, "s3cret-pw-x"); err != nil || !ok {
		t.Fatalf("legacy login on the sqlite site: ok=%v err=%v", ok, err)
	}
	var algo string
	if err := target.DB().QueryRow(`SELECT password_algo FROM users WHERE id = 1`).Scan(&algo); err != nil {
		t.Fatal(err)
	}
	if algo != "legacy:bcrypt" {
		t.Errorf("imported algo = %q, want legacy:bcrypt", algo)
	}
	// And the fresh site keeps working on its own engine afterwards.
	uid, err := target.CreateUser(ctx, "fresh@example.com", "fresh", "x", 3, "")
	if err != nil || uid != 3 {
		t.Fatalf("registration on the cutover site = %d, %v", uid, err)
	}
}

// TestLegacyDDLVariantsAgree keeps the three fixture spellings honest: a fixture
// that quietly drops a column would let an import "pass" while reading nothing.
func TestLegacyDDLVariantsAgree(t *testing.T) {
	want := map[string][]string{
		"legacy_users":            {"uid", "email", "nickname", "score", "avatar", "password", "ip", "permission", "last_sign_at", "register_at", "verified", "verification_token", "is_dark_mode", "locale"},
		"legacy_players":          {"pid", "uid", "name", "tid_cape", "last_modified", "tid_skin"},
		"legacy_textures":         {"tid", "name", "type", "hash", "size", "uploader", "public", "upload_at", "likes"},
		"legacy_uuid":             {"id", "pid", "name", "uuid"},
		"legacy_options":          {"id", "option_name", "option_value"},
		"legacy_user_closet":      {"user_uid", "texture_tid", "item_name"},
		"legacy_invitation_codes": {"id", "code", "generated_at", "used_by", "used_at", "description"},
	}
	for _, eng := range []dialect.Engine{dialect.MySQL, dialect.SQLite, dialect.Postgres} {
		got := fixtureColumns(legacyDDLFor(eng))
		for table, cols := range want {
			have := got[table]
			if strings.Join(have, ",") != strings.Join(cols, ",") {
				t.Errorf("%s fixture %s columns = %v, want %v", eng, table, have, cols)
			}
		}
	}
}

// fixtureColumns reads the declared columns out of a CREATE TABLE script. The
// fixture packs several columns per line, so the split is on top-level commas
// rather than on lines.
func fixtureColumns(ddl string) map[string][]string {
	out := map[string][]string{}
	for _, stmt := range splitDDL(ddl) {
		head := strings.Fields(stmt)
		if len(head) < 3 || !strings.EqualFold(head[0], "CREATE") || !strings.EqualFold(head[1], "TABLE") {
			continue
		}
		table := head[2]
		open := strings.Index(stmt, "(")
		close := strings.LastIndex(stmt, ")")
		if open < 0 || close < open {
			continue
		}
		depth := 0
		var part strings.Builder
		add := func() {
			p := strings.Join(strings.Fields(part.String()), " ")
			part.Reset()
			if p == "" {
				return
			}
			switch strings.ToUpper(strings.Fields(p)[0]) {
			case "PRIMARY", "UNIQUE", "KEY", "CONSTRAINT", "FOREIGN", "CHECK":
				return
			}
			out[table] = append(out[table], strings.Trim(strings.Fields(p)[0], "`"))
		}
		for _, r := range stmt[open+1 : close] {
			switch r {
			case '(':
				depth++
			case ')':
				depth--
			case ',':
				if depth == 0 {
					add()
					continue
				}
			}
			part.WriteRune(r)
		}
		add()
	}
	return out
}

// splitDDL breaks a legacy DDL script into single statements (test helper).
func splitDDL(s string) []string {
	var out []string
	for _, line := range strings.Split(s, ";") {
		if t := strings.TrimSpace(line); t != "" {
			out = append(out, t)
		}
	}
	return out
}
