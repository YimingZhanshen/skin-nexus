package adminapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"skin-nexus/server/internal/store"
)

// stubSession swaps the session-admin resolver for one test (restored via
// t.Cleanup) — the store-backed default needs MySQL and lives behind the
// same seam style as oidc.sessionCookie.
func stubSession(t *testing.T, fn func(*http.Request) (int64, bool)) {
	t.Helper()
	old := sessionAdmin
	sessionAdmin = func(r *http.Request, _ *store.Mysql) (int64, bool) { return fn(r) }
	t.Cleanup(func() { sessionAdmin = old })
}

func authServed(t *testing.T, b *Backend, r *http.Request) (served bool, actor int64, rec *httptest.ResponseRecorder) {
	t.Helper()
	rec = httptest.NewRecorder()
	h := b.auth(func(w http.ResponseWriter, r *http.Request) {
		served = true
		actor = actorUID(r)
		w.WriteHeader(http.StatusOK)
	})
	h.ServeHTTP(rec, r)
	return
}

func TestAuthTokenPasses(t *testing.T) {
	b := &Backend{AdminTok: "sekret"}
	r := httptest.NewRequest(http.MethodGet, "/api/admin/v1/overview", nil)
	r.Header.Set("X-Admin-Token", "sekret")
	served, actor, rec := authServed(t, b, r)
	if !served || rec.Code != http.StatusOK {
		t.Fatalf("valid token: served=%v code=%d", served, rec.Code)
	}
	if actor != 0 { // token path stays the system/API actor
		t.Fatalf("token path actor uid = %d, want 0", actor)
	}
}

func TestAuthTokenWrongForbidden(t *testing.T) {
	b := &Backend{AdminTok: "sekret"}
	r := httptest.NewRequest(http.MethodGet, "/api/admin/v1/overview", nil)
	r.Header.Set("X-Admin-Token", "nope")
	served, _, rec := authServed(t, b, r)
	if served || rec.Code != http.StatusForbidden {
		t.Fatalf("wrong token: served=%v code=%d, want 403", served, rec.Code)
	}
}

func TestAuthTokenPathLockedWithoutConfig(t *testing.T) {
	b := &Backend{AdminTok: ""} // empty SKIN_ADMIN_TOKEN = token path locked
	r := httptest.NewRequest(http.MethodGet, "/api/admin/v1/overview", nil)
	served, _, rec := authServed(t, b, r)
	if served || rec.Code != http.StatusForbidden {
		t.Fatalf("empty token config: served=%v code=%d, want 403", served, rec.Code)
	}
}

func TestAuthSessionAdminPasses(t *testing.T) {
	stubSession(t, func(r *http.Request) (int64, bool) {
		if c, err := r.Cookie(sessionCookieName); err != nil || c.Value == "" {
			t.Fatalf("session lookup without %s cookie", sessionCookieName)
		}
		return 7, true
	})
	b := &Backend{AdminTok: "sekret"}
	r := httptest.NewRequest(http.MethodGet, "/api/admin/v1/overview", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "tok"})
	served, actor, rec := authServed(t, b, r)
	if !served || rec.Code != http.StatusOK {
		t.Fatalf("admin session: served=%v code=%d", served, rec.Code)
	}
	if actor != 7 {
		t.Fatalf("session actor uid = %d, want 7 (audit attribution)", actor)
	}
}

