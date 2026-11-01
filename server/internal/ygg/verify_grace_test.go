package ygg

// 邮箱验证宽限期 (2026-11-02): the gate became operable only when it grew a
// grace window — with 0 days it locks out every account that never got a
// link, with N days a fresh registration can play while it verifies.

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func authenticateSteve(t *testing.T, api *API) string {
	t.Helper()
	rec := do(t, api.Handler(), "POST", "/api/yggdrasil/authserver/authenticate",
		`{"username":"steve","password":"pw-ok","clientToken":"ct"}`, nil)
	if rec.Code != 200 {
		t.Fatalf("authenticate = %d %s (want 200)", rec.Code, rec.Body.String())
	}
	var tok struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &tok); err != nil {
		t.Fatal(err)
	}
	return tok.AccessToken
}

func joinWith(t *testing.T, api *API, accessToken, serverID string) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, api.Handler(), "POST", "/api/yggdrasil/sessionserver/session/minecraft/join",
		`{"accessToken":"`+accessToken+`","selectedProfile":"`+testUUID+`","serverId":"`+serverID+`"}`, nil)
}

func setUnverified(st *memStore, createdAt time.Time) {
	u := st.byID[1]
	u.EmailVerified = false
	u.CreatedAt = createdAt
	st.byID[1] = u
	st.users["steve"] = u
}

func TestVerificationGraceBlocksAfterWindow(t *testing.T) {
	api, st := newTestAPI(t)
	// Authenticate while the gate is off (the gate also stops login itself),
	// then turn it on with the account's birthday outside the window.
	accessToken := authenticateSteve(t, api)
	fixed := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	api.svc.now = func() time.Time { return fixed }
	api.svc.RequireVerification = true

	setUnverified(st, fixed.Add(-36*time.Hour))
	if rec := joinWith(t, api, accessToken, "g-late"); rec.Code != 403 ||
		!strings.Contains(rec.Body.String(), "Email not verified") {
		t.Fatalf("past grace: join = %d %s (want 403 email gate)", rec.Code, rec.Body.String())
	}
}

func TestVerificationGraceAllowsInsideWindow(t *testing.T) {
	api, st := newTestAPI(t)
	accessToken := authenticateSteve(t, api)
	fixed := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	api.svc.now = func() time.Time { return fixed }
	api.svc.RequireVerification = true
	api.svc.VerifyGrace = 7 * 24 * time.Hour

	setUnverified(st, fixed.Add(-36*time.Hour))
	if rec := joinWith(t, api, accessToken, "g-grace"); rec.Code != 204 {
		t.Fatalf("inside grace: join = %d %s (want 204)", rec.Code, rec.Body.String())
	}
}

func TestVerificationGraceFromResolver(t *testing.T) {
	api, st := newTestAPI(t)
	fixed := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	api.svc.now = func() time.Time { return fixed }
	api.svc.RequireVerificationResolver = func(context.Context) bool { return true }
	// Panel writes the option per request; a resolver returning 0 days must
	// behave like no grace at all.
	api.svc.VerifyGraceResolver = func(context.Context) time.Duration { return 0 }

	setUnverified(st, fixed.Add(-time.Minute))
	if !api.svc.verificationBlocks(context.Background(), st.byID[1]) {
		t.Fatal("0-day grace: fresh account should already be blocked")
	}
	api.svc.VerifyGraceResolver = func(context.Context) time.Duration { return time.Hour }
	if api.svc.verificationBlocks(context.Background(), st.byID[1]) {
		t.Fatal("1-hour grace: account created a minute ago should still join")
	}
}

// A user record without CreatedAt (older callers, hand-built fixtures) must
// fail closed rather than buy itself an open-ended grace.
func TestVerificationZeroCreatedAtIsNoGrace(t *testing.T) {
	svc := NewService(newMemStore(), nil)
	svc.RequireVerification = true
	svc.VerifyGrace = 30 * 24 * time.Hour
	if !svc.verificationBlocks(context.Background(), User{ID: 7, EmailVerified: false}) {
		t.Fatal("zero CreatedAt with grace set: want blocked")
	}
}
