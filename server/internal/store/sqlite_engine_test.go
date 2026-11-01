// Engine test: the store's swept SQL run against a real SQLite database.
//
// The MySQL integration suite proves these queries on one engine; this file is
// what makes the multi-engine claim more than a migration tree plus a promise.
// It has no build tag and needs no server, so every CI run executes it.
package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"skin-nexus/server/internal/dialect"
	"skin-nexus/server/internal/migrate"
)

func openSQLiteStore(t *testing.T) *Mysql {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "engine.db")
	st, err := Open("sqlite", dsn, nil)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := migrate.ApplySQLDirTracked(st.DB(), filepath.Join("..", "..", "migrations"), dialect.SQLite); err != nil {
		t.Fatalf("apply sqlite tree: %v", err)
	}
	if got := st.Engine(); got != dialect.SQLite {
		t.Fatalf("engine = %q, want sqlite", got)
	}
	return st
}

func TestSQLiteEngineUsersSessionsTextures(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()

	uid, err := st.CreateUser(ctx, "Player@Example.COM", "Notch", "x", 3, "127.0.0.1")
	if err != nil || uid <= 0 {
		t.Fatalf("CreateUser = %d, %v", uid, err)
	}
	// The case-folding uniques came out of MySQL's collation; on SQLite they are
	// LOWER() expression indexes, so the lookup has to fold too.
	if got, err := st.UserByIdentifier(ctx, "player@example.com"); err != nil || got.ID != uid {
		t.Fatalf("UserByIdentifier(case-folded) = %+v, %v", got, err)
	}
	if _, err := st.CreateUser(ctx, "other@example.com", "NOTCH", "x", 3, ""); err == nil {
		t.Fatal("nickname differing only in case must collide on every engine (MySQL folded it in the column collation, SQLite/PG in the LOWER() index)")
	}

	// Session lifetime is compared against the Go clock the store bound.
	if err := st.CreateSession(ctx, "tok-live", uid, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateSession(ctx, "tok-dead", uid, time.Now().UTC().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got, err := st.SessionUser(ctx, "tok-live"); err != nil || got != uid {
		t.Fatalf("SessionUser(live) = %d, %v", got, err)
	}
	if _, err := st.SessionUser(ctx, "tok-dead"); err != ErrNotFound {
		t.Fatalf("SessionUser(expired) err = %v, want ErrNotFound", err)
	}

	tid, err := st.AddTexture(ctx, uid, "skin", hashOf("a"), "default", TextureMeta{Description: "first"})
	if err != nil || tid <= 0 {
		t.Fatalf("AddTexture = %d, %v", tid, err)
	}
	if _, err := st.AddTexture(ctx, uid, "skin", hashOf("a"), "slim", TextureMeta{}); err != ErrDuplicatePublic {
		t.Fatalf("duplicate public content err = %v, want ErrDuplicatePublic", err)
	}
	// Two private copies of the same bytes are legal (data-compat rule: privacy
	// is per-row), which is exactly what the NULL public_dedupe column encodes.
	p1, err := st.AddTexturePrivate(ctx, uid, "skin", hashOf("b"), "default", TextureMeta{})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := st.AddTexturePrivate(ctx, uid, "skin", hashOf("b"), "default", TextureMeta{})
	if err != nil || p1 == p2 {
		t.Fatalf("private copies = %d/%d, %v", p1, p2, err)
	}

	// Republishing a private row writes public_dedupe through Engine.Concat; if
	// that concatenation is wrong the dedupe cannot reject a later duplicate.
	yes := true
	if err := st.UpdateTextureMeta(ctx, p1, uid, TextureMetaPatch{Public: &yes}); err != nil {
		t.Fatalf("publish private texture: %v", err)
	}
	if _, err := st.AddTexture(ctx, uid, "skin", hashOf("b"), "default", TextureMeta{}); err != ErrDuplicatePublic {
		t.Fatalf("content published from the closet must dedupe, err = %v", err)
	}
}

func hashOf(seed string) string {
	// 64 hex chars, as the textures.hash column requires.
	full := ""
	for len(full) < 64 {
		full += seed
	}
	return full[:64]
}

func TestSQLiteEngineEmailVerificationTokens(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid, err := st.CreateUser(ctx, "verify@example.com", "verifier", "x", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateEmailVerifyToken(ctx, uid, "digest-one", time.Hour); err != nil {
		t.Fatalf("create token: %v", err)
	}
	got, err := st.ConsumeEmailVerifyToken(ctx, "digest-one")
	if err != nil || got != uid {
		t.Fatalf("consume = %d, %v", got, err)
	}
	// Single use: the digest is gone from the live set, and a replay must answer
	// the same way an unknown digest does.
	if _, err := st.ConsumeEmailVerifyToken(ctx, "digest-one"); err != ErrNotFound {
		t.Fatalf("replay err = %v, want ErrNotFound", err)
	}
	if _, err := st.ConsumeEmailVerifyToken(ctx, "never-minted"); err != ErrNotFound {
		t.Fatalf("unknown digest err = %v, want ErrNotFound", err)
	}
	// A newer request invalidates the older one (last email sent is the one that
	// works), and the write carries the Go clock's used_at.
	if err := st.CreateEmailVerifyToken(ctx, uid, "digest-two", time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateEmailVerifyToken(ctx, uid, "digest-three", time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ConsumeEmailVerifyToken(ctx, "digest-two"); err != ErrNotFound {
		t.Fatalf("superseded token err = %v, want ErrNotFound", err)
	}
	if _, err := st.ConsumeEmailVerifyToken(ctx, "digest-three"); err != nil {
		t.Fatalf("latest token err = %v", err)
	}
	var used sql.NullTime
	if err := st.DB().QueryRow(`SELECT used_at FROM email_verify_tokens WHERE token_hash = 'digest-three'`).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if !used.Valid || used.Time.IsZero() {
		t.Errorf("consume did not stamp used_at: %+v", used)
	}
}

func TestSQLiteEngineChangingEmailClearsVerification(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid, err := st.CreateUser(ctx, "keep@example.com", "keeper", "x", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, `UPDATE users SET email_verified = 1 WHERE id = ?`, uid); err != nil {
		t.Fatal(err)
	}
	// Re-saving the same address is a no-op edit and must not lock the player
	// out of the server; this is the CASE WHEN + NullSafeEq path, where MySQL
	// reads the old column left-to-right and the other engines always do.
	if err := st.UpdateUserEmail(ctx, uid, "keep@example.com"); err != nil {
		t.Fatal(err)
	}
	if u, err := st.UserByID(ctx, uid); err != nil || !u.EmailVerified {
		t.Fatalf("same address cleared the flag: %+v, %v", u, err)
	}
	if err := st.UpdateUserEmail(ctx, uid, "moved@example.com"); err != nil {
		t.Fatal(err)
	}
	if u, err := st.UserByID(ctx, uid); err != nil || u.EmailVerified {
		t.Fatalf("new address kept the flag: %+v, %v", u, err)
	}
}

func TestSQLiteEngineNotifications(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid, err := st.CreateUser(ctx, "inbox@example.com", "reader", "x", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	tid, err := st.AddTexture(ctx, uid, "cape", hashOf("c"), "", TextureMeta{Description: "night cape"})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"report.resolved", "texture.hidden"} {
		if err := st.InsertNotification(ctx, uid, k, tid, "412"); err != nil {
			t.Fatalf("insert %s: %v", k, err)
		}
	}
	if n, err := st.UnreadNotificationCount(ctx, uid); err != nil || n != 2 {
		t.Fatalf("unread = %d, %v", n, err)
	}
	rows, total, err := st.ListNotifications(ctx, uid, true, 1, 1)
	if err != nil || total != 2 || len(rows) != 1 {
		t.Fatalf("list page1 = %d rows / total %d, %v", len(rows), total, err)
	}
	var first int64
	if err := st.DB().QueryRow(`SELECT id FROM notifications WHERE uid = ? ORDER BY id LIMIT 1`, uid).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkNotificationRead(ctx, uid, first); err != nil {
		t.Fatal(err)
	}
	if n, _ := st.UnreadNotificationCount(ctx, uid); n != 1 {
		t.Errorf("unread after one read = %d, want 1", n)
	}
	// Reading again is idempotent (the WHERE pins read_at IS NULL), and another
	// user's id must not touch this user's row.
	if err := st.MarkNotificationRead(ctx, uid, first); err != nil {
		t.Errorf("second mark: %v", err)
	}
	if err := st.MarkAllNotificationsRead(ctx, uid); err != nil {
		t.Fatal(err)
	}
	if n, _ := st.UnreadNotificationCount(ctx, uid); n != 0 {
		t.Errorf("unread after mark-all = %d, want 0", n)
	}
	if name := st.TextureNameForNotification(ctx, tid); name != "night cape" {
		t.Errorf("TextureNameForNotification = %q, want the description snapshot", name)
	}
	if name := st.TextureNameForNotification(ctx, 999999); name != "" {
		t.Errorf("name for a missing texture = %q, want empty", name)
	}
}

func TestSQLiteEnginePluginKVSweep(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	soon := time.Now().UTC().Add(30 * time.Millisecond)
	if err := st.SetPluginKV(ctx, "demo", "ns", "k1", "v1", &soon); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPluginKV(ctx, "demo", "ns", "k2", "v2", nil); err != nil {
		t.Fatal(err)
	}
	v, exp, ok, err := st.GetPluginKV(ctx, "demo", "ns", "k1")
	if err != nil || !ok || v != "v1" || exp == nil {
		t.Fatalf("get k1 = %q/%v/%v/%v", v, exp, ok, err)
	}
	time.Sleep(80 * time.Millisecond)
	// The sweep is the derived-table DELETE … LIMIT: MySQL's `DELETE … LIMIT`
	// does not parse on PostgreSQL or SQLite.
	n, err := st.SweepPluginKV(ctx, 10)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 1 {
		t.Errorf("swept %d rows, want exactly the one expired key", n)
	}
	if _, _, ok, _ := st.GetPluginKV(ctx, "demo", "ns", "k1"); ok {
		t.Error("expired key survived the sweep")
	}
	if _, _, ok, _ := st.GetPluginKV(ctx, "demo", "ns", "k2"); !ok {
		t.Error("sweep deleted a key with no expiry")
	}
}

func TestSQLiteEngineOptionUpsert(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	if err := st.SetOption(ctx, "site_name", "First"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetOption(ctx, "site_name", "Second"); err != nil {
		t.Fatal(err)
	}
	v, ok, err := st.GetOption(ctx, "site_name")
	if err != nil || !ok || v != "Second" {
		t.Fatalf("option after upsert = %q/%v/%v", v, ok, err)
	}
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM options WHERE k = 'site_name'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("upsert left %d rows for one key", n)
	}
}

func TestSQLiteEnginePruneKeepsNewest(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid, err := st.CreateUser(ctx, "many@example.com", "many", "x", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC()
	for i := 0; i < 4; i++ {
		_, err := st.DB().ExecContext(ctx,
			`INSERT INTO ygg_tokens (user_id, client_token, access_token, expires_at, invalidated)
			 VALUES (?,?,?, ?,0)`,
			uid, "ct", "at"+string(rune('a'+i)), base.Add(time.Duration(i)*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
	}
	evicted, err := st.PruneUserTokens(ctx, uid, 2)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if evicted != 2 {
		t.Fatalf("evicted = %d, want 2", evicted)
	}
	var live int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM ygg_tokens WHERE user_id = ? AND invalidated = 0`, uid).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 2 {
		t.Errorf("live tokens = %d, want the 2 newest", live)
	}
	var oldest sql.NullInt64
	_ = st.DB().QueryRow(`SELECT MIN(id) FROM ygg_tokens WHERE user_id = ? AND invalidated = 0`, uid).Scan(&oldest)
	var firstLive int64
	if err := st.DB().QueryRow(`SELECT id FROM ygg_tokens WHERE user_id = ? AND invalidated = 1 ORDER BY id DESC LIMIT 1`, uid).Scan(&firstLive); err != nil {
		t.Fatal(err)
	}
	if oldest.Int64 <= firstLive {
		t.Errorf("prune invalidated the newest rows: first live id %d, last pruned id %d", oldest.Int64, firstLive)
	}
	// Idempotent: nothing left above the keep count.
	if n, err := st.PruneUserTokens(ctx, uid, 2); err != nil || n != 0 {
		t.Errorf("second prune = %d, %v", n, err)
	}
}

func TestSQLiteEngineHashLockRunsTheWork(t *testing.T) {
	st := openSQLiteStore(t)
	ran := false
	err := st.WithHashLock(context.Background(), hashOf("d"), func(context.Context) error {
		ran = true
		return nil
	})
	if err != nil || !ran {
		t.Fatalf("with-hash-lock on sqlite: ran=%v err=%v", ran, err)
	}
	sentinel := sql.ErrNoRows
	if err := st.WithHashLock(context.Background(), hashOf("d"), func(context.Context) error { return sentinel }); err != sentinel {
		t.Errorf("locked work error = %v, want it to pass through", err)
	}
}

func TestSQLiteEngineZeroMeansUnset(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid, err := st.CreateUser(ctx, "wear@example.com", "wearer", "x", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	pid, _, err := st.CreatePlayer(ctx, uid, "Wearer")
	if err != nil {
		t.Fatal(err)
	}
	tid, err := st.AddTexture(ctx, uid, "skin", hashOf("f"), "default", TextureMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetPlayerTextures(ctx, pid, tid, 0); err != nil {
		t.Fatalf("set textures: %v", err)
	}
	var skinNull, capeNull int
	if err := st.DB().QueryRow(`SELECT CASE WHEN tid_skin IS NULL THEN 1 ELSE 0 END,
		CASE WHEN tid_cape IS NULL THEN 1 ELSE 0 END FROM players WHERE id = ?`, pid).
		Scan(&skinNull, &capeNull); err != nil {
		t.Fatal(err)
	}
	if skinNull != 0 || capeNull != 1 {
		t.Errorf("tid=%d set / 0 cleared => skinNull=%d capeNull=%d, want 0 and 1", tid, skinNull, capeNull)
	}
	// Clearing again must land on NULL, not on 0: 0 is a valid-looking id for
	// every reader that only checks `IS NOT NULL`.
	if err := st.SetPlayerTextures(ctx, pid, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.DB().QueryRow(`SELECT CASE WHEN tid_skin IS NULL THEN 1 ELSE 0 END FROM players WHERE id = ?`, pid).
		Scan(&skinNull); err != nil {
		t.Fatal(err)
	}
	if skinNull != 1 {
		t.Error("passing 0 must clear the column to NULL")
	}
	if err := st.SetUserAvatar(ctx, uid, tid); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserAvatar(ctx, uid, 0); err != nil {
		t.Fatal(err)
	}
	var avatarNull int
	if err := st.DB().QueryRow(`SELECT CASE WHEN avatar_tid IS NULL THEN 1 ELSE 0 END FROM users WHERE id = ?`, uid).
		Scan(&avatarNull); err != nil {
		t.Fatal(err)
	}
	if avatarNull != 1 {
		t.Error("avatar 0 must clear users.avatar_tid")
	}
}

func TestSQLiteEngineClosetDropsOneCopy(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid, err := st.CreateUser(ctx, "closet@example.com", "closeted", "x", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	tid, err := st.AddTexturePrivate(ctx, uid, "skin", hashOf("e"), "default", TextureMeta{})
	if err != nil {
		t.Fatal(err)
	}
	// user_closet has no unique key (legacy shape), so the same texture can sit
	// in the closet twice and one removal must take exactly one copy.
	for i := 0; i < 2; i++ {
		_, err := st.DB().ExecContext(ctx,
			`INSERT INTO user_closet (user_uid, texture_tid, item_name) VALUES (?,?,?)`, uid, tid, "hat")
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := st.RemoveClosetEntryByTID(ctx, uid, tid); err != nil {
		t.Fatalf("remove: %v", err)
	}
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM user_closet WHERE user_uid = ? AND texture_tid = ?`, uid, tid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("closet rows after one removal = %d, want 1", n)
	}
	if err := st.RemoveClosetEntryByTID(ctx, uid, tid); err != nil {
		t.Fatalf("second remove: %v", err)
	}
	if err := st.RemoveClosetEntryByTID(ctx, uid, tid); err != ErrNotFound {
		t.Errorf("remove from an empty closet = %v, want ErrNotFound", err)
	}
}
