//go:build integration

// integration_test.go: MySQL round-trip coverage for the 0003 (OIDC) and
// 0004 (Union) schemas plus the web/session/audit helpers they rely on.
//
// Runs outside the default build (CGO-free dev boxes have no MySQL):
//
//	SKIN_TEST_DSN='skin:pass@tcp(127.0.0.1:3306)/skin_nexus_test?parseTime=true' \
//	  go test -tags integration ./internal/store/
//
// The target database MUST be empty/disposable: the suite drops and
// re-creates every table it touches, idempotently. CI runs it against a
// MySQL 8 service container (see .github/workflows/ci.yml).
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"skin-nexus/server/internal/auth"
	"skin-nexus/server/internal/jobs"
	"skin-nexus/server/internal/migrate"
	"skin-nexus/server/internal/oidc"
	"skin-nexus/server/internal/ygg"
)

// testTables mirrors the DDL the suite owns; dropped before apply so reruns
// on a reused database stay green.
var testTables = []string{
	"oidc_device_auth", "oidc_refresh_tokens", "oidc_access_tokens",
	"oidc_auth_requests", "oidc_clients",
	"union_kv", "web_sessions", "audit_log", "options",
	"ygg_join_sessions", "ygg_tokens", "uuid_map", "textures", "players", "users",
	"invitation_codes", "user_closet", "password_reset_tokens", "access_logs", "schema_migrations",
	"plugin_meta", "plugin_secrets", "plugin_kv", "plugin_docs", "plugin_migrations", "plugin_install_tx",
	"host_jobs",
	"legacy_users", "legacy_players", "legacy_textures", "legacy_uuid",
	"legacy_options", "legacy_user_closet", "legacy_invitation_codes",
}

func openTestStore(t *testing.T) *Mysql {
	t.Helper()
	dsn := os.Getenv("SKIN_TEST_DSN")
	if dsn == "" {
		t.Skip("SKIN_TEST_DSN not set; skipping MySQL integration suite")
	}
	m, err := Open(dsn, "https://skin.test", nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })

	ctx := context.Background()
	_, _ = m.db.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS=0")
	for _, tbl := range testTables {
		_, _ = m.db.ExecContext(ctx, "DROP TABLE IF EXISTS "+tbl)
	}
	applyMigrations(t, m)
	return m
}

// applyMigrations executes migrations/*.sql in filename order via the tracked
// path (per-file markers + duplicate-object tolerance) — the same helper the
// empty-DB and existing-DB boot paths run.
func applyMigrations(t *testing.T, m *Mysql) {
	t.Helper()
	dir := filepath.Join("..", "..", "migrations")
	if err := migrate.ApplySQLDirTracked(m.db, dir); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
}

// seedSeq makes plain seedUser callable multiple times per run
// (uq_users_nickname); tests that pin identity literals use seedUserAs.
var seedSeq int64

