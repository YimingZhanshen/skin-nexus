package adminapi

// Theme-market actions ride the same super-admin gate as the plugin market, so
// they were expected to behave like /market/install — but they were registered
// without the auth middleware, so `actorUID` stayed 0 and every caller, super
// admin included, was told "this action needs a super admin web session".
// These tests walk the whole gate from both sides so a missing `b.auth` cannot
// pass as "correctly refused" and a loosened gate cannot pass as "reachable".

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"skin-nexus/server/internal/theme"
	"skin-nexus/server/internal/thememkt"
)

const (
	needWebSession  = "super admin web session"
	needPermTwo     = "permission=2"
	multipartBadFit = "multipart parse failed"
)

func newThemeMarketBackend(t *testing.T) *Backend {
	t.Helper()
	dir := t.TempDir()
	reg, err := theme.NewRegistry(dir, "0.5.0")
	if err != nil {
		t.Fatal(err)
	}
	// MarketURL stays empty on purpose: an offline service still answers the
	// gate, which is all these tests assert. The action itself then fails on a
	// disabled market — a status that proves the caller got through.
	svc := thememkt.New(thememkt.Config{ThemesDir: dir, HostVersion: "0.5.0"}, reg)
	b := &Backend{AdminTok: "sekret"}
	b.MountAppearance(AppearanceDeps{Themes: reg, ThemeMarket: svc})
	return b
}

func doTheme(t *testing.T, b *Backend, method, path, body string, token bool, origin string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if token {
		r.Header.Set("X-Admin-Token", "sekret")
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	for _, c := range cookies {
		r.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	b.Handler().ServeHTTP(rec, r)
	return rec
}

// A super admin web session reaches the action; a machine token and a plain
// admin session each get their own distinct refusal.
func TestThemeMarketActionsAcceptASuperAdminWebSession(t *testing.T) {
	cases := []struct {
		method, path, body string
	}{
		{http.MethodPost, "/api/admin/v1/themes/market/install", `{"name":"demo"}`},
		{http.MethodDelete, "/api/admin/v1/themes/market/demo", ""},
	}
	for _, c := range cases {
		b := newThemeMarketBackend(t)

		stubSession(t, func(*http.Request) (int64, bool) { return 0, false })
		rec := doTheme(t, b, c.method, c.path, c.body, true, "http://example.com")
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), needWebSession) {
			t.Fatalf("%s %s with a token only = %d %s, want the web-session refusal",
				c.method, c.path, rec.Code, rec.Body.String())
		}

		stubSession(t, func(*http.Request) (int64, bool) { return 5, true })
		stubPerm(t, 1)
		rec = doTheme(t, b, c.method, c.path, c.body, false, "http://example.com")
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), needPermTwo) {
			t.Fatalf("%s %s as an ordinary admin = %d %s, want the super-admin refusal"+
				" (a route registered without b.auth answers with the web-session message instead)",
				c.method, c.path, rec.Code, rec.Body.String())
		}

		stubPerm(t, 2)
		rec = doTheme(t, b, c.method, c.path, c.body, false, "http://example.com")
		if rec.Code == http.StatusForbidden {
			t.Fatalf("%s %s as a same-origin super admin = 403 %s",
				c.method, c.path, rec.Body.String())
		}
	}
}

type multipartBody struct {
	body        *bytes.Reader
	contentType string
}

// multipartWith builds one form field of `size` bytes of filler. The bytes are
// not a valid package: these tests only need the request to survive transport
// and reach the handler that judges it.
func multipartWith(t *testing.T, field string, size int) multipartBody {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile(field, "pack.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(bytes.Repeat([]byte("x"), size)); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return multipartBody{body: bytes.NewReader(buf.Bytes()), contentType: mw.FormDataContentType()}
}

// A browser that once stored the admin token sends it on every panel request,
// so the machine channel — which carries no actor — answers before the session
// is ever read. Every "super admin web session" action then refuses the person
// who is, in fact, logged in as super admin.
func TestAdminTokenDoesNotShadowAWebSession(t *testing.T) {
	b := newThemeMarketBackend(t)
	stubSession(t, func(*http.Request) (int64, bool) { return 5, true })
	stubPerm(t, 2)

	rec := doTheme(t, b, http.MethodPost, "/api/admin/v1/themes/market/install",
		`{"name":"demo"}`, true, "http://example.com",
		&http.Cookie{Name: sessionCookieName, Value: "admin-session-token"})
	if rec.Code == http.StatusForbidden && strings.Contains(rec.Body.String(), needWebSession) {
		t.Fatalf("a super admin whose browser also sends the API token was routed down the machine"+
			" channel and lost their identity: %s", rec.Body.String())
	}
}

// A token that is not accompanied by an admin session stays on the machine
// channel, which is what the refusal above is supposed to mean.
func TestAdminTokenAloneIsStillAMachineCaller(t *testing.T) {
	b := newThemeMarketBackend(t)
	stubSession(t, func(*http.Request) (int64, bool) { return 0, false })

	rec := doTheme(t, b, http.MethodPost, "/api/admin/v1/themes/market/install",
		`{"name":"demo"}`, true, "http://example.com")
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), needWebSession) {
		t.Fatalf("token only = %d %s, want the web-session refusal", rec.Code, rec.Body.String())
	}

	// A cookie that does not resolve to an admin must not hand the machine
	// caller an identity either.
	rec = doTheme(t, b, http.MethodPost, "/api/admin/v1/themes/market/install",
		`{"name":"demo"}`, true, "http://example.com",
		&http.Cookie{Name: sessionCookieName, Value: "somebody-elses-session"})
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), needWebSession) {
		t.Fatalf("token + a session that is not an admin = %d %s, want the web-session refusal",
			rec.Code, rec.Body.String())
	}
}

// Local upload is the operator's documented path for a theme pack that is not
// in the market; the global admin body cap cut it down to 1 MiB before the
// service's own 16 MiB limit could see it.
func TestThemeUploadAcceptsASuperAdminWebSession(t *testing.T) {
	b := newThemeMarketBackend(t)
	stubSession(t, func(*http.Request) (int64, bool) { return 5, true })
	stubPerm(t, 2)

	mr := multipartWith(t, "file", 64<<10)
	r := httptest.NewRequest(http.MethodPost, "/api/admin/v1/themes/upload", mr.body)
	r.Header.Set("Content-Type", mr.contentType)
	r.Header.Set("Origin", "http://example.com")
	rec := httptest.NewRecorder()
	b.Handler().ServeHTTP(rec, r)
	if rec.Code == http.StatusForbidden {
		t.Fatalf("upload as a same-origin super admin = 403 %s", rec.Body.String())
	}
}

// A package above the global 1 MiB admin cap must reach the theme service,
// which sets its own limit — otherwise the documented 16 MiB ceiling is a lie.
func TestThemeUploadReachesServiceAboveOneMiB(t *testing.T) {
	b := newThemeMarketBackend(t)
	stubSession(t, func(*http.Request) (int64, bool) { return 5, true })
	stubPerm(t, 2)

	mr := multipartWith(t, "file", 2<<20)
	r := httptest.NewRequest(http.MethodPost, "/api/admin/v1/themes/upload", mr.body)
	r.Header.Set("Content-Type", mr.contentType)
	r.Header.Set("Origin", "http://example.com")
	rec := httptest.NewRecorder()
	b.Handler().ServeHTTP(rec, r)
	if strings.Contains(rec.Body.String(), multipartBadFit) {
		t.Fatalf("a 2 MiB package was truncated before the theme service saw it: %d %s",
			rec.Code, rec.Body.String())
	}
}
