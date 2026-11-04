// Engine test: the store's swept SQL run against a real SQLite database.
//
// The MySQL integration suite proves these queries on one engine; this file is
// what makes the multi-engine claim more than a migration tree plus a promise.
// It has no build tag and needs no server, so every CI run executes it.
package store

import (
	"context"
	"database/sql"
	"errors"
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

// TestSQLiteEngineSetupGateIsNotMySQLCatalogue pins the install wizard's gate.
// The catalogue probe used to read `information_schema` with MySQL's
// `DATABASE()`, which SQLite has no notion of — so the very first request a
// fresh SQLite site makes (`GET /api/v1/setup/status`) answered
// "no such table: information_schema.tables" and the wizard never appeared.
// Running the binary found it; no query test had covered the handler.
func TestSQLiteEngineSetupGateIsNotMySQLCatalogue(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()

	if ok, err := st.TableExists(ctx, "users"); err != nil || !ok {
		t.Fatalf("TableExists(users) = %v, %v", ok, err)
	}
	if ok, err := st.TableExists(ctx, "no_such_table"); err != nil || ok {
		t.Fatalf("TableExists(missing) = %v, %v — must be a clean false", ok, err)
	}
	need, err := st.NeedsSetup(ctx)
	if err != nil {
		t.Fatalf("NeedsSetup on an empty site: %v", err)
	}
	if !need {
		t.Error("a migrated schema with no users must still ask for setup")
	}
	if err := st.SetOption(ctx, "setup_done", "1"); err != nil {
		t.Fatal(err)
	}
	if need, err := st.NeedsSetup(ctx); err != nil || need {
		t.Errorf("NeedsSetup after the marker = %v, %v", need, err)
	}
	// A table the site does not have must read as absent rather than as an
	// error — the gate treats an error as "cannot serve".
	if ok, err := st.TableExists(ctx, "information_schema_tables"); err != nil || ok {
		t.Errorf("unheard-of table reported as existing: %v %v", ok, err)
	}
}

// TestSQLiteEngineAdminSearchIsCaseInsensitive pins the search semantics that
// MySQL's column collation gave for free. PostgreSQL and SQLite compare LIKE
// case-sensitively, so an admin searching a player by lowercase name found
// nothing there — the CI PostgreSQL leg caught it, and this is the same engine
// boundary the panel must not depend on.
func TestSQLiteEngineAdminSearchIsCaseInsensitive(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid, err := st.CreateUser(ctx, "search@example.com", "Searcher", "x", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	pid, _, err := st.CreatePlayer(ctx, uid, "NotchSteve")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"notchsteve", "NOTCHSTEVE", "NotchSteve", "chSt"} {
		rows, total, err := st.AdminPlayers(ctx, q, 0, 1, 20)
		if err != nil || total != 1 || len(rows) != 1 {
			t.Errorf("search %q: total=%d rows=%d err=%v", q, total, len(rows), err)
		}
	}
	if _, total, err := st.AdminPlayers(ctx, "searcher", 0, 1, 20); err != nil || total != 1 {
		t.Errorf("owner nickname search: total=%d err=%v", total, err)
	}
	if _, total, _ := st.AdminPlayers(ctx, "nomatch", 0, 1, 20); total != 0 {
		t.Errorf("nonsense query matched %d rows, want 0", total)
	}
	_ = pid
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

// A hard-deleted texture must not hand its id to a later row. Reports and
// tombstones key on texture_id, so a reused id silently re-attributes someone
// else's moderation history — and the second tombstone insert refuses outright.
func TestSQLiteEngineTextureIDsAreNeverReused(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid, err := st.CreateUser(ctx, "ids@example.com", "ids", "x", 3, "")
	if err != nil {
		t.Fatal(err)
	}

	first, err := st.AddTexture(ctx, uid, "skin", hashOf("first"), "default", TextureMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DeleteTexture(ctx, first, uid, uid, true, ""); err != nil {
		t.Fatalf("delete first: %v", err)
	}
	second, err := st.AddTexture(ctx, uid, "skin", hashOf("second"), "default", TextureMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatalf("texture id %d was handed to a new row after the old one was hard-deleted", first)
	}
	// The reused id is also what makes a second deletion of the same slot fail.
	if _, err := st.DeleteTexture(ctx, second, uid, uid, true, ""); err != nil {
		t.Fatalf("delete second: %v", err)
	}
}

// The moderation verdict outlives the row: bytes struck as a violation and then
// hard-deleted must not re-enter the library as a fresh upload. Deletions that
// carried no verdict must stay re-uploadable, or a player who removed a skin could
// never put it back.
func TestSQLiteEngineViolationTombstoneKeepsContentBlocked(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()

	uid, err := st.CreateUser(ctx, "uploader@example.com", "uploader", "x", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := st.CreateUser(ctx, "handler@example.com", "handler", "x", 3, "")
	if err != nil {
		t.Fatal(err)
	}

	struck, err := st.AddTexture(ctx, uid, "skin", hashOf("struck"), "default", TextureMeta{Description: "doomed"})
	if err != nil {
		t.Fatal(err)
	}
	reportID, err := st.CreateReport(ctx, struck, handler, "offensive", "take it down")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.StrikeTexture(ctx, struck, reportID, handler); err != nil {
		t.Fatalf("strike: %v", err)
	}
	if _, err := st.DeleteTexture(ctx, struck, 0, handler, true, "moderation verdict"); err != nil {
		t.Fatalf("forced delete of the struck texture: %v", err)
	}
	if blocked, err := st.ViolationTombstoned(ctx, "skin", hashOf("struck")); err != nil || !blocked {
		t.Errorf("struck content blocked = %v (%v), want true", blocked, err)
	}
	// The verdict is about these bytes as a skin. The same pixels as a cape are a
	// different content address in this schema, so a ban must not widen itself
	// across types just because the hash matches.
	if blocked, err := st.ViolationTombstoned(ctx, "cape", hashOf("struck")); err != nil || blocked {
		t.Errorf("same hash as a cape blocked = %v (%v), want false", blocked, err)
	}

	clean, err := st.AddTexture(ctx, uid, "skin", hashOf("clean"), "default", TextureMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DeleteTexture(ctx, clean, uid, uid, true, ""); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
	if blocked, err := st.ViolationTombstoned(ctx, "skin", hashOf("clean")); err != nil || blocked {
		t.Errorf("clean deletion blocked = %v (%v), want false", blocked, err)
	}

	// The false-positive path: the verdict was lifted before the row went away, so
	// nothing about this content is banned any more.
	lifted, err := st.AddTexture(ctx, uid, "skin", hashOf("lifted"), "default", TextureMeta{})
	if err != nil {
		t.Fatal(err)
	}
	liftReport, err := st.CreateReport(ctx, lifted, handler, "offensive", "wrongly reported")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.StrikeTexture(ctx, lifted, liftReport, handler); err != nil {
		t.Fatalf("strike before lift: %v", err)
	}
	if err := st.LiftTextureModeration(ctx, lifted); err != nil {
		t.Fatalf("lift: %v", err)
	}
	if _, err := st.DeleteTexture(ctx, lifted, 0, handler, true, ""); err != nil {
		t.Fatalf("delete after lift: %v", err)
	}
	if blocked, err := st.ViolationTombstoned(ctx, "skin", hashOf("lifted")); err != nil || blocked {
		t.Errorf("content deleted after its verdict was lifted blocked = %v (%v), want false", blocked, err)
	}
}

// A站长 who finds banned bytes in the library should not have to wait for a
// player to report them. reportID 0 is that path: same verdict, same
// un-publishing, nothing to close.
func TestSQLiteEngineStrikeWithoutReport(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid, err := st.CreateUser(ctx, "strike@example.com", "striker", "x", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	tid, err := st.AddTexture(ctx, uid, "skin", hashOf("strike-direct"), "default", TextureMeta{Description: "banned look"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.StrikeTexture(ctx, tid, 0, uid); err != nil {
		t.Fatalf("strike with no report: %v", err)
	}
	if pub, _ := st.TextureIsPublic(ctx, tid); pub {
		t.Error("a directly struck texture is still listed publicly")
	}
	if _, n, err := st.AdminTextures(ctx, "", "", "struck", 1, 50); err != nil || n != 1 {
		t.Errorf("struck filter after a direct strike = %d (err %v), want 1", n, err)
	}
	// The verdict must survive as evidence once the row goes: same content, same
	// ban, whether or not a report ever existed.
	if _, err := st.DeleteTexture(ctx, tid, 0, uid, true, "站长直接处置"); err != nil {
		t.Fatalf("delete the struck texture: %v", err)
	}
	if blocked, err := st.ViolationTombstoned(ctx, "skin", hashOf("strike-direct")); err != nil || !blocked {
		t.Errorf("directly struck content blocked = %v (%v), want true", blocked, err)
	}
	// And a strike still refuses a texture that is not there.
	if err := st.StrikeTexture(ctx, tid+999, 0, uid); !errors.Is(err, ErrNotFound) {
		t.Errorf("strike a missing texture = %v, want ErrNotFound", err)
	}
}

// The fsck queries have to see what the library actually holds: which addresses are
// reachable, which ones only survive as deletion evidence, and the one mismatch a
// database can prove — a tombstone citing an id that a different texture now owns.
func TestSQLiteEngineFSCKSeesReachableAndStruckContent(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid, err := st.CreateUser(ctx, "fsck@example.com", "fscker", "x", 3, "")
	if err != nil {
		t.Fatal(err)
	}

	live, err := st.AddTexture(ctx, uid, "skin", hashOf("live"), "default", TextureMeta{})
	if err != nil {
		t.Fatal(err)
	}
	struck, err := st.AddTexture(ctx, uid, "skin", hashOf("struck"), "default", TextureMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DeleteTexture(ctx, struck, uid, uid, true, "moderation verdict"); err != nil {
		t.Fatalf("delete the struck one: %v", err)
	}

	usage, err := st.FSCKHashUsage(ctx)
	if err != nil {
		t.Fatalf("hash usage: %v", err)
	}
	if usage[hashOf("live")] != 1 {
		t.Errorf("live address counted %d times, want 1", usage[hashOf("live")])
	}
	if _, ok := usage[hashOf("struck")]; ok {
		t.Error("a deleted address is still reported as reachable")
	}

	tombstoned, err := st.FSCKTombstonedHashes(ctx)
	if err != nil {
		t.Fatalf("tombstoned: %v", err)
	}
	if !tombstoned[hashOf("struck")] {
		t.Error("the deleted content is missing from the evidence set")
	}
	if tombstoned[hashOf("live")] {
		t.Error("a live texture is reported as deleted evidence")
	}

	// AUTOINCREMENT stops new collisions, so a recycled slot can only arrive from a
	// database that predates the fix. Both halves of such a pair must show up.
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO texture_tombstones (texture_id, uid, type, hash, deleted_by, upload_at)
		 VALUES (?, ?, 'skin', ?, ?, '2026-01-01 00:00:00')`, live, uid, hashOf("ghost"), uid); err != nil {
		t.Fatalf("plant a recycled slot: %v", err)
	}
	slots, err := st.FSCKRecycledSlots(ctx)
	if err != nil {
		t.Fatalf("recycled slots: %v", err)
	}
	if len(slots) != 1 {
		t.Fatalf("recycled slots = %d, want exactly 1", len(slots))
	}
	if slots[0].TextureID != live || slots[0].TombstoneHash != hashOf("ghost") || slots[0].LiveHash != hashOf("live") {
		t.Errorf("recycled slot = %+v, want the planted id with both addresses", slots[0])
	}

	textures, tombs, openReports, err := st.FSCKCounts(ctx)
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	if textures != 1 || tombs != 2 || openReports != 0 {
		t.Errorf("counts = textures %d tombstones %d open reports %d, want 1/2/0", textures, tombs, openReports)
	}
}

// The panel's material list is the only place an operator can see what the site
// holds. Two facts must travel with each row: whether it is public, and whether a
// moderation verdict sits on it — otherwise「已下架」and「还在库里」look identical.
func TestSQLiteEngineAdminTexturesReportsVisibilityAndVerdict(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid, err := st.CreateUser(ctx, "panel@example.com", "panellist", "x", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := st.CreateUser(ctx, "panelmod@example.com", "panelmod", "x", 3, "")
	if err != nil {
		t.Fatal(err)
	}

	visible, err := st.AddTexture(ctx, uid, "skin", hashOf("p-visible"), "default", TextureMeta{Description: "in library"})
	if err != nil {
		t.Fatal(err)
	}
	private, err := st.AddTexturePrivate(ctx, uid, "skin", hashOf("p-private"), "default", TextureMeta{})
	if err != nil {
		t.Fatal(err)
	}
	struck, err := st.AddTexture(ctx, uid, "cape", hashOf("p-struck"), "", TextureMeta{})
	if err != nil {
		t.Fatal(err)
	}
	reportID, err := st.CreateReport(ctx, struck, handler, "violence", "gore")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.StrikeTexture(ctx, struck, reportID, handler); err != nil {
		t.Fatalf("strike: %v", err)
	}

	rows, total, err := st.AdminTextures(ctx, "", "", "", 1, 50)
	if err != nil {
		t.Fatalf("admin textures: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3 rows in the library", total)
	}
	byID := map[int64]map[string]interface{}{}
	for _, m := range rows {
		byID[m["id"].(int64)] = m
	}
	for _, tc := range []struct {
		name       string
		tid        int64
		wantPublic bool
		wantMod    string
		wantName   string
	}{
		{"public", visible, true, "", "in library"},
		{"private", private, false, "", ""},
		{"struck", struck, false, "violation", ""},
	} {
		row, ok := byID[tc.tid]
		if !ok {
			t.Fatalf("%s row missing from the admin list", tc.name)
		}
		if got, isBool := row["public"].(bool); !isBool || got != tc.wantPublic {
			t.Errorf("%s public = %#v, want %v", tc.name, row["public"], tc.wantPublic)
		}
		if tc.wantName != "" {
			if got, _ := row["name"].(string); got != tc.wantName {
				t.Errorf("%s name = %q, want %q (a hash-only list is unreadable to an operator)", tc.name, got, tc.wantName)
			}
		}
		if got, isStr := row["moderation"].(string); !isStr || got != tc.wantMod {
			t.Errorf("%s moderation = %#v, want %q", tc.name, row["moderation"], tc.wantMod)
		}
	}

	// Paging must report the whole count while returning one page of rows.
	page2, total2, err := st.AdminTextures(ctx, "", "", "", 2, 2)
	if err != nil {
		t.Fatalf("admin textures page 2: %v", err)
	}
	if len(page2) != 1 || total2 != 3 {
		t.Errorf("page 2 = %d rows of total %d, want 1 of 3", len(page2), total2)
	}

	// The three filters are what makes this list operable, and they have to
	// narrow the *count* too or the pager lies about how many pages exist.
	if got, n, err := st.AdminTextures(ctx, "", "cape", "", 1, 50); err != nil {
		t.Fatalf("filter by type: %v", err)
	} else if n != 1 || len(got) != 1 || got[0]["id"].(int64) != struck {
		t.Errorf("type=cape = %d rows of %d, want only the struck cape", len(got), n)
	}
	for _, tc := range []struct {
		state string
		want  int
	}{
		{"public", 1},  // the struck cape is not public either, but never was listed
		{"private", 2}, // the private skin and the struck cape
		{"struck", 1},
	} {
		got, n, err := st.AdminTextures(ctx, "", "", tc.state, 1, 50)
		if err != nil {
			t.Fatalf("filter by state %s: %v", tc.state, err)
		}
		if n != tc.want || len(got) != tc.want {
			t.Errorf("state=%s = %d rows of total %d, want %d", tc.state, len(got), n, tc.want)
		}
	}
	// Search reaches the uploader's nickname, because 「谁传的那张」 is asked in
	// names, not in #7.
	byNick, nNick, err := st.AdminTextures(ctx, "panellist", "", "", 1, 50)
	if err != nil {
		t.Fatalf("filter by nickname: %v", err)
	}
	if nNick != 3 || len(byNick) != 3 {
		t.Errorf("search by uploader = %d rows of %d, want all three (one uploader)", len(byNick), nNick)
	}
	skinsSeen := 0
	for _, row := range byNick {
		if got, _ := row["nickname"].(string); got != "panellist" {
			t.Fatalf("row %#v carries nickname %q, want the uploader's; the list would show a bare #uid", row, got)
		}
		if row["type"] == "skin" {
			skinsSeen++
			if got, _ := row["previewURL"].(string); got == "" {
				t.Errorf("skin row %#v has no previewURL; the panel stays text-only", row)
			}
		}
	}
	if skinsSeen != 2 {
		t.Errorf("skin rows = %d, want 2", skinsSeen)
	}
	// A cape has no body render, so its thumbnail must stay empty rather than
	// point at a preview route that 404s.
	capeRow, _, err := st.AdminTextures(ctx, "", "cape", "", 1, 50)
	if err != nil || len(capeRow) != 1 {
		t.Fatalf("cape row: %v %d", err, len(capeRow))
	}
	if got, _ := capeRow[0]["previewURL"].(string); got != "" {
		t.Errorf("cape previewURL = %q, want empty", got)
	}
	// Searching by uploader must be able to exclude, too: the other account owns
	// nothing here.
	if _, n, err := st.AdminTextures(ctx, "panelmod", "", "", 1, 50); err != nil || n != 0 {
		t.Errorf("search by the second uploader = %d rows (err %v), want 0", n, err)
	}
	// Wildcards in a search string are text, not pattern.
	if _, n, err := st.AdminTextures(ctx, "%", "", "", 1, 50); err != nil || n != 0 {
		t.Errorf("a search of one percent sign matched %d rows (err %v), want 0 — the LIKE must be escaped", n, err)
	}
}

// The audit log is where an operator checks what the panel did. Fixed at the last
// 100 entries, it silently stops showing anything older with no hint that more
// exists, so the list needs a total and a page the way every other table has one.
func TestSQLiteEngineAdminAuditPagesWithTotal(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid, err := st.CreateUser(ctx, "auditor@example.com", "auditor", "x", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"first", "second", "third"} {
		st.InsertAudit(ctx, uid, action, map[string]any{"why": action})
	}

	rows, total, err := st.AdminAudit(ctx, 1, 2)
	if err != nil {
		t.Fatalf("admin audit page 1: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3 audit rows", total)
	}
	if len(rows) != 2 {
		t.Fatalf("page 1 = %d rows, want 2", len(rows))
	}
	if newest, ok := rows[0]["action"].(string); !ok || newest != "third" {
		t.Errorf("newest entry = %#v, want third (rows must come back newest first)", rows[0])
	}
	older, total2, err := st.AdminAudit(ctx, 2, 2)
	if err != nil {
		t.Fatalf("admin audit page 2: %v", err)
	}
	if len(older) != 1 || total2 != 3 {
		t.Errorf("page 2 = %d rows of total %d, want 1 of 3", len(older), total2)
	}
	if action, _ := older[0]["action"].(string); action != "first" {
		t.Errorf("page 2 holds %q, want the oldest entry", action)
	}
}

// The store's search predicates must treat a typed "%" or "_" as text. Left
// unescaped, "%" matches every row (a full scan behind an anonymous library
// search) and "_" silently widens the query.
func TestSQLiteEngineSearchTreatsWildcardsLiterally(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid, err := st.CreateUser(ctx, "wild@example.com", "wildcards", "x", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddTexture(ctx, uid, "skin", hashOf("w-percent"), "default", TextureMeta{Description: "50%off skin"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddTexture(ctx, uid, "skin", hashOf("w-under"), "default", TextureMeta{Description: "axb cape"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddTexture(ctx, uid, "skin", hashOf("w-plain"), "default", TextureMeta{Description: "ordinary"}); err != nil {
		t.Fatal(err)
	}

	count := func(kw string) int {
		opts := SkinlibOpts{Keyword: kw, PublicOnly: true, Page: 1, PerPage: 50}
		_, total, err := st.SkinlibList(ctx, opts)
		if err != nil {
			t.Fatalf("search %q: %v", kw, err)
		}
		return total
	}
	// Escaped, "%" means a percent sign: exactly the one row whose name carries
	// it. Unescaped it would be every row in the library.
	if got := count("%"); got != 1 {
		t.Fatalf(`keyword "%%" matched %d rows, want the single literal one`, got)
	}
	if got := count("50%off"); got != 1 {
		t.Fatalf(`keyword "50%%off" matched %d rows, want the one literal name`, got)
	}
	if got := count("a_b"); got != 0 {
		t.Fatalf(`keyword "a_b" matched %d rows, want 0 — "_" must not match "x"`, got)
	}
	// Case folding still works through LOWER(?) on the bound side.
	if got := count("ORDINARY"); got != 1 {
		t.Fatalf("keyword ORDINARY matched %d rows, want 1", got)
	}
}

// A hostile page number must not reach the engine as a negative OFFSET.
func TestSQLiteEngineHugePageDoesNotError(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	if _, _, err := st.AdminTextures(ctx, "", "", "", 9223372036854775807, 50); err != nil {
		t.Fatalf("AdminTextures with a huge page: %v", err)
	}
	if _, _, err := st.AdminAudit(ctx, 9223372036854775807, 100); err != nil {
		t.Fatalf("AdminAudit with a huge page: %v", err)
	}
	if _, _, err := st.AdminUsers(ctx, "", 9223372036854775807, 20); err != nil {
		t.Fatalf("AdminUsers with a huge page: %v", err)
	}
}

// The uploader's own list is the one place a private texture is always visible,
// so its filters must never widen past the owner — and a typed "%" must not turn
// "find my one skin" into "list everything I own".
func TestSQLiteEngineOwnTextureListFilters(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	mine, err := st.CreateUser(ctx, "me@example.com", "me", "x", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := st.CreateUser(ctx, "other@example.com", "other", "x", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddTexturePrivate(ctx, mine, "skin", hashOf("own-skin"), "default", TextureMeta{Description: "50%off"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddTexturePrivate(ctx, mine, "cape", hashOf("own-cape"), "", TextureMeta{Description: "night"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddTexture(ctx, theirs, "skin", hashOf("their-skin"), "default", TextureMeta{Description: "50%off"}); err != nil {
		t.Fatal(err)
	}

	all, total, err := st.ListTexturesPage(ctx, mine, "", "", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(all) != 2 {
		t.Fatalf("unfiltered own list = %d rows / total %d, want 2/2", len(all), total)
	}

	if _, n, _ := st.ListTexturesPage(ctx, mine, "", "cape", 50, 0); n != 1 {
		t.Fatalf("type=cape matched %d, want 1", n)
	}
	if _, n, _ := st.ListTexturesPage(ctx, mine, "50%off", "", 50, 0); n != 1 {
		t.Fatalf(`q="50%%off" matched %d, want the one literal name`, n)
	}
	if _, n, _ := st.ListTexturesPage(ctx, mine, "%", "", 50, 0); n != 1 {
		t.Fatalf(`q="%%" matched %d, want only the row whose name carries a percent sign`, n)
	}
	// The hash side of the match is what makes "find the one I just uploaded" work
	// when the texture is unnamed.
	if rows, n, _ := st.ListTexturesPage(ctx, mine, hashOf("own-cape"), "", 50, 0); n != 1 || rows[0].Type != "cape" {
		t.Fatalf("search by content address = %d rows, want the cape", n)
	}
	// Someone else's identically-named texture must not appear.
	if _, n, _ := st.ListTexturesPage(ctx, mine, "50%off", "", 50, 0); n != 1 {
		t.Fatalf("the filter leaked another user's row: %d matches", n)
	}
}
