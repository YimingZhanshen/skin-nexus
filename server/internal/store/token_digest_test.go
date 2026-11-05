package store

// Credentials at rest. The independent source of truth here is the digest
// literal, computed by hand before the code existed:
//
//	printf '%s' '<value>' | sha256sum
//
// Asserting "the column equals sha256(column-input)" computed in Go would only
// re-run the implementation and could never fail.

import (
	"context"
	"errors"
	"testing"
	"time"

	"skin-nexus/server/internal/ygg"
)

const (
	rawAccessToken     = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	digestAccessToken  = "ffe054fe7ae0cb6dc65c3af9b61d5209f439851db43d0ba5997337df154668eb"
	rawSessionToken    = "session-token-under-test"
	digestSessionToken = "58aa88018cd7454a9ab8168d6f45035a583c34a6bb63853a0225e2907844a9cc"
	rawLauncherToken   = "live-launcher-token"
	digestLauncherTok  = "d6e48f1926394445e2b52330b32562c5b1c5b8c888a2d9e142ee72dbb2c96217"
	// Exactly 32: `ygg_tokens.client_token` is CHAR(32), which SQLite does not
	// enforce and MySQL/MariaDB/PostgreSQL do — a 33-char fixture here passed the
	// SQLite-only leg for a whole day and was rejected by the engine legs.
	testClientToken32 = "0123456789abcdef0123456789abcdef"
)

func selectString(t *testing.T, st *Mysql, query string, args ...any) string {
	t.Helper()
	var s string
	if err := st.db.QueryRowContext(context.Background(), query, args...).Scan(&s); err != nil {
		t.Fatalf("scan %s: %v", query, err)
	}
	return s
}

