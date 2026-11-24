package store

import (
	"context"
	"testing"
	"time"

	"skin-nexus/server/internal/oidc"
	"skin-nexus/server/internal/ygg"
)

func TestSQLiteBannedUserProjectionPreservesIndependentJoinFlag(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid, err := st.CreateUser(ctx, "banned@example.test", "Banned", "hash", 3, "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	assertJoin := func(want bool) {
		t.Helper()
		byID, err := st.UserByID(ctx, uid)
		if err != nil {
			t.Fatalf("UserByID: %v", err)
		}
		byIdentifier, err := st.UserByIdentifier(ctx, "BANNED")
		if err != nil {
			t.Fatalf("UserByIdentifier: %v", err)
		}
		if byID.CanJoinServer != want || byIdentifier.CanJoinServer != want {
			t.Fatalf("banned projection by id/identifier = %v/%v, want %v/%v", byID.CanJoinServer, byIdentifier.CanJoinServer, want, want)
		}
	}
	readStoredFlag := func() bool {
		t.Helper()
		var flag bool
		if err := st.DB().QueryRow(`SELECT can_join_server FROM users WHERE id = ?`, uid).Scan(&flag); err != nil {
			t.Fatalf("read stored can_join_server: %v", err)
		}
		return flag
	}

	if err := st.SetUserPermission(ctx, uid, -1); err != nil {
		t.Fatalf("ban: %v", err)
	}
	assertJoin(false)
	if !readStoredFlag() {
		t.Fatal("ban must not overwrite the independent can_join_server column")
	}
	if err := st.SetUserPermission(ctx, uid, 0); err != nil {
		t.Fatalf("unban: %v", err)
	}
	assertJoin(true)

	if _, err := st.DB().Exec(`UPDATE users SET can_join_server = 0 WHERE id = ?`, uid); err != nil {
		t.Fatalf("clear independent join flag: %v", err)
	}
	if err := st.SetUserPermission(ctx, uid, -1); err != nil {
		t.Fatalf("ban again: %v", err)
	}
	if err := st.SetUserPermission(ctx, uid, 0); err != nil {
		t.Fatalf("unban again: %v", err)
	}
	assertJoin(false)
	if readStoredFlag() {
		t.Fatal("unban must not turn an independently disabled join flag back on")
	}
}

