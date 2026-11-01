package webapi

// errorshape_test.go pins the response envelope itself. These paths need no
// database: every /me/* handler resolves the session before it touches a store,
// and the forgot-password handler rejects a malformed body before it does, so
// the whole error shape is reachable from a zero-value Backend.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"skin-nexus/server/internal/errorgate"
)

// envelope decodes the three fields every error response must carry.
type envelope struct {
	Error   string `json:"error"`
	Message string `json:"message"`
	Code    string `json:"code"`
}

func TestErrorEnvelopeShape(t *testing.T) {
	h := (&Backend{}).Handler()

	cases := []struct {
		name   string
		method string
		path   string
		body   string
		want   int
	}{
		{"me without session", "GET", "/api/v1/me", "", http.StatusUnauthorized},
		{"textures without session", "GET", "/api/v1/me/textures", "", http.StatusUnauthorized},
		{"upload without session", "POST", "/api/v1/me/textures", "", http.StatusUnauthorized},
		{"players without session", "GET", "/api/v1/me/players", "", http.StatusUnauthorized},
		{"create player without session", "POST", "/api/v1/me/players", "", http.StatusUnauthorized},
		{"rename without session", "PUT", "/api/v1/me/players/1/name", "", http.StatusUnauthorized},
		{"set skin without session", "PUT", "/api/v1/me/players/1/skin", "", http.StatusUnauthorized},
		{"set cape without session", "PUT", "/api/v1/me/players/1/cape", "", http.StatusUnauthorized},
		{"password without session", "POST", "/api/v1/me/password", "", http.StatusUnauthorized},
		{"profile without session", "PUT", "/api/v1/me/profile", "", http.StatusUnauthorized},
		{"account delete without session", "DELETE", "/api/v1/me", "", http.StatusUnauthorized},
		{"avatar without session", "PUT", "/api/v1/me/avatar", "", http.StatusUnauthorized},
		{"avatar reset without session", "DELETE", "/api/v1/me/avatar", "", http.StatusUnauthorized},
		{"closet without session", "GET", "/api/v1/me/closet", "", http.StatusUnauthorized},
		{"texture delete without session", "DELETE", "/api/v1/me/textures/1", "", http.StatusUnauthorized},
		{"skinlib patch without session", "PATCH", "/api/v1/skinlib/1", "", http.StatusUnauthorized},
		{"report without session", "POST", "/api/v1/skinlib/1/report", "", http.StatusUnauthorized},
		{"forgot with broken body", "POST", "/api/v1/password/forgot", "{", http.StatusBadRequest},
		{"forgot with non-object", "POST", "/api/v1/password/forgot", `[]`, http.StatusBadRequest},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body.String())
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
			var env envelope
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Fatalf("body is not a JSON envelope: %v (%s)", err, rec.Body.String())
			}
			if env.Error == "" {
				t.Error("`error` is empty: clients key their fallback message off it")
			}
			if env.Message == "" {
				t.Error("`message` is empty: an unknown code would render as nothing")
			}
			// Every branch carries a code now (errorgate_test.go fails the build
			// otherwise); this is the runtime proof that the gate's static rule
			// actually reaches the wire for these routes.
			if !errorgate.CodePattern.MatchString(env.Code) {
				t.Errorf("code %q is not a namespaced key (see internal/i18n)", env.Code)
			}
		})
	}
}
