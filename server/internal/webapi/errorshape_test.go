package webapi

// errorshape_test.go pins the response envelope itself. These paths need no
// database: every /me/* handler resolves the session before it touches a store,
// and the forgot-password handler rejects a malformed body before it does, so
// the whole error shape is reachable from a zero-value Backend.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"skin-nexus/server/internal/account"
)

// codePattern is the namespace rule from internal/i18n: <area>.<reason>, plus
// the single unprefixed `internal` fallback.
var codePattern = regexp.MustCompile(`^([a-z][a-z0-9_]*(\.[a-z0-9_]+)+|internal)$`)

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
			// `code` is optional until the budget in testdata/error-budget.txt
			// reaches zero, but a code that is present is already API surface —
			// a malformed one is invisible to every language pack.
			if env.Code != "" && !codePattern.MatchString(env.Code) {
				t.Errorf("code %q is not a namespaced key (see internal/i18n)", env.Code)
			}
		})
	}
}

// accountCodeValues is every wire-level code string declared in internal/account
// (code → constant name). That package owns both the codes and their HTTP
// mapping, so a new code must not default to 500 unnoticed.
func accountCodeValues(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for name, val := range codeConsts(t) {
		if codePattern.MatchString(val) && strings.HasPrefix(name, "Code") {
			out[val] = name
		}
	}
	if len(out) < 10 {
		t.Fatalf("found only %d account codes — the prefix convention changed", len(out))
	}
	return out
}

func TestAccountErrStatusCoversEveryCode(t *testing.T) {
	// The status a code maps to is client-visible behavior: a launcher that
	// retries on 429 and not on 400, a UI that shows "already taken" on 409.
	// Pinned as a table, so changing one is a deliberate edit, not a refactor
	// side effect.
	want := map[string]int{
		account.CodeNotFound:      http.StatusNotFound,
		account.CodeLibraryNotFnd: http.StatusNotFound,
		account.CodeLimit:         http.StatusForbidden,
		account.CodeNotOwned:      http.StatusForbidden,
		account.CodeForbidden:     http.StatusForbidden,
		account.CodeNameTaken:     http.StatusConflict,
		account.CodeDuplicatePub:  http.StatusConflict,
		account.CodeInUse:         http.StatusConflict,
		account.CodeBusy:          http.StatusConflict,
		account.CodeInvalidName:   http.StatusBadRequest,
		account.CodeInvalid:       http.StatusBadRequest,
		account.CodeTypeMismatch:  http.StatusBadRequest,
		account.CodeSourceURLReq:  http.StatusBadRequest,
		account.CodeSourceURLBad:  http.StatusBadRequest,
		account.CodeTooLarge:      http.StatusRequestEntityTooLarge,
		account.CodeInternal:      http.StatusInternalServerError,
	}
	codes := accountCodeValues(t)
	for code := range want {
		if _, ok := codes[code]; !ok {
			t.Errorf("status table names %q, which is no longer an account code", code)
		}
	}
	for code, name := range codes {
		status, ok := want[code]
		if !ok {
			t.Errorf("account code %s (%q) has no entry in the status table and falls back to 500", name, code)
			continue
		}
		if got := accountErrStatus(code); got != status {
			t.Errorf("accountErrStatus(%q) = %d, want %d", code, got, status)
		}
	}
	if got := accountErrStatus("not.a.real.code"); got != http.StatusInternalServerError {
		t.Errorf("unknown code mapped to %d; answering 400 would blame the client for a code we do not know", got)
	}
}

func TestAccountErrBodyAlwaysCarriesCode(t *testing.T) {
	cases := []struct {
		name string
		err  *account.Error
	}{
		{"plain", &account.Error{Code: account.CodeNotFound, Message: "player not found"}},
		{"wrapped", &account.Error{Code: account.CodeInternal, Message: "db error", Err: http.ErrServerClosed}},
		{
			"duplicate flattens its details",
			&account.Error{Code: account.CodeDuplicatePub, Message: "already public",
				Details: map[string]any{"tid": int64(7)}},
		},
		{
			"in use names what still blocks",
			&account.Error{Code: account.CodeInUse, Message: "still equipped",
				Details: map[string]any{"players": []string{"Steve"}, "playerCount": 1}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := accountErrBody(tc.err)
			for _, key := range []string{"error", "code", "message"} {
				v, ok := body[key].(string)
				if !ok || v == "" {
					t.Errorf("body[%q] = %v, want a non-empty string", key, body[key])
				}
			}
			if body["code"] != tc.err.Code {
				t.Errorf("code = %v, want %q", body["code"], tc.err.Code)
			}
			if !codePattern.MatchString(tc.err.Code) {
				t.Errorf("code %q is not a namespaced key", tc.err.Code)
			}
			for k := range tc.err.Details {
				if _, ok := body[k]; !ok {
					t.Errorf("detail %q was not flattened into the body", k)
				}
			}
		})
	}
	// A code that is not one of the two detail-carrying codes must not leak
	// details: they are internal bookkeeping, not a client contract.
	plain := accountErrBody(&account.Error{Code: account.CodeLimit, Message: "too many",
		Details: map[string]any{"players": "should not appear"}})
	if _, ok := plain["players"]; ok {
		t.Error("details leaked for a code that does not advertise them")
	}
}
