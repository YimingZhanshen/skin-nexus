package oidc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// failingBackend delegates to a working Backend and fails exactly one revoke
// call, so partial completion is observable without a database.
type failingBackend struct {
	Backend
	failAccess        bool
	failClientRefresh bool
	failUserAccess    bool
	failUserRefresh   bool
}

func (b *failingBackend) RevokeAccessToken(ctx context.Context, jti string) error {
	if b.failAccess {
		return errors.New("store unavailable")
	}
	return b.Backend.RevokeAccessToken(ctx, jti)
}

func (b *failingBackend) RevokeClientRefreshTokens(ctx context.Context, uid int64, clientID string) error {
	if b.failClientRefresh {
		return errors.New("store unavailable")
	}
	return b.Backend.RevokeClientRefreshTokens(ctx, uid, clientID)
}

func (b *failingBackend) RevokeUserAccessTokens(ctx context.Context, uid int64) error {
	if b.failUserAccess {
		return errors.New("store unavailable")
	}
	return b.Backend.RevokeUserAccessTokens(ctx, uid)
}

func (b *failingBackend) RevokeUserRefreshTokens(ctx context.Context, uid int64) error {
	if b.failUserRefresh {
		return errors.New("store unavailable")
	}
	return b.Backend.RevokeUserRefreshTokens(ctx, uid)
}

// seedRevokeFixture gives uid 42 one live access token and one live refresh
// token on the shared client, and returns a handler that reads the session as
// uid 42 plus a poster for the revoke endpoint. wrap, when non-nil, replaces the
// backend the handlers talk to while the fixture seeds the real one.
func seedRevokeFixture(t *testing.T, wrap func(Backend) Backend) (http.Handler, func(string) *httptest.ResponseRecorder) {
	t.Helper()
	p, b := testProvider(t)
	if wrap != nil {
		p.backend = wrap(b)
	}
	p.SessionResolver = func(context.Context, string) (int64, error) { return 42, nil }
	now := time.Now()
	if err := b.SaveAccessToken(context.Background(), AccessTokenRecord{
		JTI: "jti-live", UserID: 42, ClientID: "2",
		Scopes: []string{"openid"}, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := b.SaveRefreshToken(context.Background(), RefreshTokenRecord{
		Token: "rt-live", UserID: 42, ClientID: "2",
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	h := p.Wrap(http.NotFoundHandler())
	post := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/v1/oidc/tokens/revoke", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "skin_session", Value: "s1"})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	return h, post
}

// The refresh table has no jti column, so a single revocation also kills the
// uid+client refresh scope. When that second half fails the caller must not be
// told the whole revocation succeeded: a refresh token they believe is dead can
// still mint new access tokens.
func TestRevokeReportsClientRefreshFailure(t *testing.T) {
	_, post := seedRevokeFixture(t, func(b Backend) Backend {
		return &failingBackend{Backend: b, failClientRefresh: true}
	})

	w := post(`{"jti":"jti-live"}`)
	if strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("revoke answered success while the client refresh scope survived: %d %s", w.Code, w.Body.String())
	}
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("partial revocation status = %d, want 500: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("error body is not JSON: %v (%s)", err, w.Body.String())
	}
	if body["code"] != "oidc.revoke_partial" {
		t.Fatalf("error code = %v, want oidc.revoke_partial — the jti died but the refresh scope did not", body["code"])
	}
}