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
