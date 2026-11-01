//go:build integration

// import_integration_test.go: drives migrate.RunImport against legacy-shaped
// shadow tables (prefix `legacy_`) inside the disposable test DB — the same
// run pattern the cutover uses with prefix `legacy.`.
package store

import (
	"context"
	"strings"
	"testing"

	"skin-nexus/server/internal/migrate"

	"golang.org/x/crypto/bcrypt"
)

const legacyDDL = `
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

func TestIntegrationImport(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	for _, stmt := range splitDDL(legacyDDL) {
		if _, err := m.db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("legacy ddl: %v", err)
		}
	}

	// seed legacy-shaped data: 2 users (bcrypt markers), 2 players (one unset
	// skin/cape), 3 textures (steve/alex/cape), uuid rows exercising the
	// five-step clean (dup name + orphan), options, closet (dup row), codes.
	exec := func(q string, a ...any) {
		t.Helper()
		if _, err := m.db.ExecContext(ctx, q, a...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	exec(`INSERT INTO legacy_users (uid,email,nickname,score,avatar,password,ip,permission,last_sign_at,register_at,verified,verification_token,is_dark_mode,locale) VALUES
		(1,'a@x.com','alice',10,7,'$2y$10$abcdefghijklmnopqrstuv','1.2.3.4',0,'2026-01-01','2025-06-01',1,'tok',1,'zh-CN'),
		(2,'b@x.com','bob',0,0,'$2y$10$zyxwvutsrqponmlkjihgfe','5.6.7.8',1,'2026-01-02','2025-07-01',0,'',0,NULL)`)
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

	opt := migrate.ImportOptions{Source: m.DB(), Target: m.DB(), SourcePrefix: "legacy_"}

	// dry-run: counts only, nothing written
	dry, err := migrate.RunImport(ctx, migrate.ImportOptions{
		Source: m.DB(), Target: m.DB(), SourcePrefix: "legacy_", DryRun: true,
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

	// users: ID preserved, legacy bcrypt marker, avatar 0->NULL, verified carried
	var algo string
	var avatarNull, dark, verified, score int
	var locale string
	if err := m.db.QueryRowContext(ctx,
		`SELECT password_algo, avatar_tid IS NULL, is_dark_mode, email_verified, score, locale FROM users WHERE id = 1`).
		Scan(&algo, &avatarNull, &dark, &verified, &score, &locale); err != nil {
		t.Fatalf("user1: %v", err)
	}
	if algo != "legacy:bcrypt" || avatarNull != 0 || dark != 1 || verified != 1 || score != 10 || locale != "zh-CN" {
		t.Fatalf("user1 mapping: %s %d %d %d %d %s", algo, avatarNull, dark, verified, score, locale)
	}
	if err := m.db.QueryRowContext(ctx, `SELECT avatar_tid IS NULL FROM users WHERE id = 2`).Scan(&avatarNull); err != nil || avatarNull != 1 {
		t.Fatalf("user2 avatar must be NULL: %d %v", avatarNull, err)
	}

	// Credential compatibility (user priority 2026-10-23): a real PHP-style
	// $2y$ bcrypt row verifies through store.VerifyPassword with NO legacy
	// salt chain mounted (m.legacy == nil here) — the PHC short-circuit.
	raw, err := bcrypt.GenerateFromPassword([]byte("s3cret-pw-x"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	phpHash := "$2y$" + string(raw)[4:]
	if _, err := m.db.ExecContext(ctx,
		`UPDATE users SET password = ?, password_algo = 'legacy:bcrypt' WHERE id = 1`, phpHash); err != nil {
		t.Fatal(err)
	}
	if ok, err := m.VerifyPassword(ctx, 1, "s3cret-pw-x"); err != nil || !ok {
		t.Fatalf("legacy bcrypt verify ok=%v err=%v", ok, err)
	}
	if ok, err := m.VerifyPassword(ctx, 1, "wrong-pass"); err != nil || ok {
		t.Fatalf("wrong password must fail: ok=%v err=%v", ok, err)
	}
	var algoMark string
	if err := m.db.QueryRowContext(ctx, `SELECT password_algo FROM users WHERE id = 1`).Scan(&algoMark); err != nil || algoMark != "legacy:bcrypt" {
		t.Fatalf("algo marker = %q %v", algoMark, err)
	}

	// players: -1/0 unset normalized to NULL, real ids kept
	var skinNull, capeNull, capeVal int
	if err := m.db.QueryRowContext(ctx,
		`SELECT tid_skin IS NULL, tid_cape IS NULL, tid_cape FROM players WHERE id = 11`).
		Scan(&skinNull, &capeNull, &capeVal); err != nil || skinNull != 1 || capeNull != 0 || capeVal != 6 {
		t.Fatalf("player11 mapping: skinNull=%d capeNull=%d cape=%d err=%v", skinNull, capeNull, capeVal, err)
	}
	if err := m.db.QueryRowContext(ctx,
		`SELECT tid_skin, tid_cape IS NULL FROM players WHERE id = 10`).
		Scan(&capeVal, &capeNull); err != nil || capeVal != 5 || capeNull != 1 {
		t.Fatalf("player10 mapping: skin=%d capeNull=%d err=%v", capeVal, capeNull, err)
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
	if err := m.db.QueryRowContext(ctx, `SELECT v FROM options WHERE k = 'ygg_uuid_algorithm'`).Scan(&v); err != nil || v != "v3" {
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
