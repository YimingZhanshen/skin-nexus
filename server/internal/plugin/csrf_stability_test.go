package plugin

// One CSRF cookie is shared by the whole site (Path=/), while every plugin page
// keeps the token it fetched in its own frame. Re-minting on each request means
// the last page to load wins and every other mounted page starts failing its
// writes with "csrf validation failed" — a refusal the operator cannot cause,
// explain, or retry out of. The token is a double-submit pairing, not a secret
// per request, so a second page gets the same value back.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fetchCSRF(t *testing.T, m *Manager, carry ...*http.Cookie) (string, *http.Cookie) {
	t.Helper()
	rec := doPluginReq(m, "GET", "/api/plugins/routes/_csrf", "", carry...)
	if rec.Code != 200 {
		t.Fatalf("csrf = %d %s", rec.Code, rec.Body.String())
	}
	var tok struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &tok); err != nil {
		t.Fatal(err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("no csrf cookie set")
	}
	return tok.Token, cookies[0]
}

func TestCSRFTokenSurvivesASecondPage(t *testing.T) {
	m := newRoutesHost(t)
	user := &http.Cookie{Name: "skin_session", Value: "user-token"}

	first, firstCookie := fetchCSRF(t, m)
	// A browser replays the cookie it already holds, which is what the second
	// page does in reality.
	second, secondCookie := fetchCSRF(t, m, firstCookie)
	if second != first {
		t.Fatalf("a second page re-minted the token (%s then %s); the first frame's writes now fail"+
			" because the cookie no longer matches the value it cached", first, second)
	}
	if firstCookie.Value != secondCookie.Value {
		t.Fatalf("the csrf cookie changed between pages: %q then %q", firstCookie.Value, secondCookie.Value)
	}

	// The value the *first* page cached must still be accepted once the second
	// page has loaded — that is the whole regression.
	r := httptest.NewRequest("POST", "/api/plugins/routes/me", strings.NewReader(`{"x":1}`))
	r.Host = "skin.test"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", first)
	r.Header.Set("Origin", "http://skin.test")
	r.AddCookie(user)
	r.AddCookie(secondCookie)
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, r)
	if rec.Code != 200 {
		t.Fatalf("post with the first page's token after a second page loaded = %d %s",
			rec.Code, rec.Body.String())
	}
}

// A token that did not come from this handler must not be adopted: only a
// well-formed 64-hex value round-trips, anything else is replaced.
func TestCSRFIgnoresAHandWrittenCookie(t *testing.T) {
	m := newRoutesHost(t)
	r := httptest.NewRequest("GET", "/api/plugins/routes/_csrf", nil)
	r.Host = "skin.test"
	r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "not-a-token-from-us-at-all-and-not-64-hex"})
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, r)
	if rec.Code != 200 {
		t.Fatalf("csrf = %d %s", rec.Code, rec.Body.String())
	}
	got := rec.Result().Cookies()
	if len(got) == 0 {
		t.Fatal("no csrf cookie was set")
	}
	if got[0].Value == "not-a-token-from-us-at-all-and-not-64-hex" {
		t.Fatal("the handler adopted a cookie value it never issued")
	}
	if len(got[0].Value) != 64 || strings.TrimLeft(got[0].Value, "0123456789abcdef") != "" {
		t.Fatalf("issued token is not 64 lowercase hex: %q", got[0].Value)
	}
}
