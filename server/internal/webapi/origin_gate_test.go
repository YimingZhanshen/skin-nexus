package webapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"skin-nexus/server/internal/dialect"
	"skin-nexus/server/internal/migrate"
	"skin-nexus/server/internal/store"
)

func originGateFixture(t *testing.T) (*Backend, *store.Mysql, int64) {
	t.Helper()
	m, err := store.Open("sqlite", "sqlite:"+filepath.Join(t.TempDir(), "origin.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	if err := migrate.ApplySQLDirTracked(m.DB(), filepath.Join("..", "..", "migrations"), dialect.SQLite); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	uid, err := m.CreateUser(ctx, "origin@example.test", "before", "hash", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.CreateSession(ctx, "origin-session", uid, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	return &Backend{Store: m}, m, uid
}

func TestWebCookieMutationOriginGate(t *testing.T) {
	cases := []struct {
		name       string
		origin     string
		site       string
		wantStatus int
		wantNick   string
	}{
		{name: "missing origin is refused before mutation", wantStatus: http.StatusForbidden, wantNick: "before"},
		{name: "external origin is refused before mutation", origin: "https://other.test", wantStatus: http.StatusForbidden, wantNick: "before"},
		{name: "same host origin mutates", origin: "https://skin.test", wantStatus: http.StatusOK, wantNick: "after"},
		{name: "same-origin fetch metadata mutates", site: "same-origin", wantStatus: http.StatusOK, wantNick: "after"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, m, uid := originGateFixture(t)
			r := httptest.NewRequest(http.MethodPut, "http://skin.test/api/v1/me/profile", strings.NewReader(`{"nickname":"after"}`))
			r.Header.Set("Content-Type", "application/json")
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "origin-session"})
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if tc.site != "" {
				r.Header.Set("Sec-Fetch-Site", tc.site)
			}
			rec := httptest.NewRecorder()
			b.Handler().ServeHTTP(rec, r)
			if rec.Code != tc.wantStatus {
				t.Fatalf("profile update = %d, want %d; body %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus == http.StatusForbidden {
				var body struct {
					Code string `json:"code"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if body.Code != "admin.origin_mismatch" {
					t.Fatalf("error code = %q, want admin.origin_mismatch", body.Code)
				}
			}
			user, err := m.UserByID(context.Background(), uid)
			if err != nil {
				t.Fatal(err)
			}
			if user.Nickname != tc.wantNick {
				t.Fatalf("stored nickname = %q, want %q", user.Nickname, tc.wantNick)
			}
			var audits int
			if err := m.DB().QueryRow(`SELECT COUNT(*) FROM audit_log WHERE actor_uid = ? AND action = 'profile_update'`, uid).Scan(&audits); err != nil {
				t.Fatal(err)
			}
			wantAudits := 0
			if tc.wantStatus == http.StatusOK {
				wantAudits = 1
			}
			if audits != wantAudits {
				t.Fatalf("profile_update audit rows = %d, want %d", audits, wantAudits)
			}
		})
	}
}

func TestWebSafeAndPublicRequestsDoNotRequireOrigin(t *testing.T) {
	b, _, _ := originGateFixture(t)
	h := b.Handler()

	get := httptest.NewRequest(http.MethodGet, "http://skin.test/api/v1/me", nil)
	get.AddCookie(&http.Cookie{Name: sessionCookie, Value: "origin-session"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, get)
	if rec.Code != http.StatusOK {
		t.Fatalf("safe session request without Origin = %d, want 200; body %s", rec.Code, rec.Body.String())
	}

	post := httptest.NewRequest(http.MethodPost, "http://skin.test/api/v1/register", strings.NewReader(`{"nickname":"guest","password":"password123"}`))
	post.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, post)
	if rec.Code != http.StatusCreated {
		t.Fatalf("public mutation without session or Origin = %d, want 201; body %s", rec.Code, rec.Body.String())
	}
}
