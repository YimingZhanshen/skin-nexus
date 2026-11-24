package adminapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"skin-nexus/server/internal/oidc"
	"skin-nexus/server/internal/ygg"
)

func TestBanRevokeFailureRollsBackPermissionAndCredentials(t *testing.T) {
	b := newDiagBackend(t)
	st := b.Store
	ctx := context.Background()
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	adminUID, err := st.CreateUserAdmin(ctx, "admin@example.test", "Admin", string(passwordHash), 3)
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	targetUID, err := st.CreateUser(ctx, "target@example.test", "Target", string(passwordHash), 3, "")
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	_, playerUUID, err := st.CreatePlayer(ctx, targetUID, "TargetPlayer")
	if err != nil {
		t.Fatalf("create player: %v", err)
	}
	now := time.Now().UTC()
	if err := st.SaveToken(ctx, ygg.Token{UserID: targetUID, AccessToken: "opaque", ClientToken: "client", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatalf("save opaque token: %v", err)
	}
	if err := st.SaveAccessToken(ctx, oidc.AccessTokenRecord{JTI: "access", UserID: targetUID, ClientID: "2", ExpiresAt: now.Add(time.Hour), CreatedAt: now}); err != nil {
		t.Fatalf("save OIDC access token: %v", err)
	}
	if err := st.SaveRefreshToken(ctx, oidc.RefreshTokenRecord{Token: "refresh", UserID: targetUID, ClientID: "2", ExpiresAt: now.Add(time.Hour), CreatedAt: now}); err != nil {
		t.Fatalf("save OIDC refresh token: %v", err)
	}
	if err := st.SaveJoin(ctx, ygg.JoinSession{AccessToken: "opaque", UUID: playerUUID, ServerID: "server", CreatedAt: now, ExpiresAt: now.Add(time.Minute)}); err != nil {
		t.Fatalf("save join: %v", err)
	}
	if _, err := st.DB().Exec(`CREATE TRIGGER fail_join_delete BEFORE DELETE ON ygg_join_sessions BEGIN SELECT RAISE(ABORT, 'forced join delete failure'); END`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/admin/v1/users/%d/permission", targetUID), strings.NewReader(`{"permission":-1}`))
	req.SetPathValue("id", strconv.FormatInt(targetUID, 10))
	req = req.WithContext(context.WithValue(req.Context(), actorUIDKey{}, adminUID))
	rec := httptest.NewRecorder()
	b.handleUserSetPermission(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("ban with forced revoke failure = %d %s, want 500", rec.Code, rec.Body.String())
	}
	permission, err := st.UserPermission(ctx, targetUID)
	if err != nil || permission != 0 {
		t.Fatalf("failed ban permission = %d, err = %v; want original 0", permission, err)
	}
	token, err := st.TokenByAccess(ctx, "opaque")
	if err != nil || token.Invalidated {
		t.Fatalf("failed ban changed opaque token: invalidated=%v err=%v", token.Invalidated, err)
	}
	access, err := st.GetAccessToken(ctx, "access")
	if err != nil || !access.RevokedAt.IsZero() {
		t.Fatalf("failed ban changed OIDC access token: revoked=%v err=%v", access.RevokedAt, err)
	}
	refresh, err := st.GetRefreshToken(ctx, "refresh")
	if err != nil || refresh.Revoked {
		t.Fatalf("failed ban changed OIDC refresh token: revoked=%v err=%v", refresh.Revoked, err)
	}
	joins, err := st.FindLiveJoins(ctx, playerUUID, now)
	if err != nil || len(joins) != 1 {
		t.Fatalf("failed ban changed join sessions: count=%d err=%v", len(joins), err)
	}
}

func TestYggValidateAndHasJoinedFailClosedForBannedAccount(t *testing.T) {
	b := newDiagBackend(t)
	st := b.Store
	ctx := context.Background()
	uid, err := st.CreateUser(ctx, "banned@example.test", "Banned", "hash", 3, "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	_, playerUUID, err := st.CreatePlayer(ctx, uid, "BannedPlayer")
	if err != nil {
		t.Fatalf("create player: %v", err)
	}
	signer, err := ygg.NewLocalSigner(filepath.Join(t.TempDir(), "sign-key.pem"))
	if err != nil {
		t.Fatalf("create signer: %v", err)
	}
	svc := ygg.NewService(st, signer)
	now := time.Now().UTC()
	token := ygg.Token{UserID: uid, AccessToken: "still-live", ClientToken: "client", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := st.SaveToken(ctx, token); err != nil {
		t.Fatalf("save token: %v", err)
	}
	if err := st.SaveJoin(ctx, ygg.JoinSession{AccessToken: token.AccessToken, UUID: playerUUID, ServerID: "server", CreatedAt: now, ExpiresAt: now.Add(time.Minute)}); err != nil {
		t.Fatalf("save join: %v", err)
	}
	// Bypass revocation deliberately: the read paths must enforce the ban even
	// when a live credential and join session are still present.
	if _, err := st.DB().Exec(`UPDATE users SET permission = -1 WHERE id = ?`, uid); err != nil {
		t.Fatalf("ban directly: %v", err)
	}
	t.Run("validate", func(t *testing.T) {
		if err := svc.Validate(ctx, token.AccessToken, token.ClientToken); err == nil {
			t.Fatal("Validate accepted a live token belonging to a banned account")
		}
	})
	t.Run("hasJoined", func(t *testing.T) {
		if _, err := svc.HasJoined(ctx, "BannedPlayer", "server", ""); err == nil {
			t.Fatal("HasJoined accepted a live join belonging to a banned account")
		}
	})
}

func TestBanRevokesYggAccessAndUnbanPreservesJoinFlag(t *testing.T) {
	for _, independentJoin := range []bool{true, false} {
		t.Run(fmt.Sprintf("independent_join_%t", independentJoin), func(t *testing.T) {
			b := newDiagBackend(t)
			st := b.Store
			ctx := context.Background()
			passwordHash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
			if err != nil {
				t.Fatalf("hash password: %v", err)
			}
			adminUID, err := st.CreateUserAdmin(ctx, "admin@example.test", "Admin", string(passwordHash), 3)
			if err != nil {
				t.Fatalf("create admin: %v", err)
			}
			targetUID, err := st.CreateUser(ctx, "target@example.test", "Target", string(passwordHash), 3, "")
			if err != nil {
				t.Fatalf("create target: %v", err)
			}
			if err := st.SetEmailVerified(ctx, targetUID, true); err != nil {
				t.Fatalf("verify target: %v", err)
			}
			_, playerUUID, err := st.CreatePlayer(ctx, targetUID, "TargetPlayer")
			if err != nil {
				t.Fatalf("create player: %v", err)
			}
			signer, err := ygg.NewLocalSigner(filepath.Join(t.TempDir(), "sign-key.pem"))
			if err != nil {
				t.Fatalf("create signer: %v", err)
			}
			svc := ygg.NewService(st, signer)
			svc.SetAuthGapResolver(func(context.Context) time.Duration { return 0 })
			api := ygg.NewAPI(svc, ygg.MetadataConfig{})
			h := api.Handler()
			request := func(method, path, body string) *httptest.ResponseRecorder {
				t.Helper()
				req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				return rec
			}
			authenticate := func() *httptest.ResponseRecorder {
				return request(http.MethodPost, "/api/yggdrasil/authserver/authenticate",
					`{"username":"target@example.test","password":"correct-password","clientToken":"client"}`)
			}
			setPermission := func(permission int) *httptest.ResponseRecorder {
				t.Helper()
				req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/admin/v1/users/%d/permission", targetUID),
					strings.NewReader(fmt.Sprintf(`{"permission":%d}`, permission)))
				req.SetPathValue("id", strconv.FormatInt(targetUID, 10))
				req = req.WithContext(context.WithValue(req.Context(), actorUIDKey{}, adminUID))
				rec := httptest.NewRecorder()
				b.handleUserSetPermission(rec, req)
				return rec
			}

			login := authenticate()
			if login.Code != http.StatusOK {
				t.Fatalf("initial authenticate = %d %s", login.Code, login.Body.String())
			}
			var tokenResponse struct {
				AccessToken string `json:"accessToken"`
			}
			if err := json.Unmarshal(login.Body.Bytes(), &tokenResponse); err != nil || tokenResponse.AccessToken == "" {
				t.Fatalf("decode authenticate: %s %v", login.Body.String(), err)
			}
			accessToken := tokenResponse.AccessToken
			joinBody := fmt.Sprintf(`{"accessToken":%q,"selectedProfile":%q,"serverId":"server"}`, accessToken, playerUUID)
			if rec := request(http.MethodPost, "/api/yggdrasil/sessionserver/session/minecraft/join", joinBody); rec.Code != http.StatusNoContent {
				t.Fatalf("initial join = %d %s", rec.Code, rec.Body.String())
			}
			if rec := request(http.MethodGet, "/api/yggdrasil/sessionserver/session/minecraft/hasJoined?username=TargetPlayer&serverId=server", ""); rec.Code != http.StatusOK {
				t.Fatalf("initial hasJoined = %d %s", rec.Code, rec.Body.String())
			}

			now := time.Now().UTC()
			if err := st.SaveAccessToken(ctx, oidc.AccessTokenRecord{
				JTI: "banned-access", UserID: targetUID, ClientID: "2",
				Scopes: []string{oidc.ScopeYggdrasilJoin}, ExpiresAt: now.Add(time.Hour), CreatedAt: now,
			}); err != nil {
				t.Fatalf("save OIDC access ledger row: %v", err)
			}
			if err := st.SaveRefreshToken(ctx, oidc.RefreshTokenRecord{
				Token: "banned-refresh", UserID: targetUID, ClientID: "2", ExpiresAt: now.Add(time.Hour), CreatedAt: now,
			}); err != nil {
				t.Fatalf("save OIDC refresh row: %v", err)
			}
			if !independentJoin {
				if _, err := st.DB().Exec(`UPDATE users SET can_join_server = 0 WHERE id = ?`, targetUID); err != nil {
					t.Fatalf("disable independent join flag: %v", err)
				}
			}

			if rec := setPermission(-1); rec.Code != http.StatusOK {
				t.Fatalf("ban = %d %s", rec.Code, rec.Body.String())
			}
			if rec := authenticate(); rec.Code != http.StatusForbidden {
				t.Fatalf("authenticate after ban = %d %s", rec.Code, rec.Body.String())
			}
			if rec := request(http.MethodPost, "/api/yggdrasil/sessionserver/session/minecraft/join", joinBody); rec.Code != http.StatusForbidden {
				t.Fatalf("join after ban = %d %s", rec.Code, rec.Body.String())
			}
			if rec := request(http.MethodGet, "/api/yggdrasil/sessionserver/session/minecraft/hasJoined?username=TargetPlayer&serverId=server", ""); rec.Code != http.StatusNoContent {
				t.Fatalf("hasJoined after ban = %d %s", rec.Code, rec.Body.String())
			}
			if rec := request(http.MethodPost, "/api/yggdrasil/authserver/validate", fmt.Sprintf(`{"accessToken":%q,"clientToken":"client"}`, accessToken)); rec.Code != http.StatusForbidden {
				t.Fatalf("validate old opaque token after ban = %d %s", rec.Code, rec.Body.String())
			}
			opaque, err := st.TokenByAccess(ctx, accessToken)
			if err != nil || !opaque.Invalidated {
				t.Fatalf("old opaque token invalidated = %v, err = %v", opaque.Invalidated, err)
			}
			oidcAccess, err := st.GetAccessToken(ctx, "banned-access")
			if err != nil || oidcAccess.RevokedAt.IsZero() {
				t.Fatalf("old OIDC access token revoked = %v, err = %v", oidcAccess.RevokedAt, err)
			}
			oidcRefresh, err := st.GetRefreshToken(ctx, "banned-refresh")
			if err != nil || !oidcRefresh.Revoked {
				t.Fatalf("old OIDC refresh token revoked = %v, err = %v", oidcRefresh.Revoked, err)
			}

			if rec := setPermission(0); rec.Code != http.StatusOK {
				t.Fatalf("unban = %d %s", rec.Code, rec.Body.String())
			}
			var storedFlag bool
			if err := st.DB().QueryRow(`SELECT can_join_server FROM users WHERE id = ?`, targetUID).Scan(&storedFlag); err != nil {
				t.Fatalf("read independent join flag: %v", err)
			}
			if storedFlag != independentJoin {
				t.Fatalf("unban changed can_join_server to %v, want %v", storedFlag, independentJoin)
			}
			recovered := authenticate()
			wantStatus := http.StatusForbidden
			if independentJoin {
				wantStatus = http.StatusOK
			}
			if recovered.Code != wantStatus {
				t.Fatalf("authenticate after unban = %d %s, want %d", recovered.Code, recovered.Body.String(), wantStatus)
			}
		})
	}
}