func countRows(t *testing.T, st *Mysql, table string) int {
	t.Helper()
	var n int
	if err := st.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM `+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func TestSQLiteEngineTokenRowHoldsADigestNotTheToken(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid := newMeasureUser(t, st, "tok@example.com", "tokuser")
	now := time.Now().UTC()

	if err := st.SaveToken(ctx, ygg.Token{
		AccessToken: rawAccessToken, ClientToken: testClientToken32, UserID: uid,
		SelectedPID: 0, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	stored := selectString(t, st, `SELECT access_token FROM ygg_tokens WHERE user_id = ?`, uid)
	if stored == rawAccessToken {
		t.Fatal("the access token is recoverable from the row — a table dump is a list of live sessions")
	}
	if stored != digestAccessToken {
		t.Errorf("stored = %q, want the hand-computed sha256 %q", stored, digestAccessToken)
	}

	got, err := st.TokenByAccess(ctx, rawAccessToken)
	if err != nil {
		t.Fatalf("lookup by the presented token must still work: %v", err)
	}
	if got.UserID != uid {
		t.Errorf("resolved uid = %d, want %d", got.UserID, uid)
	}
	if got.AccessToken != rawAccessToken {
		t.Errorf("AccessToken = %q, want the value the caller presented (the row cannot produce it)", got.AccessToken)
	}
	// client_token stays verbatim on purpose (digest.go): it cannot authenticate
	// alone, and Yggdrasil requires the server to echo it back.
	if got.ClientToken != testClientToken32 {
		t.Errorf("ClientToken = %q, want it echoed verbatim", got.ClientToken)
	}

	// A lookup must digest exactly once. If some caller started passing the stored
	// digest, the double form would silently match nothing.
	if _, err := st.TokenByAccess(ctx, digestAccessToken); !errors.Is(err, ErrNotFound) {
		t.Errorf("lookup by the stored digest = %v, want ErrNotFound", err)
	}

	if err := st.InvalidateToken(ctx, rawAccessToken); err != nil {
		t.Fatalf("invalidate: %v", err)
	}
	if tk, err := st.TokenByAccess(ctx, rawAccessToken); err != nil || !tk.Invalidated {
		t.Errorf("after invalidate: %+v %v, want the row marked", tk, err)
	}
}

func TestSQLiteEngineWebSessionIsKeyedOnADigest(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid := newMeasureUser(t, st, "sess@example.com", "sessuser")
	other := newMeasureUser(t, st, "sess2@example.com", "sess2user")
	expiry := time.Now().UTC().Add(2 * time.Hour)

	if err := st.CreateSession(ctx, rawSessionToken, uid, expiry); err != nil {
		t.Fatalf("create session: %v", err)
	}
	stored := selectString(t, st, `SELECT token FROM web_sessions WHERE user_id = ?`, uid)
	if stored != digestSessionToken {
		t.Errorf("stored = %q, want the hand-computed sha256 %q", stored, digestSessionToken)
	}
	if got, err := st.SessionUser(ctx, rawSessionToken); err != nil || got != uid {
		t.Fatalf("SessionUser(presented) = %d %v, want %d", got, err, uid)
	}
	if _, err := st.SessionUser(ctx, digestSessionToken); !errors.Is(err, ErrNotFound) {
		t.Errorf("SessionUser(digest) = %v, want ErrNotFound", err)
	}

	// The cookie a browser sends is the raw value, so the survivor of a
	// "keep this one, kill the rest" purge has to match on the digest form.
	second := "another-session-token"
	if err := st.CreateSession(ctx, second, uid, expiry); err != nil {
		t.Fatalf("create second session: %v", err)
	}
	if err := st.CreateSession(ctx, "someone-elses-session", other, expiry); err != nil {
		t.Fatalf("create other session: %v", err)
	}
	if err := st.DeleteUserSessionsExcept(ctx, uid, rawSessionToken); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if _, err := st.SessionUser(ctx, rawSessionToken); err != nil {
		t.Errorf("the caller's own session was purged: %v", err)
	}
	if _, err := st.SessionUser(ctx, second); !errors.Is(err, ErrNotFound) {
		t.Errorf("the other session of the same account survived: %v", err)
	}
	if _, err := st.SessionUser(ctx, "someone-elses-session"); err != nil {
		t.Errorf("the purge crossed accounts: %v", err)
	}

	if err := st.DeleteSession(ctx, rawSessionToken); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := st.SessionUser(ctx, rawSessionToken); !errors.Is(err, ErrNotFound) {
		t.Errorf("after logout the session still resolves: %v", err)
	}
}

func TestSQLiteEngineJoinSessionStoresADigestAndNeverReturnsOneAsAToken(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid := newMeasureUser(t, st, "join@example.com", "joinuser")
	_ = uid
	now := time.Now().UTC()

	if err := st.SaveJoin(ctx, ygg.JoinSession{
		AccessToken: rawLauncherToken, UUID: "069a79f444e94726a5befca90e38aaf5",
		ServerID: "srv-1", ServerIP: "203.0.113.9", CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("save join: %v", err)
	}
	stored := selectString(t, st, `SELECT access_token FROM ygg_join_sessions WHERE server_id = ?`, "srv-1")
	if stored != digestLauncherTok {
		t.Errorf("stored = %q, want the hand-computed sha256 %q", stored, digestLauncherTok)
	}

	j, err := st.JoinByTokenServer(ctx, rawLauncherToken, "srv-1")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if j.AccessToken != rawLauncherToken {
		t.Errorf("JoinSession.AccessToken = %q, want the presented value, never the stored form", j.AccessToken)
	}
	// The scanner must not hand back a digest in a field named AccessToken:
	// one caller mistake away from that digest being treated as a token.
	live, err := st.FindLiveJoins(ctx, "069a79f444e94726a5befca90e38aaf5", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("live joins: %v", err)
	}
	if len(live) != 1 {
		t.Fatalf("live joins = %d, want 1", len(live))
	}
	if live[0].AccessToken != "" {
		t.Errorf("FindLiveJoins populated AccessToken with %q; the column is a digest and must not be read as a token", live[0].AccessToken)
	}
}

func TestSQLiteEngineEnsureTokenDigestsRewritesExistingRowsOnce(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid := newMeasureUser(t, st, "old@example.com", "olduser")
	now := time.Now().UTC()

	// Rows written before this change existed: raw credentials, inserted here
	// straight past the store's own API, which is exactly the state a real
	// upgrade finds.
	if _, err := st.db.ExecContext(ctx,
		`INSERT INTO ygg_tokens (user_id, client_token, access_token, created_at, expires_at, invalidated) VALUES (?,?,?,?,?,0)`,
		uid, testClientToken32, rawAccessToken, now, now.Add(time.Hour)); err != nil {
		t.Fatalf("plant token: %v", err)
	}
	if _, err := st.db.ExecContext(ctx,
		`INSERT INTO web_sessions (token, user_id, expires_at) VALUES (?,?,?)`,
		rawSessionToken, uid, now.Add(time.Hour)); err != nil {
		t.Fatalf("plant session: %v", err)
	}
	if _, err := st.db.ExecContext(ctx,
		`INSERT INTO ygg_join_sessions (access_token, uuid, server_id, server_ip, created_at, expires_at) VALUES (?,?,?,?,?,?)`,
		rawLauncherToken, "069a79f444e94726a5befca90e38aaf5", "srv-2", "203.0.113.9", now, now.Add(time.Hour)); err != nil {
		t.Fatalf("plant join: %v", err)
	}
	before := countRows(t, st, "ygg_tokens") + countRows(t, st, "web_sessions") + countRows(t, st, "ygg_join_sessions")

	// Nothing resolves while the rows are raw and the code looks up digests —
	// that is the whole reason the rewrite has to happen at boot, not lazily.
	rewritten, err := st.EnsureTokenDigests(ctx)
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if rewritten != 3 {
		t.Errorf("rewrote %d rows, want 3 (one per credential column)", rewritten)
	}
	after := countRows(t, st, "ygg_tokens") + countRows(t, st, "web_sessions") + countRows(t, st, "ygg_join_sessions")
	if after != before {
		t.Errorf("row count changed by the rewrite: %d → %d", before, after)
	}
	if got := selectString(t, st, `SELECT access_token FROM ygg_tokens WHERE user_id = ?`, uid); got != digestAccessToken {
		t.Errorf("token column after rewrite = %q, want %q", got, digestAccessToken)
	}
	if got := selectString(t, st, `SELECT token FROM web_sessions WHERE user_id = ?`, uid); got != digestSessionToken {
		t.Errorf("session column after rewrite = %q, want %q", got, digestSessionToken)
	}
	// The upgraded database must serve the same tokens the client already holds.
	if _, err := st.TokenByAccess(ctx, rawAccessToken); err != nil {
		t.Errorf("in-flight token broke across the rewrite: %v", err)
	}
	if got, err := st.SessionUser(ctx, rawSessionToken); err != nil || got != uid {
		t.Errorf("in-flight session broke: %d %v", got, err)
	}
	if _, err := st.JoinByTokenServer(ctx, rawLauncherToken, "srv-2"); err != nil {
		t.Errorf("in-flight join broke: %v", err)
	}

	// Second boot: the marker says the work is done. Re-running would digest the
	// digests and log out every account on the site.
	again, err := st.EnsureTokenDigests(ctx)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if again != 0 {
		t.Errorf("second pass rewrote %d rows, want 0", again)
	}
	if got := selectString(t, st, `SELECT access_token FROM ygg_tokens WHERE user_id = ?`, uid); got != digestAccessToken {
		t.Errorf("token column after the second pass = %q, want it unchanged", got)
	}
	if _, err := st.TokenByAccess(ctx, rawAccessToken); err != nil {
		t.Errorf("the second pass broke the token anyway: %v", err)
	}

	// A token issued *after* the rewrite is digested once, like any other.
	newRaw := "brand-new-token-after-upgrade"
	if err := st.SaveToken(ctx, ygg.Token{AccessToken: newRaw, ClientToken: testClientToken32, UserID: uid,
		CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatalf("save after rewrite: %v", err)
	}
	if _, err := st.TokenByAccess(ctx, newRaw); err != nil {
		t.Errorf("post-rewrite token unreadable: %v", err)
	}
}

// fsck cannot tell a raw token from a digest by looking at the value — that is
// the whole reason the rewrite is single-shot instead of a dual-format read — so
// what it reports is the marker plus how many rows the rewrite would have had to
// cover. Both halves are asserted, because each one alone would let a healthy-
// looking line sit next to a table full of live sessions.
func TestSQLiteEngineFSCKReportsWhetherCredentialsWereRewritten(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid := newMeasureUser(t, st, "fsck-cred@example.com", "fsckcred")
	now := time.Now().UTC()

	rows, migrated, err := st.FSCKCredentialDigestState(ctx)
	if err != nil {
		t.Fatalf("empty database: %v", err)
	}
	if rows != 0 || migrated {
		t.Errorf("empty database = (%d, %v), want (0, false)", rows, migrated)
	}

	plant := func(table, sql string, args ...any) {
		t.Helper()
		if _, err := st.db.ExecContext(ctx, sql, args...); err != nil {
			t.Fatalf("plant %s: %v", table, err)
		}
	}
	plant("ygg_tokens",
		`INSERT INTO ygg_tokens (user_id, client_token, access_token, created_at, expires_at, invalidated) VALUES (?,?,?,?,?,0)`,
		uid, testClientToken32, rawAccessToken, now, now.Add(time.Hour))
	plant("web_sessions", `INSERT INTO web_sessions (token, user_id, created_at, expires_at) VALUES (?,?,?,?)`,
		rawSessionToken, uid, now, now.Add(time.Hour))

	rows, migrated, err = st.FSCKCredentialDigestState(ctx)
	if err != nil {
		t.Fatalf("pre-upgrade state: %v", err)
	}
	if rows != 2 || migrated {
		t.Errorf("raw credentials on site = (%d, %v), want (2, false) — this is the state an upgrade finds", rows, migrated)
	}

	if n, err := st.EnsureTokenDigests(ctx); err != nil || n != 2 {
		t.Fatalf("rewrite = %d %v, want 2", n, err)
	}
	rows, migrated, err = st.FSCKCredentialDigestState(ctx)
	if err != nil {
		t.Fatalf("post-upgrade state: %v", err)
	}
	if rows != 2 || !migrated {
		t.Errorf("after the rewrite = (%d, %v), want (2, true)", rows, migrated)
	}

	// The marker makes the work single-shot: a raw row that arrives afterwards
	// (a restored partial backup, an old binary still writing) is reported as
	// covered by the count but is not repaired, and fsck must not claim otherwise.
	plant("ygg_join_sessions",
		`INSERT INTO ygg_join_sessions (access_token, uuid, server_id, server_ip, created_at, expires_at) VALUES (?,?,?,?,?,?)`,
		rawLauncherToken, "069a79f444e94726a5befca90e38aaf5", "srv-fsck", "203.0.113.9", now, now.Add(time.Hour))
	rows, migrated, err = st.FSCKCredentialDigestState(ctx)
	if err != nil {
		t.Fatalf("late raw row: %v", err)
	}
	if rows != 3 || !migrated {
		t.Errorf("late raw row = (%d, %v), want (3, true): the marker stays, the row is not rewritten", rows, migrated)
	}
}
