package oidc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"skin-nexus/server/internal/i18n"
)

func originProvider(t *testing.T) (*Provider, *memBackend) {
	t.Helper()
	p, b := testProvider(t)
	p.SessionResolver = func(context.Context, string) (int64, error) { return 42, nil }
	return p, b
}

func seedOriginDevice(t *testing.T, b *memBackend) {
	t.Helper()
	if err := b.SaveDeviceAuth(context.Background(), DeviceAuthRecord{
		DeviceCode: "device-origin",
		UserCode:   "ABCD-EFGH",
		ClientID:   "2",
		Scopes:     []string{"openid"},
		ExpiresAt:  time.Now().Add(time.Hour),
		CreatedAt:  time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
}

func seedOriginToken(t *testing.T, b *memBackend) {
	t.Helper()
	now := time.Now()
	if err := b.SaveAccessToken(context.Background(), AccessTokenRecord{
		JTI: "jti-origin", UserID: 42, ClientID: "2", Scopes: []string{"openid"},
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := b.SaveRefreshToken(context.Background(), RefreshTokenRecord{
		Token: "rt-origin", UserID: 42, ClientID: "2", Scopes: []string{"openid"},
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDeviceAPIMutationsRequireOrigin(t *testing.T) {
	endpoints := []struct {
		name string
		path string
		body string
	}{
		{name: "device submit", path: "/api/v1/oidc/device/submit", body: `{"user_code":"ABCD-EFGH","decision":"approve","pid":7}`},
		{name: "token revoke", path: "/api/v1/oidc/tokens/revoke", body: `{"jti":"jti-origin"}`},
	}
	origins := []struct {
		name       string
		origin     string
		wantStatus int
	}{
		{name: "missing origin", wantStatus: http.StatusForbidden},
		{name: "external origin", origin: "https://other.test", wantStatus: http.StatusForbidden},
		{name: "same origin", origin: "https://skin.test", wantStatus: http.StatusOK},
	}
	for _, endpoint := range endpoints {
		for _, origin := range origins {
			t.Run(endpoint.name+"/"+origin.name, func(t *testing.T) {
				p, b := originProvider(t)
				if endpoint.path == "/api/v1/oidc/device/submit" {
					seedOriginDevice(t, b)
				} else {
					seedOriginToken(t, b)
				}
				r := httptest.NewRequest(http.MethodPost, "http://skin.test"+endpoint.path, strings.NewReader(endpoint.body))
				r.Header.Set("Content-Type", "application/json")
				r.AddCookie(&http.Cookie{Name: "skin_session", Value: "session"})
				if origin.origin != "" {
					r.Header.Set("Origin", origin.origin)
				}
				rec := httptest.NewRecorder()
				p.Wrap(http.NotFoundHandler()).ServeHTTP(rec, r)
				if rec.Code != origin.wantStatus {
					t.Fatalf("status = %d, want %d; body %s", rec.Code, origin.wantStatus, rec.Body.String())
				}
				if origin.wantStatus == http.StatusForbidden {
					var body struct {
						Code string `json:"code"`
					}
					if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
						t.Fatal(err)
					}
					if body.Code != i18n.MsgOriginMismatch {
						t.Fatalf("error code = %q, want %q", body.Code, i18n.MsgOriginMismatch)
					}
				}
				if endpoint.path == "/api/v1/oidc/device/submit" {
					device, err := b.GetDeviceAuthByUserCode(context.Background(), "ABCD-EFGH")
					if err != nil {
						t.Fatal(err)
					}
					if device.Done != (origin.wantStatus == http.StatusOK) {
						t.Fatalf("device Done = %v after status %d", device.Done, origin.wantStatus)
					}
				} else {
					token, err := b.GetAccessToken(context.Background(), "jti-origin")
					if err != nil {
						t.Fatal(err)
					}
					wantRevoked := origin.wantStatus == http.StatusOK
					if gotRevoked := !token.RevokedAt.IsZero(); gotRevoked != wantRevoked {
						t.Fatalf("token revoked = %v after status %d, want %v", gotRevoked, origin.wantStatus, wantRevoked)
					}
					refresh, err := b.GetRefreshToken(context.Background(), "rt-origin")
					if err != nil {
						t.Fatal(err)
					}
					if refresh.Revoked != wantRevoked {
						t.Fatalf("refresh Revoked = %v after status %d, want %v", refresh.Revoked, origin.wantStatus, wantRevoked)
					}
				}
			})
		}
	}
}

func TestDeviceAPIReadsDoNotRequireOrigin(t *testing.T) {
	p, b := originProvider(t)
	seedOriginDevice(t, b)
	r := httptest.NewRequest(http.MethodGet, "http://skin.test/api/v1/oidc/device/info?user_code=ABCD-EFGH", nil)
	r.Header.Set("Origin", "https://other.test")
	r.AddCookie(&http.Cookie{Name: "skin_session", Value: "session"})
	rec := httptest.NewRecorder()
	p.Wrap(http.NotFoundHandler()).ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("device info with external Origin = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
}

func TestOriginGateDoesNotWrapProtocolEndpoints(t *testing.T) {
	p, b := originProvider(t)
	if err := b.EnsureOidcClient(context.Background(), ClientRecord{
		ID: "origin-client", Name: "Origin client", SecretHash: "secret",
		RedirectURIs: []string{"https://client.test/callback"},
		GrantTypes:   []string{"authorization_code"}, Scopes: []string{"openid"},
	}); err != nil {
		t.Fatal(err)
	}
	challenge := strings.Repeat("A", 43)
	cases := []struct {
		name        string
		method      string
		path        string
		contentType string
		body        string
		wantStatus  int
	}{
		{name: "token", method: http.MethodPost, path: "/oauth/token", contentType: "application/x-www-form-urlencoded", body: "grant_type=device_code&client_id=2&device_code=missing", wantStatus: http.StatusBadRequest},
		{name: "device authorization", method: http.MethodPost, path: "/device_authorization", contentType: "application/x-www-form-urlencoded", body: "client_id=2&scope=openid", wantStatus: http.StatusOK},
		{name: "authorize", method: http.MethodGet, path: "/authorize?client_id=origin-client&redirect_uri=https%3A%2F%2Fclient.test%2Fcallback&response_type=code&scope=openid&state=state&code_challenge=" + challenge + "&code_challenge_method=S256", wantStatus: http.StatusFound},
		{name: "revoke", method: http.MethodPost, path: "/revoke", contentType: "application/x-www-form-urlencoded", body: "token=missing&client_id=2", wantStatus: http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, origin := range []string{"", "https://other.test"} {
				r := httptest.NewRequest(tc.method, "http://skin.test"+tc.path, strings.NewReader(tc.body))
				if tc.contentType != "" {
					r.Header.Set("Content-Type", tc.contentType)
				}
				if origin != "" {
					r.Header.Set("Origin", origin)
				}
				rec := httptest.NewRecorder()
				p.Wrap(http.NotFoundHandler()).ServeHTTP(rec, r)
				if rec.Code != tc.wantStatus {
					t.Fatalf("Origin %q: status = %d, want %d; body %s", origin, rec.Code, tc.wantStatus, rec.Body.String())
				}
				if strings.Contains(rec.Body.String(), i18n.MsgOriginMismatch) {
					t.Fatalf("protocol endpoint was origin-gated: %s", rec.Body.String())
				}
			}
		})
	}
}