func TestSQLiteRevokeUserAccessRevokesAllCredentialFamilies(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid, err := st.CreateUser(ctx, "revoked@example.test", "Revoked", "hash", 3, "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	otherUID, err := st.CreateUser(ctx, "other@example.test", "Other", "hash", 3, "")
	if err != nil {
		t.Fatalf("create other user: %v", err)
	}
	_, uuid1, err := st.CreatePlayer(ctx, uid, "RevokedOne")
	if err != nil {
		t.Fatalf("create first player: %v", err)
	}
	_, uuid2, err := st.CreatePlayer(ctx, uid, "RevokedTwo")
	if err != nil {
		t.Fatalf("create second player: %v", err)
	}
	_, otherUUID, err := st.CreatePlayer(ctx, otherUID, "OtherPlayer")
	if err != nil {
		t.Fatalf("create other player: %v", err)
	}
	now := time.Now().UTC()
	for _, token := range []ygg.Token{
		{UserID: uid, ClientToken: "client-a", AccessToken: "opaque-a", CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
		{UserID: uid, ClientToken: "client-b", AccessToken: "opaque-b", CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
		{UserID: otherUID, ClientToken: "client-other", AccessToken: "opaque-other", CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
	} {
		if err := st.SaveToken(ctx, token); err != nil {
			t.Fatalf("save opaque token: %v", err)
		}
	}
	for _, rec := range []oidc.AccessTokenRecord{
		{JTI: "access-a", UserID: uid, ClientID: "2", Scopes: []string{oidc.ScopeYggdrasilJoin}, ExpiresAt: now.Add(time.Hour), CreatedAt: now},
		{JTI: "access-other", UserID: otherUID, ClientID: "2", Scopes: []string{oidc.ScopeYggdrasilJoin}, ExpiresAt: now.Add(time.Hour), CreatedAt: now},
	} {
		if err := st.SaveAccessToken(ctx, rec); err != nil {
			t.Fatalf("save OIDC access token: %v", err)
		}
	}
	for _, rec := range []oidc.RefreshTokenRecord{
		{Token: "refresh-a", UserID: uid, ClientID: "2", ExpiresAt: now.Add(24 * time.Hour), CreatedAt: now},
		{Token: "refresh-other", UserID: otherUID, ClientID: "2", ExpiresAt: now.Add(24 * time.Hour), CreatedAt: now},
	} {
		if err := st.SaveRefreshToken(ctx, rec); err != nil {
			t.Fatalf("save OIDC refresh token: %v", err)
		}
	}
	for _, join := range []ygg.JoinSession{
		{AccessToken: "opaque-a", UUID: uuid1, ServerID: "server-a", CreatedAt: now, ExpiresAt: now.Add(time.Minute)},
		{AccessToken: "opaque-b", UUID: uuid2, ServerID: "server-b", CreatedAt: now, ExpiresAt: now.Add(time.Minute)},
		{AccessToken: "opaque-other", UUID: otherUUID, ServerID: "server-other", CreatedAt: now, ExpiresAt: now.Add(time.Minute)},
	} {
		if err := st.SaveJoin(ctx, join); err != nil {
			t.Fatalf("save join session: %v", err)
		}
	}

	revoker, ok := any(st).(interface {
		RevokeUserAccess(context.Context, int64) error
	})
	if !ok {
		t.Fatal("store does not implement RevokeUserAccess")
	}
	if err := revoker.RevokeUserAccess(ctx, uid); err != nil {
		t.Fatalf("RevokeUserAccess: %v", err)
	}

	for _, raw := range []string{"opaque-a", "opaque-b"} {
		token, err := st.TokenByAccess(ctx, raw)
		if err != nil || !token.Invalidated {
			t.Fatalf("opaque token %s invalidated = %v, err = %v", raw, token.Invalidated, err)
		}
	}
	otherToken, err := st.TokenByAccess(ctx, "opaque-other")
	if err != nil || otherToken.Invalidated {
		t.Fatalf("other user's opaque token invalidated = %v, err = %v", otherToken.Invalidated, err)
	}
	access, err := st.GetAccessToken(ctx, "access-a")
	if err != nil || access.RevokedAt.IsZero() {
		t.Fatalf("OIDC access token revoked = %v, err = %v", access.RevokedAt, err)
	}
	refresh, err := st.GetRefreshToken(ctx, "refresh-a")
	if err != nil || !refresh.Revoked {
		t.Fatalf("OIDC refresh token revoked = %v, err = %v", refresh.Revoked, err)
	}
	otherAccess, err := st.GetAccessToken(ctx, "access-other")
	if err != nil || !otherAccess.RevokedAt.IsZero() {
		t.Fatalf("other user's OIDC access token revoked = %v, err = %v", otherAccess.RevokedAt, err)
	}
	otherRefresh, err := st.GetRefreshToken(ctx, "refresh-other")
	if err != nil || otherRefresh.Revoked {
		t.Fatalf("other user's OIDC refresh token revoked = %v, err = %v", otherRefresh.Revoked, err)
	}

	for _, uuid := range []string{uuid1, uuid2} {
		joins, err := st.FindLiveJoins(ctx, uuid, now)
		if err != nil || len(joins) != 0 {
			t.Fatalf("revoked user's joins for %s = %d, err = %v", uuid, len(joins), err)
		}
	}
	joins, err := st.FindLiveJoins(ctx, otherUUID, now)
	if err != nil || len(joins) != 1 {
		t.Fatalf("other user's joins = %d, err = %v", len(joins), err)
	}
}

func TestSQLiteRevokeUserAccessRollsBackOnJoinDeleteFailure(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid, err := st.CreateUser(ctx, "rollback@example.test", "Rollback", "hash", 3, "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	_, uuid, err := st.CreatePlayer(ctx, uid, "RollbackPlayer")
	if err != nil {
		t.Fatalf("create player: %v", err)
	}
	now := time.Now().UTC()
	if err := st.SaveToken(ctx, ygg.Token{UserID: uid, AccessToken: "opaque", ClientToken: "client", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatalf("save token: %v", err)
	}
	if err := st.SaveAccessToken(ctx, oidc.AccessTokenRecord{JTI: "access", UserID: uid, ClientID: "2", ExpiresAt: now.Add(time.Hour), CreatedAt: now}); err != nil {
		t.Fatalf("save access token: %v", err)
	}
	if err := st.SaveRefreshToken(ctx, oidc.RefreshTokenRecord{Token: "refresh", UserID: uid, ClientID: "2", ExpiresAt: now.Add(time.Hour), CreatedAt: now}); err != nil {
		t.Fatalf("save refresh token: %v", err)
	}
	if err := st.SaveJoin(ctx, ygg.JoinSession{AccessToken: "opaque", UUID: uuid, ServerID: "server", CreatedAt: now, ExpiresAt: now.Add(time.Minute)}); err != nil {
		t.Fatalf("save join: %v", err)
	}
	if _, err := st.DB().Exec(`CREATE TRIGGER fail_join_delete BEFORE DELETE ON ygg_join_sessions BEGIN SELECT RAISE(ABORT, 'forced join delete failure'); END`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	revoker, ok := any(st).(interface {
		RevokeUserAccess(context.Context, int64) error
	})
	if !ok {
		t.Fatal("store does not implement RevokeUserAccess")
	}
	if err := revoker.RevokeUserAccess(ctx, uid); err == nil {
		t.Fatal("RevokeUserAccess must report the forced join-delete failure")
	}
	token, err := st.TokenByAccess(ctx, "opaque")
	if err != nil || token.Invalidated {
		t.Fatalf("transaction did not roll back opaque revocation: invalidated=%v err=%v", token.Invalidated, err)
	}
	access, err := st.GetAccessToken(ctx, "access")
	if err != nil || !access.RevokedAt.IsZero() {
		t.Fatalf("transaction did not roll back OIDC access revocation: revoked=%v err=%v", access.RevokedAt, err)
	}
	refresh, err := st.GetRefreshToken(ctx, "refresh")
	if err != nil || refresh.Revoked {
		t.Fatalf("transaction did not roll back OIDC refresh revocation: revoked=%v err=%v", refresh.Revoked, err)
	}
}