func TestAuthTokenDoesNotFallbackFromSessionCookie(t *testing.T) {
	cases := []struct {
		name       string
		cookie     string
		permission int
		expired    bool
		createUser bool
	}{
		{name: "invalid", cookie: "unknown-session"},
		{name: "non_admin", cookie: "normal-session", permission: 0, createUser: true},
		{name: "banned", cookie: "banned-session", permission: -1, createUser: true},
		{name: "expired", cookie: "expired-session", permission: 1, expired: true, createUser: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newDiagBackend(t)
			b.AdminTok = "machine-token"
			ctx := context.Background()
			if tc.createUser {
				uid, err := b.Store.CreateUser(ctx, tc.name+"@example.test", tc.name, "hash", 3, "")
				if err != nil {
					t.Fatalf("create user: %v", err)
				}
				expires := time.Now().UTC().Add(time.Hour)
				if tc.expired {
					expires = time.Now().UTC().Add(-time.Hour)
				}
				if err := b.Store.CreateSession(ctx, tc.cookie, uid, expires); err != nil {
					t.Fatalf("create session: %v", err)
				}
				if tc.permission == -1 {
					// Leave the live session in place: the read path must reject a
					// banned account even before physical revocation catches up.
					if _, err := b.Store.DB().ExecContext(ctx, `UPDATE users SET permission = -1 WHERE id = ?`, uid); err != nil {
						t.Fatalf("ban directly: %v", err)
					}
				} else if tc.permission != 0 {
					if err := b.Store.SetUserPermission(ctx, uid, tc.permission); err != nil {
						t.Fatalf("set permission: %v", err)
					}
				}
			}
			r := httptest.NewRequest(http.MethodGet, "/api/admin/v1/overview", nil)
			r.Header.Set("X-Admin-Token", "machine-token")
			r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tc.cookie})
			served, actor, rec := authServed(t, b, r)
			if served || actor != 0 || rec.Code != http.StatusForbidden {
				t.Fatalf("served=%v actor=%d code=%d, want no handler call, actor 0 and 403", served, actor, rec.Code)
			}
		})
	}
}

func TestAuthTokenWithAdminSessionKeepsHumanActor(t *testing.T) {
	b := newDiagBackend(t)
	b.AdminTok = "machine-token"
	ctx := context.Background()
	uid, err := b.Store.CreateUserAdmin(ctx, "hybrid@example.test", "HybridAdmin", "hash", 3)
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if err := b.Store.CreateSession(ctx, "admin-session", uid, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("create session: %v", err)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/admin/v1/overview", nil)
	r.Header.Set("X-Admin-Token", "machine-token")
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "admin-session"})
	served, actor, rec := authServed(t, b, r)
	if !served || actor != uid || rec.Code != http.StatusOK {
		t.Fatalf("served=%v actor=%d code=%d, want admin actor %d and 200", served, actor, rec.Code, uid)
	}
}

func TestAuthTokenAllowsEmptySessionCookie(t *testing.T) {
	stubSession(t, func(*http.Request) (int64, bool) {
		t.Fatal("an empty session cookie must not trigger session lookup")
		return 0, false
	})
	b := &Backend{AdminTok: "machine-token"}
	r := httptest.NewRequest(http.MethodGet, "/api/admin/v1/overview", nil)
	r.Header.Set("X-Admin-Token", "machine-token")
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: ""})
	served, actor, rec := authServed(t, b, r)
	if !served || actor != 0 || rec.Code != http.StatusOK {
		t.Fatalf("served=%v actor=%d code=%d, want machine actor 0 and 200", served, actor, rec.Code)
	}
}

func TestAuthTokenRejectsLaterNonEmptySessionCookie(t *testing.T) {
	stubSession(t, func(*http.Request) (int64, bool) { return 0, false })
	b := &Backend{AdminTok: "machine-token"}
	r := httptest.NewRequest(http.MethodGet, "/api/admin/v1/overview", nil)
	r.Header.Set("X-Admin-Token", "machine-token")
	// Browsers can send same-name cookies from different paths. An empty first
	// value must not hide a later non-empty session credential.
	r.Header.Set("Cookie", "skin_session=; skin_session=invalid-session")
	served, actor, rec := authServed(t, b, r)
	if served || actor != 0 || rec.Code != http.StatusForbidden {
		t.Fatalf("served=%v actor=%d code=%d, want no handler call, actor 0 and 403", served, actor, rec.Code)
	}
}

func TestAuthNonAdminSessionForbidden(t *testing.T) {
	stubSession(t, func(*http.Request) (int64, bool) { return 0, false })
	b := &Backend{AdminTok: "sekret"}
	r := httptest.NewRequest(http.MethodGet, "/api/admin/v1/overview", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "tok"})
	served, _, rec := authServed(t, b, r)
	if served || rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin session: served=%v code=%d, want 403 (same as wrong token)", served, rec.Code)
	}
}