func seedUser(t *testing.T, m *Mysql) int64 {
	t.Helper()
	seedSeq++
	uid, err := m.CreateUser(context.Background(),
		fmt.Sprintf("it%d@example.com", seedSeq), fmt.Sprintf("it_user%d", seedSeq), "x", 3, "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return uid
}

// seedUserAs creates a user with a fixed email/nickname (identity-asserting
// tests). Callers must not repeat an identity within one suite run.
func seedUserAs(t *testing.T, m *Mysql, email, nick string) int64 {
	t.Helper()
	uid, err := m.CreateUser(context.Background(), email, nick, "x", 3, "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return uid
}

// ---- 0015 public texture hash dedupe ----

func TestIntegrationPublicTextureDedupe(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	u1, u2 := seedUser(t, m), seedUser(t, m)
	hash := strings.Repeat("a", 64)

	tid, err := m.AddTexture(ctx, u1, "skin", hash, "default", "first")
	if err != nil {
		t.Fatalf("first public texture: %v", err)
	}
	if _, err := m.AddTexture(ctx, u2, "skin", hash, "default", "duplicate"); !errors.Is(err, ErrDuplicatePublic) {
		t.Fatalf("second public texture err = %v, want ErrDuplicatePublic", err)
	}
	if got, ok, err := m.PublicTextureByHash(ctx, "skin", hash); err != nil || !ok || got.ID != tid {
		t.Fatalf("public lookup = %+v ok=%v err=%v, want id %d", got, ok, err, tid)
	}

	// Private copies are allowed and never satisfy the public lookup.
	if _, err := m.AddTexturePrivate(ctx, u2, "skin", hash, "default", "private"); err != nil {
		t.Fatalf("private duplicate: %v", err)
	}
	// Publishing the private copy must collide with the existing public row.
	allow := true
	var privateID int64
	if err := m.db.QueryRowContext(ctx,
		`SELECT id FROM textures WHERE uid=? AND hash=? AND public=0 LIMIT 1`, u2, hash).Scan(&privateID); err != nil {
		t.Fatalf("find private: %v", err)
	}
	if err := m.UpdateTextureMeta(ctx, privateID, u2, nil, &allow); !errors.Is(err, ErrDuplicatePublic) {
		t.Fatalf("publish duplicate err = %v, want ErrDuplicatePublic", err)
	}

	// Legacy duplicate rows (pre-0015, NULL key) do not block startup; the
	// backfill fills exactly one winner and leaves the duplicate NULL.
	legacyHash := strings.Repeat("b", 64)
	for i := 0; i < 2; i++ {
		if _, err := m.db.ExecContext(ctx,
			`INSERT INTO textures (uid,type,hash,model,description,public) VALUES (?,?,?,?,?,1)`,
			u1, "skin", legacyHash, "default", "legacy"); err != nil {
			t.Fatalf("insert legacy duplicate: %v", err)
		}
	}
	if _, err := m.BackfillPublicDedupe(ctx); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	var filled, nulls int
	if err := m.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM textures WHERE hash=? AND public_dedupe IS NOT NULL`, legacyHash).Scan(&filled); err != nil {
		t.Fatal(err)
	}
	if err := m.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM textures WHERE hash=? AND public_dedupe IS NULL`, legacyHash).Scan(&nulls); err != nil {
		t.Fatal(err)
	}
	if filled != 1 || nulls != 1 {
		t.Fatalf("backfill filled=%d nulls=%d, want 1/1", filled, nulls)
	}
}

// ---- 0016 durable plugin jobs ----

func TestIntegrationJobsLifecycle(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	js := jobs.New(m.DB())
	uid := seedUser(t, m)
	now := time.Now()
	mk := func(id string) {
		if err := js.Create(ctx, jobs.Job{
			ID: id, Plugin: "demo", Handler: "batch", OwnerUID: uid,
			State: "queued", Payload: []byte(`{"from":1}`), MaxAttempts: 3,
			RunAfter: now, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("create job %s: %v", id, err)
		}
	}

	mk("job-a")
	claimed, err := js.Claim(ctx, "w1", now, time.Minute)
	if err != nil || claimed == nil || claimed.ID != "job-a" {
		t.Fatalf("claim = %+v %v", claimed, err)
	}
	if ok, err := js.SaveStep(ctx, "job-a", "w1", []byte(`{"page":2}`), 50, "page 2"); err != nil || !ok {
		t.Fatalf("save step ok=%v err=%v", ok, err)
	}
	if err := js.Finish(ctx, "job-a", "w1", []byte(`{"imported":2}`)); err != nil {
		t.Fatal(err)
	}
	if got, _ := js.Get(ctx, "job-a"); got.State != "done" || got.Progress != 100 {
		t.Fatalf("finished job = %+v", got)
	}

	mk("job-b")
	if _, err := js.Claim(ctx, "w1", now, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := js.RequestCancel(ctx, "job-b"); err != nil {
		t.Fatal(err)
	}
	if err := js.MarkCancelled(ctx, "job-b", "w1"); err != nil {
		t.Fatal(err)
	}
	if got, _ := js.Get(ctx, "job-b"); got.State != "cancelled" {
		t.Fatalf("cancelled job = %+v", got)
	}
	if err := js.Resume(ctx, "job-b"); err != nil {
		t.Fatal(err)
	}
	if claimed, err := js.Claim(ctx, "w2", now, time.Minute); err != nil || claimed == nil || claimed.ID != "job-b" {
		t.Fatalf("resumed claim = %+v %v", claimed, err)
	}
}

// ---- 0003 OIDC tables ----

func TestIntegrationOidcRoundTrips(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()

	// clients: upsert -> get -> list -> delete.
	rec := oidc.ClientRecord{
		ID: "it-client", Name: "Integration", SecretHash: "$argon2id$fake",
		RedirectURIs: []string{"https://a/cb"}, GrantTypes: []string{"authorization_code"},
	}
	if err := m.EnsureOidcClient(ctx, rec); err != nil {
		t.Fatalf("ensure client: %v", err)
	}
	got, err := m.GetClient(ctx, "it-client")
	if err != nil || got.Name != "Integration" || len(got.RedirectURIs) != 1 {
		t.Fatalf("get client = %+v %v", got, err)
	}
	if list, err := m.ListClients(ctx); err != nil || len(list) == 0 {
		t.Fatalf("list clients = %v %v", list, err)
	}
	if err := m.DeleteClient(ctx, "it-client"); err != nil {
		t.Fatalf("delete client: %v", err)
	}
	if _, err := m.GetClient(ctx, "it-client"); err != oidc.ErrNotFound {
		t.Fatalf("deleted client err = %v", err)
	}

	uid := seedUser(t, m)

	// access tokens: save -> get -> list live -> revoke one -> list empty.
	uidNum := uid
	at := oidc.AccessTokenRecord{
		JTI: "jti-1", UserID: uidNum, ClientID: "2",
		Scopes:    []string{"openid", "Yggdrasil.Server.Join"},
		ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
	}
	if err := m.SaveAccessToken(ctx, at); err != nil {
		t.Fatalf("save at: %v", err)
	}
	if got, err := m.GetAccessToken(ctx, "jti-1"); err != nil || got.UserID != uidNum {
		t.Fatalf("get at = %+v %v", got, err)
	}
	live, err := m.ListUserAccessTokens(ctx, uidNum)
	if err != nil || len(live) != 1 || live[0].JTI != "jti-1" {
		t.Fatalf("list live = %v %v", live, err)
	}
	if err := m.RevokeAccessToken(ctx, "jti-1"); err != nil {
		t.Fatalf("revoke at: %v", err)
	}
	if live, _ := m.ListUserAccessTokens(ctx, uidNum); len(live) != 0 {
		t.Fatalf("revoked token still live: %v", live)
	}

	// refresh tokens: save -> rotate -> old dead, new live -> client-scoped revoke.
	old := oidc.RefreshTokenRecord{
		Token: "rt-old", UserID: uidNum, ClientID: "2",
		ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
	}
	newTok := oidc.RefreshTokenRecord{
		Token: "rt-new", UserID: uidNum, ClientID: "2",
		ExpiresAt: time.Now().Add(2 * time.Hour), CreatedAt: time.Now(),
	}
	if err := m.SaveRefreshToken(ctx, old); err != nil {
		t.Fatalf("save rt: %v", err)
	}
	if err := m.RotateRefreshToken(ctx, old, newTok); err != nil {
		t.Fatalf("rotate rt: %v", err)
	}
	if got, _ := m.GetRefreshToken(ctx, "rt-old"); !got.Revoked {
		t.Fatal("old refresh not revoked after rotate")
	}
	if got, err := m.GetRefreshToken(ctx, "rt-new"); err != nil || got.Revoked {
		t.Fatalf("new refresh = %+v %v", got, err)
	}
	if err := m.RevokeClientRefreshTokens(ctx, uidNum, "2"); err != nil {
		t.Fatalf("client revoke: %v", err)
	}
	if got, _ := m.GetRefreshToken(ctx, "rt-new"); !got.Revoked {
		t.Fatal("client-scoped revoke missed live refresh")
	}
	// user-wide revoke path
	if err := m.RevokeUserRefreshTokens(ctx, uidNum); err != nil {
		t.Fatalf("user revoke: %v", err)
	}

	// device auth: save -> get by both codes -> update -> duplicate code.
	da := oidc.DeviceAuthRecord{
		DeviceCode: "dev-1", UserCode: "AAAA-BBBB", ClientID: "2",
		Scopes: []string{"openid"}, ExpiresAt: time.Now().Add(10 * time.Minute),
		CreatedAt: time.Now(),
	}
	if err := m.SaveDeviceAuth(ctx, da); err != nil {
		t.Fatalf("save device: %v", err)
	}
	if _, err := m.GetDeviceAuthByUserCode(ctx, "AAAA-BBBB"); err != nil {
		t.Fatalf("get by user code: %v", err)
	}
	da.Done, da.UserID, da.SelectedPID = true, uidNum, 7
	if err := m.UpdateDeviceAuth(ctx, da); err != nil {
		t.Fatalf("update device: %v", err)
	}
	if got, _ := m.GetDeviceAuthByDeviceCode(ctx, "dev-1"); !got.Done || got.SelectedPID != 7 {
		t.Fatalf("device after update = %+v", got)
	}
	dup := da
	dup.DeviceCode = "dev-2"
	if err := m.SaveDeviceAuth(ctx, dup); err == nil {
		t.Fatal("duplicate user_code must fail")
	}
}

// ---- 0004 union_kv + audit + options ----

func TestIntegrationUnionAndAudit(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()

	// union_kv round trip
	if err := m.SetUnionKV(ctx, "k1", "v1"); err != nil {
		t.Fatalf("set kv: %v", err)
	}
	if v, ok, err := m.GetUnionKV(ctx, "k1"); err != nil || !ok || v != "v1" {
		t.Fatalf("get kv = %q %v %v", v, ok, err)
	}
	if err := m.SetUnionKV(ctx, "k1", "v2"); err != nil {
		t.Fatalf("overwrite kv: %v", err)
	}
	if v, _, _ := m.GetUnionKV(ctx, "k1"); v != "v2" {
		t.Fatalf("kv overwrite = %q", v)
	}
	if _, ok, err := m.GetUnionKV(ctx, "missing"); err != nil || ok {
		t.Fatalf("missing kv = %v %v", ok, err)
	}

	// options round trip (member key persistence path)
	if err := m.SetOption(ctx, "union_member_key", "mk-1"); err != nil {
		t.Fatalf("set option: %v", err)
	}
	if v, ok, err := m.GetOption(ctx, "union_member_key"); err != nil || !ok || v != "mk-1" {
		t.Fatalf("get option = %q %v %v", v, ok, err)
	}

	// audit: insert (system + user actor) -> RecentAudit sees them.
	uid := seedUser(t, m)
	m.InsertAudit(ctx, 0, "union.member_key_rotated", map[string]any{"k": "v"})
	m.InsertAudit(ctx, uid, "oidc.device_approve", map[string]any{"pid": 7})
	rows, err := m.RecentAudit(ctx, 10)
	if err != nil || len(rows) < 2 {
		t.Fatalf("recent audit = %v %v", rows, err)
	}
	found := map[string]bool{}
	for _, r := range rows {
		found[r["action"].(string)] = true
	}
	if !found["union.member_key_rotated"] || !found["oidc.device_approve"] {
		t.Fatalf("audit rows missing: %v", found)
	}
}

func TestIntegrationProfileHooks(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	uid := seedUserAs(t, m, "it@example.com", "it_user")

	pid, uuid, err := m.CreatePlayer(ctx, uid, "HookSteve")
	if err != nil || pid == 0 || uuid == "" {
		t.Fatalf("create player = %d %q %v", pid, uuid, err)
	}

	// ProfileNameByUUID powers union POST /sync.
	list, err := m.ProfileNameByUUID(ctx)
	if err != nil || list[uuid] != "HookSteve" {
		t.Fatalf("profile list = %v %v", list, err)
	}

	// EmailByPlayerName powers queryemail.
	if email, ok, err := m.EmailByPlayerName(ctx, "hooksteve"); err != nil || !ok || email != "it@example.com" {
		t.Fatalf("email lookup = %q %v %v", email, ok, err)
	}
	if _, ok, err := m.EmailByPlayerName(ctx, "NoSuchPlayer"); err != nil || ok {
		t.Fatalf("missing player lookup ok=%v err=%v", ok, err)
	}

	// RemapUUIDs applies hub-issued batch; only mapped rows move; counts are
	// truthful (MUA review P1).
	applied, skipped, err := m.RemapUUIDs(ctx, map[string]string{uuid: "cafebabecafebabecafebabecafebabe"})
	if err != nil || applied != 1 || skipped != 0 {
		t.Fatalf("remap applied=%d skipped=%d err=%v", applied, skipped, err)
	}
	if p, err := m.PlayerByUUID(ctx, "cafebabecafebabecafebabecafebabe"); err != nil || p.ID != pid {
		t.Fatalf("after remap = %+v %v", p, err)
	}
	// Unknown old uuid: no error, counted as skipped.
	applied, skipped, err = m.RemapUUIDs(ctx, map[string]string{"0000000000000000000000000000dead": "0000000000000000000000000000beef"})
	if err != nil || applied != 0 || skipped != 1 {
		t.Fatalf("remap unknown applied=%d skipped=%d err=%v", applied, skipped, err)
	}
	// Hub values are normalized (dashed/uppercase used to TRUNCATE into a
	// wrong identity under non-strict sql_mode).
	applied, skipped, err = m.RemapUUIDs(ctx, map[string]string{"cafebabecafebabecafebabecafebabe": "ABCD-ABCD-ABCD-ABCD-ABCD-ABCD-ABCD-ABCD"})
	if err != nil || applied != 1 || skipped != 0 {
		t.Fatalf("remap normalize applied=%d skipped=%d err=%v", applied, skipped, err)
	}
	// Collision-tolerant batch: a merge batch never rolls back everything and
	// never overwrites an existing target row.
	pid2, uuid2, err := m.CreatePlayer(ctx, uid, "HookAlex")
	if err != nil {
		t.Fatal(err)
	}
	applied, skipped, err = m.RemapUUIDs(ctx, map[string]string{uuid2: "abcdabcdabcdabcdabcdabcdabcdabcd"})
	if err != nil || applied != 0 || skipped != 1 {
		t.Fatalf("collision batch applied=%d skipped=%d err=%v", applied, skipped, err)
	}
	if p, err := m.PlayerByUUID(ctx, "abcdabcdabcdabcdabcdabcdabcdabcd"); err != nil || p.ID != pid {
		t.Fatalf("target row must keep original owner: %+v %v", p, err)
	}
	// ConvergeUUID: conflicts are refused ...
	if err := m.ConvergeUUID(ctx, uuid2, "abcdabcdabcdabcdabcdabcdabcdabcd"); !errors.Is(err, ErrUUIDConflict) {
		t.Fatalf("converge conflict = %v", err)
	}
	// ... and a fresh target converges the local row (requesting site must
	// converge before answering ok — MUA review P1).
	if err := m.ConvergeUUID(ctx, uuid2, "0123456789abcdef0123456789abcdef"); err != nil {
		t.Fatalf("converge: %v", err)
	}
	if p, err := m.PlayerByUUID(ctx, "0123456789abcdef0123456789abcdef"); err != nil || p.ID != pid2 {
		t.Fatalf("after converge = %+v %v", p, err)
	}
}

// ---- 0002 sessions ----

func TestIntegrationSessionLifecycle(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	uid := seedUser(t, m)
	tok := "it-session-token"
	if err := m.CreateSession(ctx, tok, uid, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if got, err := m.SessionUser(ctx, tok); err != nil || got != uid {
		t.Fatalf("session user = %d %v", got, err)
	}
	if err := m.DeleteSession(ctx, tok); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if _, err := m.SessionUser(ctx, tok); err == nil {
		t.Fatal("deleted session must not resolve")
	}
}

// ---- login lookup + password verify round trip ----
// Regression anchor for the 2026-10-23 incident: `? COLLATE ...` in
// UserByIdentifier was a syntax error on MySQL 5.7 and made every login fail
// while the register->session path stayed healthy. Pins the lookup and verify
// path against a real database (8.0 here; 5.7 compatibility is a SQL-shape
// property — keep COLLATE on the column expression).

func TestIntegrationLoginRoundTrip(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	uid := seedUserAs(t, m, "login@example.com", "login_user")

	// identifier lookup: nickname hit, case-insensitive email hit, miss
	if u, err := m.UserByIdentifier(ctx, "login_user"); err != nil || u.ID != uid {
		t.Fatalf("by nickname = %+v %v", u, err)
	}
	if u, err := m.UserByIdentifier(ctx, "LOGIN@Example.com"); err != nil || u.ID != uid {
		t.Fatalf("by email ci = %+v %v", u, err)
	}
	if _, err := m.UserByIdentifier(ctx, "missing_user"); err == nil {
		t.Fatal("missing identifier must error")
	}

	// password verify with a real PHC hash (seedUser's "x" placeholder is not
	// a PHC and can never verify) — hash like production does, then verify.
	h, err := auth.HashArgon2id("hunter2-xyz")
	if err != nil {
		t.Fatal(err)
	}
	// Lazy rehash is guarded to legacy rows only (review P3 lost-update fix:
	// an unconditional UPDATE could clobber a concurrent password change),
	// so mark the row as a migrated legacy user first.
	if _, err := m.db.ExecContext(ctx,
		`UPDATE users SET password_algo = 'legacy:bcrypt' WHERE id = ?`, uid); err != nil {
		t.Fatal(err)
	}
	if err := m.RehashPassword(ctx, uid, h); err != nil {
		t.Fatalf("rehash: %v", err)
	}
	var algo string
	if err := m.db.QueryRowContext(ctx, `SELECT password_algo FROM users WHERE id = ?`, uid).Scan(&algo); err != nil || algo != "argon2id" {
		t.Fatalf("rehash must flip marker to argon2id: %q %v", algo, err)
	}
	if ok, err := m.VerifyPassword(ctx, uid, "hunter2-xyz"); err != nil || !ok {
		t.Fatalf("verify ok = %v %v", ok, err)
	}
	if ok, err := m.VerifyPassword(ctx, uid, "wrong-pass"); err != nil || ok {
		t.Fatalf("verify wrong = %v %v", ok, err)
	}
	// Guard: rehash on a non-legacy row is a no-op (doesn't clobber).
	if err := m.RehashPassword(ctx, uid, "ignored-hash"); err != nil {
		t.Fatalf("guarded rehash: %v", err)
	}
	if ok, err := m.VerifyPassword(ctx, uid, "hunter2-xyz"); err != nil || !ok {
		t.Fatalf("guarded rehash must not clobber: %v %v", ok, err)
	}
	// P0 regression (chain-trace 2026-10-23): the password CHANGE path must
	// write unconditionally — even for rows already at argon2id.
	h2, err := auth.HashArgon2id("new-pass-456")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetPasswordHash(ctx, uid, h2); err != nil {
		t.Fatalf("set password: %v", err)
	}
	if ok, err := m.VerifyPassword(ctx, uid, "new-pass-456"); err != nil || !ok {
		t.Fatalf("after change, new password must verify: %v %v", ok, err)
	}
	if ok, err := m.VerifyPassword(ctx, uid, "hunter2-xyz"); err != nil || ok {
		t.Fatalf("after change, old password must fail: %v %v", ok, err)
	}
}

func TestIntegrationInvitationFlow(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()

	codes, err := m.CreateInvitationCodes(ctx, 2, "")
	if err != nil || len(codes) != 2 {
		t.Fatalf("create codes: %v len=%d", err, len(codes))
	}
	one, err := m.CreateInvitationCodes(ctx, 1, "legacy-desc")
	if err != nil || len(one) != 1 || one[0].Description != "legacy-desc" {
		t.Fatalf("single-with-description: %v %+v", err, one)
	}

	got, ok, err := m.FindInvitationCode(ctx, codes[0].Code)
	if err != nil || !ok || got.UsedBy != 0 {
		t.Fatalf("find free: %v ok=%v %+v", err, ok, got)
	}

	uid, err := m.CreateUserWithInvitation(ctx, "inv@example.com", "inv_user", "x", 3, codes[0].Code, "")
	if err != nil {
		t.Fatalf("register with code: %v", err)
	}
	after, ok, err := m.FindInvitationCode(ctx, codes[0].Code)
	if err != nil || !ok || after.UsedBy != uid || after.UsedAt == nil {
		t.Fatalf("code burned: %v ok=%v %+v", err, ok, after)
	}

	// double-use guard: same code again must fail and create no user
	if _, err := m.CreateUserWithInvitation(ctx, "inv2@example.com", "inv_user2", "x", 3, codes[0].Code, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double-use err = %v (want ErrNotFound)", err)
	}
	if _, err := m.UserByIdentifier(ctx, "inv_user2"); err == nil {
		t.Fatal("double-use must not create a user")
	}
	// unknown code
	if _, err := m.CreateUserWithInvitation(ctx, "inv3@example.com", "inv_user3", "x", 3, "nosuch", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown code err = %v (want ErrNotFound)", err)
	}

	if err := m.SetInvitationCodeDescription(ctx, one[0].ID, "new-desc"); err != nil {
		t.Fatalf("edit description: %v", err)
	}
	// same-value edit must be ok (MySQL changed-rows semantics, no false 404)
	if err := m.SetInvitationCodeDescription(ctx, one[0].ID, "new-desc"); err != nil {
		t.Fatalf("idempotent edit: %v", err)
	}
	if err := m.SetInvitationCodeDescription(ctx, 999999, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("edit unknown id err = %v (want ErrNotFound)", err)
	}

	free, used, err := m.ListInvitationCodes(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(free) != 2 || len(used) != 1 || used[0].ID != codes[0].ID {
		t.Fatalf("list split: free=%d used=%d", len(free), len(used))
	}
}

func TestIntegrationCloset(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	uid := seedUser(t, m)
	owner := seedUser(t, m)
	tid, err := m.AddTexture(ctx, owner, "skin", "abc123hash", "", "seed")
	if err != nil {
		t.Fatalf("seed texture: %v", err)
	}

	if created, err := m.AddClosetEntry(ctx, uid, tid, "my-skin"); err != nil || !created {
		t.Fatalf("add = %v %v", created, err)
	}
	// idempotent dup (legacy table has no unique key — app dedupes)
	if created, err := m.AddClosetEntry(ctx, uid, tid, "dup"); err != nil || created {
		t.Fatalf("dup add = %v %v (want created=false)", created, err)
	}
	// unknown texture rejected
	if _, err := m.AddClosetEntry(ctx, uid, 999999, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown texture err = %v", err)
	}

	list, err := m.ListCloset(ctx, uid)
	if err != nil || len(list) != 1 || list[0].ItemName != "my-skin" || list[0].Hash != "abc123hash" {
		t.Fatalf("list = %+v %v", list, err)
	}

	if err := m.SetClosetItemName(ctx, uid, list[0].ID, "renamed"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	// same-value rename ok (changed-rows semantics)
	if err := m.SetClosetItemName(ctx, uid, list[0].ID, "renamed"); err != nil {
		t.Fatalf("idempotent rename: %v", err)
	}
	// foreign uid cannot rename or remove
	other := seedUser(t, m)
	if err := m.SetClosetItemName(ctx, other, list[0].ID, "hijack"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign rename err = %v", err)
	}
	if err := m.RemoveClosetEntry(ctx, other, list[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign remove err = %v", err)
	}

	if err := m.RemoveClosetEntry(ctx, uid, list[0].ID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if list, _ := m.ListCloset(ctx, uid); len(list) != 0 {
		t.Fatalf("not empty: %+v", list)
	}
	// unknown id
	if err := m.RemoveClosetEntry(ctx, uid, 999999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown remove err = %v", err)
	}
}

// ---- system facts (admin status page, B1) ----

func TestIntegrationSystemFacts(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()

	ver, err := m.MySQLVersion(ctx)
	if err != nil || ver == "" {
		t.Fatalf("MySQLVersion = %q %v (want non-empty)", ver, err)
	}
	// watermark = highest applied file (0009_schema_align.sql is the last
	// migration in the chain today)
	mig, err := m.MigrationVersion(ctx)
	if err != nil || mig == "" {
		t.Fatalf("MigrationVersion = %q %v (want non-empty)", mig, err)
	}
	if mig < "0001" {
		t.Fatalf("MigrationVersion = %q, want >= 0001 (lexicographic watermark)", mig)
	}
}

// ---- admin inline edits (B2) ----

func TestIntegrationAdminUserEdits(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	uid := seedUserAs(t, m, "inline-a@example.com", "inline_user_a")
	other := seedUserAs(t, m, "inline-b@example.com", "inline_user_b")

	// email: set + collision + clear-to-NULL
	if err := m.UpdateUserEmail(ctx, uid, "inline-a2@example.com"); err != nil {
		t.Fatalf("set email: %v", err)
	}
	if err := m.UpdateUserEmail(ctx, other, "inline-a2@example.com"); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("email collision err = %v, want ErrDuplicate", err)
	}
	if err := m.UpdateUserEmail(ctx, uid, ""); err != nil {
		t.Fatalf("clear email: %v", err)
	}
	// after clear the address is reusable
	if err := m.UpdateUserEmail(ctx, other, "inline-a2@example.com"); err != nil {
		t.Fatalf("reusable after clear: %v", err)
	}

	// nickname: set + collision
	if err := m.UpdateUserNickname(ctx, uid, "inline_renamed"); err != nil {
		t.Fatalf("set nickname: %v", err)
	}
	if err := m.UpdateUserNickname(ctx, other, "inline_renamed"); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("nickname collision err = %v, want ErrDuplicate", err)
	}

	// verification + permission round-trips
	if err := m.SetEmailVerified(ctx, uid, true); err != nil {
		t.Fatalf("set verified: %v", err)
	}
	if p, err := m.UserPermission(ctx, uid); err != nil || p != 0 {
		t.Fatalf("permission before = %d %v", p, err)
	}
	if err := m.SetUserPermission(ctx, uid, -1); err != nil {
		t.Fatalf("set permission: %v", err)
	}
	if p, _ := m.UserPermission(ctx, uid); p != -1 {
		t.Fatalf("permission after = %d, want -1", p)
	}

	// AdminUsers search: hit + miss (case-insensitive, email or nickname)
	if rows, total, err := m.AdminUsers(ctx, "INLINE_USER_B", 1, 50); err != nil || total != 1 || len(rows) != 1 {
		t.Fatalf("search hit: total=%d rows=%d err=%v", total, len(rows), err)
	} else if rows[0]["id"] != other {
		t.Fatalf("search hit id = %v, want %d", rows[0]["id"], other)
	}
	if _, total, err := m.AdminUsers(ctx, "no-such-user-zzz", 1, 50); err != nil || total != 0 {
		t.Fatalf("search miss: total=%d err=%v", total, err)
	}
	// rows carry permission + email_verified for the admin table
	if rows, _, err := m.AdminUsers(ctx, "", 1, 50); err != nil || len(rows) == 0 {
		t.Fatalf("unfiltered list: %d %v", len(rows), err)
	} else {
		for _, row := range rows {
			if _, ok := row["permission"]; !ok {
				t.Fatalf("AdminUsers row missing permission: %+v", row)
			}
			if _, ok := row["emailVerified"]; !ok {
				t.Fatalf("AdminUsers row missing emailVerified: %+v", row)
			}
		}
	}
}

// ---- admin player management (B3) ----

func TestIntegrationAdminPlayers(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	uid := seedUserAs(t, m, "padmin-a@example.com", "padmin_user_a")
	other := seedUserAs(t, m, "padmin-b@example.com", "padmin_user_b")

	pid, uuid, err := m.CreatePlayer(ctx, uid, "PAdminSteve")
	if err != nil {
		t.Fatalf("create player: %v", err)
	}
	tid, err := m.AddTexture(ctx, uid, "skin", "padminhash00000000000000000000000000000000000000000000000000000", "", "seed")
	if err != nil {
		t.Fatalf("seed texture: %v", err)
	}

	// rename: UUID MUST NOT MOVE (zero-drift iron rule) — uuid_map.uuid
	// stays byte-identical while the name columns follow.
	if err := m.AdminRenamePlayer(ctx, pid, "PAdminRenamed"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	p, err := m.PlayerByID(ctx, pid)
	if err != nil || p.Name != "PAdminRenamed" {
		t.Fatalf("after rename: %+v %v", p, err)
	}
	if p.UUID != uuid {
		t.Fatalf("UUID drifted on rename: before=%s after=%s", uuid, p.UUID)
	}
	// union view follows the rename without touching uuid
	if list, _ := m.ProfileNameByUUID(ctx); list[uuid] != "PAdminRenamed" {
		t.Fatalf("profile list after rename = %v", list)
	}
	// name collision → ErrDuplicate
	pid2, _, err := m.CreatePlayer(ctx, uid, "PAdminOther")
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	if err := m.AdminRenamePlayer(ctx, pid2, "PAdminRenamed"); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("rename collision err = %v, want ErrDuplicate", err)
	}
	// unknown pid → ErrNotFound
	if err := m.AdminRenamePlayer(ctx, 999999, "Whatever"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rename missing err = %v, want ErrNotFound", err)
	}

	// owner change
	if err := m.AdminSetPlayerOwner(ctx, pid, other); err != nil {
		t.Fatalf("owner change: %v", err)
	}
	if p, _ := m.PlayerByID(ctx, pid); p.UID != other {
		t.Fatalf("owner after change = %d, want %d", p.UID, other)
	}

	// texture mount via the shared semantics
	if err := m.SetPlayerTextures(ctx, pid, tid, 0); err != nil {
		t.Fatalf("set textures: %v", err)
	}
	if skin, cape, err := m.PlayerTextureIDs(ctx, pid); err != nil || skin != tid || cape != 0 {
		t.Fatalf("texture ids = %d %d %v", skin, cape, err)
	}

	// list: search by name / owner nickname / owner uid; uid filter
	if rows, total, err := m.AdminPlayers(ctx, "padminrenamed", 0, 1, 50); err != nil || total != 1 || rows[0]["id"] != pid {
		t.Fatalf("search by name: total=%d rows=%+v err=%v", total, rows, err)
	}
	if _, total, err := m.AdminPlayers(ctx, "padmin_user_b", 0, 1, 50); err != nil || total != 1 {
		t.Fatalf("search by owner nickname: total=%d err=%v, want 1 (only the re-homed pid)", total, err)
	}
	if _, total, err := m.AdminPlayers(ctx, "", other, 1, 50); err != nil || total != 1 {
		t.Fatalf("uid filter: total=%d err=%v", total, err)
	}
	// list rows carry preview hashes + uuid
	if rows, _, err := m.AdminPlayers(ctx, "padminrenamed", 0, 1, 50); err != nil {
		t.Fatalf("list: %v", err)
	} else if rows[0]["uuid"] != uuid || rows[0]["skinHash"] != "padminhash00000000000000000000000000000000000000000000000000000" {
		t.Fatalf("list row = %+v", rows[0])
	}

	// delete: uuid_map row + token selection cleaned; texture kept
	if err := m.SaveToken(ctx, tokenRowFor(uid, pid)); err != nil {
		t.Fatalf("seed token: %v", err)
	}
	delUUID, delName, err := m.AdminDeletePlayer(ctx, pid)
	if err != nil || delUUID != uuid || delName != "PAdminRenamed" {
		t.Fatalf("delete return = %q %q %v", delUUID, delName, err)
	}
	if _, err := m.PlayerByID(ctx, pid); !errors.Is(err, ErrNotFound) {
		t.Fatalf("player survived delete: %v", err)
	}
	var nUUIDMap int
	if err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM uuid_map WHERE player_id = ?`, pid).Scan(&nUUIDMap); err != nil || nUUIDMap != 0 {
		t.Fatalf("uuid_map survived delete: n=%d err=%v", nUUIDMap, err)
	}
	var nTex int
	if err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM textures WHERE id = ?`, tid).Scan(&nTex); err != nil || nTex != 1 {
		t.Fatalf("texture row must be kept: n=%d err=%v", nTex, err)
	}
	var sel sql.NullInt64
	if err := m.db.QueryRowContext(ctx, `SELECT selected_pid FROM ygg_tokens WHERE user_id = ?`, uid).Scan(&sel); err != nil {
		t.Fatalf("token lookup: %v", err)
	}
	if sel.Valid {
		t.Fatalf("selected_pid = %d, want NULL after player delete", sel.Int64)
	}
	// deleting a missing player → ErrNotFound
	if _, _, err := m.AdminDeletePlayer(ctx, 999999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing err = %v, want ErrNotFound", err)
	}
}

// tokenRowFor builds a ygg.Token for the delete-cleanup assertion.
func tokenRowFor(uid, pid int64) ygg.Token {
	now := time.Now()
	return ygg.Token{UserID: uid, ClientToken: "padmin-ct", AccessToken: "padmin-at",
		SelectedPID: pid, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
}

// ---- site options consumption (B4) ----

func TestIntegrationSiteOptions(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()

	// defaults with an empty options table (blessing config/options.php)
	if got := m.RegsPerIP(ctx); got != 3 {
		t.Fatalf("RegsPerIP default = %d, want 3", got)
	}
	if got := m.MaxUploadKB(ctx, 2048); got != 1024 {
		t.Fatalf("MaxUploadKB default = %d, want 1024", got)
	}
	if got := m.MaxUploadKB(ctx, 512); got != 512 {
		t.Fatalf("MaxUploadKB clamp to hard ceiling = %d, want 512", got)
	}
	if got := m.Announcement(ctx); got != "" {
		t.Fatalf("Announcement default = %q, want empty", got)
	}

	// overrides + blessing 'true'/'false' spelling tolerance
	if err := m.SetOption(ctx, "regs_per_ip", "5"); err != nil {
		t.Fatalf("set regs_per_ip: %v", err)
	}
	if got := m.RegsPerIP(ctx); got != 5 {
		t.Fatalf("RegsPerIP override = %d, want 5", got)
	}
	if err := m.SetOption(ctx, "max_upload_file_size", "99999"); err != nil {
		t.Fatalf("set max_upload: %v", err)
	}
	if got := m.MaxUploadKB(ctx, 2048); got != 2048 {
		t.Fatalf("MaxUploadKB clamp = %d, want hard ceiling 2048", got)
	}
	if err := m.SetOption(ctx, "announcement", "维护通知：今晚 22:00 重启。"); err != nil {
		t.Fatalf("set announcement: %v", err)
	}
	if got := m.Announcement(ctx); got != "维护通知：今晚 22:00 重启。" {
		t.Fatalf("Announcement = %q", got)
	}

	// per-IP counting backs regs_per_ip (blessing User::where('ip',...)).
	// Two accounts from one address, one from another: buckets stay separate.
	if _, err := m.CreateUser(ctx, "ipcap-b@example.com", "ipcap_user_b", "x", 3, "203.0.113.9"); err != nil {
		t.Fatalf("create with ip: %v", err)
	}
	if _, err := m.CreateUser(ctx, "ipcap-c@example.com", "ipcap_user_c", "x", 3, "203.0.113.9"); err != nil {
		t.Fatalf("create with ip: %v", err)
	}
	if _, err := m.CreateUser(ctx, "ipcap-d@example.com", "ipcap_user_d", "x", 3, "198.51.100.7"); err != nil {
		t.Fatalf("create with other ip: %v", err)
	}
	if n, err := m.CountUsersByIP(ctx, "203.0.113.9"); err != nil || n != 2 {
		t.Fatalf("CountUsersByIP = %d %v, want 2", n, err)
	}
	if n, err := m.CountUsersByIP(ctx, "198.51.100.7"); err != nil || n != 1 {
		t.Fatalf("CountUsersByIP other bucket = %d %v, want 1", n, err)
	}
	// ip='' -> NULL: never lands in any bucket (unknown address is not a
	// shared bucket — a misconfigured proxy degrades to unlimited, not to
	// a whole-site lockout after N registrations)
	if n, err := m.CountUsersByIP(ctx, ""); err != nil || n != 0 {
		t.Fatalf("CountUsersByIP empty ip = %d %v, want 0", n, err)
	}
}

func TestIntegrationValidatePlayerName(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()

	// default rule: official, 3-16
	if err := m.ValidatePlayerName(ctx, "Steve_01"); err != nil {
		t.Fatalf("official valid name rejected: %v", err)
	}
	if err := m.ValidatePlayerName(ctx, "史蒂夫"); err == nil {
		t.Fatal("official rule accepted CJK name")
	}
	if err := m.ValidatePlayerName(ctx, "ab"); err == nil {
		t.Fatal("min length 3 not enforced")
	}
	if err := m.ValidatePlayerName(ctx, "abcdefghijklmnopq"); err == nil { // 17 runes
		t.Fatal("max length 16 not enforced")
	}

	// cjk rule
	if err := m.SetOption(ctx, "player_name_rule", "cjk"); err != nil {
		t.Fatalf("set rule: %v", err)
	}
	if err := m.ValidatePlayerName(ctx, "史蒂夫01"); err != nil {
		t.Fatalf("cjk rule rejected CJK name: %v", err)
	}
	if err := m.ValidatePlayerName(ctx, "bad name"); err == nil {
		t.Fatal("cjk rule accepted whitespace")
	}

	// custom rule (invalid stored regexp fails CLOSED)
	if err := m.SetOption(ctx, "player_name_rule", "custom"); err != nil {
		t.Fatalf("set rule: %v", err)
	}
	if err := m.SetOption(ctx, "custom_player_name_regexp", `^A[0-9]+$`); err != nil {
		t.Fatalf("set custom regexp: %v", err)
	}
	if err := m.ValidatePlayerName(ctx, "A42"); err != nil {
		t.Fatalf("custom rule rejected match: %v", err)
	}
	if err := m.ValidatePlayerName(ctx, "B42"); err == nil {
		t.Fatal("custom rule accepted non-match")
	}
	if err := m.SetOption(ctx, "custom_player_name_regexp", "(["); err != nil {
		t.Fatalf("set bad regexp: %v", err)
	}
	if err := m.ValidatePlayerName(ctx, "A42"); err == nil {
		t.Fatal("broken custom regexp must fail closed")
	}

	// length bounds are configurable (blessing player_name_length_min/max)
	if err := m.SetOption(ctx, "player_name_rule", "official"); err != nil {
		t.Fatalf("reset rule: %v", err)
	}
	if err := m.SetOption(ctx, "player_name_length_min", "2"); err != nil {
		t.Fatalf("set min: %v", err)
	}
	if err := m.ValidatePlayerName(ctx, "ab"); err != nil {
		t.Fatalf("min=2 should accept 2-char name: %v", err)
	}
}

// TestIntegrationPruneUserTokens locks ygg_tokens_limit LRU semantics (B5.2
// 拍板：超限作废最早签发 + 写审计，不拒新签发).
func TestIntegrationPruneUserTokens(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	uid := seedUserAs(t, m, "prune@example.com", "prune_user")

	// 5 issued tokens in known order (id ASC = issuance order)
	var access []string
	for i := 0; i < 5; i++ {
		tok := tokenRowFor(uid, 0)
		tok.AccessToken = fmt.Sprintf("prune-at-%d", i)
		tok.ClientToken = fmt.Sprintf("prune-ct-%d", i)
		if err := m.SaveToken(ctx, tok); err != nil {
			t.Fatalf("save token %d: %v", i, err)
		}
		access = append(access, tok.AccessToken)
	}

	evicted, err := m.PruneUserTokens(ctx, uid, 3)
	if err != nil || evicted != 2 {
		t.Fatalf("prune keep=3: evicted=%d err=%v, want 2", evicted, err)
	}
	// the two EARLIEST tokens are dead, the three latest alive
	for i := 0; i < 2; i++ {
		if tk, err := m.TokenByAccess(ctx, access[i]); err != nil || !tk.Invalidated {
			t.Fatalf("token %d must be evicted (invalidated=%v err=%v)", i, tk.Invalidated, err)
		}
	}
	for i := 2; i < 5; i++ {
		if tk, err := m.TokenByAccess(ctx, access[i]); err != nil || tk.Invalidated {
			t.Fatalf("token %d must survive (invalidated=%v err=%v)", i, tk.Invalidated, err)
		}
	}
	// under-limit is a no-op
	if evicted, err := m.PruneUserTokens(ctx, uid, 10); err != nil || evicted != 0 {
		t.Fatalf("prune keep=10: evicted=%d err=%v, want 0", evicted, err)
	}
	// keep<=0 never prunes (0 = unlimited)
	if evicted, err := m.PruneUserTokens(ctx, uid, 0); err != nil || evicted != 0 {
		t.Fatalf("prune keep=0: evicted=%d err=%v, want 0", evicted, err)
	}
}

// TestIntegrationAllProfileUUIDs covers the union roster-push data source
// (B5.3 Sync button).
func TestIntegrationAllProfileUUIDs(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	uid := seedUserAs(t, m, "roster@example.com", "roster_user")
	pid, uuid, err := m.CreatePlayer(ctx, uid, "RosterSteve")
	if err != nil {
		t.Fatalf("create player: %v", err)
	}
	_ = pid

	roster, err := m.AllProfileUUIDs(ctx)
	if err != nil {
		t.Fatalf("AllProfileUUIDs: %v", err)
	}
	if roster[uuid] != "RosterSteve" {
		t.Fatalf("roster missing pair: %v (want uuid %s -> RosterSteve)", roster, uuid)
	}
}

// TestIntegrationSkinlibOneAndPublicIDs covers the store gate behind the
// server-rendered texture detail page + its sitemap: private textures must
// 404 for anonymous/other viewers (no leak through the SEO surface) and
// never enumerate into PublicTextureIDs.
func TestIntegrationSkinlibOneAndPublicIDs(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	uid := seedUserAs(t, m, "skinlib1@example.com", "skinlib_user1")
	other := seedUserAs(t, m, "skinlib2@example.com", "skinlib_user2")

	// one public skin, one private cape (raw inserts: the suite owns the
	// schema, texture bytes live on disk outside store scope)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := m.db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("seed %s: %v", q, err)
		}
	}
	exec(`INSERT INTO textures (uid, type, hash, model, description, public) VALUES (?, 'skin', 'deadbeef1', 'slim', '公开展示', 1)`, uid)
	exec(`INSERT INTO textures (uid, type, hash, model, description, public) VALUES (?, 'cape', 'deadbeef2', NULL, '私密披风', 0)`, uid)
	var pubID, privID int64
	if err := m.db.QueryRowContext(ctx, `SELECT id FROM textures WHERE hash='deadbeef1'`).Scan(&pubID); err != nil {
		t.Fatal(err)
	}
	if err := m.db.QueryRowContext(ctx, `SELECT id FROM textures WHERE hash='deadbeef2'`).Scan(&privID); err != nil {
		t.Fatal(err)
	}

	// anonymous: public visible, private 404
	if row, err := m.SkinlibOne(ctx, pubID, 0, false); err != nil || row.Name != "公开展示" || row.Model != "slim" || row.Nickname != "skinlib_user1" {
		t.Fatalf("anon public: row=%+v err=%v", row, err)
	}
	if _, err := m.SkinlibOne(ctx, privID, 0, false); err != ErrNotFound {
		t.Fatalf("anon private err = %v, want ErrNotFound", err)
	}
	// other logged-in user: still 404
	if _, err := m.SkinlibOne(ctx, privID, other, false); err != ErrNotFound {
		t.Fatalf("other-user private err = %v, want ErrNotFound", err)
	}
	// owner: visible
	if row, err := m.SkinlibOne(ctx, privID, uid, false); err != nil || !row.Public {
		t.Fatalf("owner private: row=%+v err=%v", row, err)
	}
	// admin: visible
	if row, err := m.SkinlibOne(ctx, privID, 999999, true); err != nil || row.TID != privID {
		t.Fatalf("admin private: row=%+v err=%v", row, err)
	}
	// missing id
	if _, err := m.SkinlibOne(ctx, 999999999, 0, false); err != ErrNotFound {
		t.Fatalf("missing err = %v, want ErrNotFound", err)
	}

	// sitemap enumeration: public only, ascending, bounded
	ids, err := m.PublicTextureIDs(ctx, 5000)
	if err != nil {
		t.Fatalf("PublicTextureIDs: %v", err)
	}
	foundPub, foundPriv := false, false
	for i, e := range ids {
		if e.TID == pubID {
			foundPub = true
		}
		if e.TID == privID {
			foundPriv = true
		}
		if i > 0 && ids[i-1].TID > e.TID {
			t.Fatalf("PublicTextureIDs not ascending at %d", i)
		}
	}
	if !foundPub || foundPriv {
		t.Fatalf("enumeration leak/miss: public=%v private=%v", foundPub, foundPriv)
	}
	// limit respected
	if ids2, err := m.PublicTextureIDs(ctx, 1); err != nil || len(ids2) != 1 {
		t.Fatalf("limit=1: %v err=%v", ids2, err)
	}
}
