package account

// The status and the envelope an account error maps to are contract. The player
// API and the management API answer through these same two methods, so a code
// missing from the table would silently become a 500 on both surfaces — and a
// code that means 409 on one and 400 on the other is a bug this file exists to
// make impossible.

import (
	"net/http"
	"skin-nexus/server/internal/errorgate"
	"skin-nexus/server/internal/i18n"
	"strings"
	"testing"
)

// codeDirs are the packages that own error codes. The account package owns its
// own half; internal/i18n owns the HTTP-layer half.
var codeDirs = []string{".", "../i18n"}

// accountCodeValues is every wire-level code string this package declares
// (code → constant name), read from source so a new code cannot be forgotten by
// the table below.
func accountCodeValues(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for name, val := range errorgate.StringConsts(t, codeDirs) {
		if errorgate.CodePattern.MatchString(val) && strings.HasPrefix(name, "Code") {
			out[val] = name
		}
	}
	if len(out) < 10 {
		t.Fatalf("found only %d account codes — the prefix convention changed", len(out))
	}
	return out
}

// The texture-delete path answers with a code the i18n namespace owns (the web
// handlers raise the same one for a bad id in the URL, and one situation must
// not carry two codes). It is therefore not in the table above, which is scoped
// to this package's own constants — so its status gets pinned here instead.
func TestTextureNotFoundStatusFromI18nCode(t *testing.T) {
	if got := (&Error{Code: i18n.MsgTextureNotFound}).Status(); got != http.StatusNotFound {
		t.Errorf("Status() for %q = %d, want 404", i18n.MsgTextureNotFound, got)
	}
}

func TestErrorStatusCoversEveryCode(t *testing.T) {
	// A launcher retries on 429 and not on 400; a UI shows "already taken" on
	// 409. Pinned as a table so changing one is a deliberate edit, not a
	// refactor side effect.
	want := map[string]int{
		CodeNotFound:          http.StatusNotFound,
		CodeLibraryNotFnd:     http.StatusNotFound,
		CodeLimit:             http.StatusForbidden,
		CodeNotOwned:          http.StatusForbidden,
		CodeForbidden:         http.StatusForbidden,
		CodeViolationReupload: http.StatusForbidden,
		CodeNameTaken:         http.StatusConflict,
		CodeDuplicatePub:      http.StatusConflict,
		CodeInUse:             http.StatusConflict,
		CodeBusy:              http.StatusConflict,
		CodeInvalidName:       http.StatusBadRequest,
		CodeInvalid:           http.StatusBadRequest,
		CodeDimensions:        http.StatusBadRequest,
		CodePixelBomb:         http.StatusBadRequest,
		CodeMetadata:          http.StatusBadRequest,
		CodeModelBad:          http.StatusBadRequest,
		CodeSourceBad:         http.StatusBadRequest,
		CodeTypeMismatch:      http.StatusBadRequest,
		CodeSourceURLReq:      http.StatusBadRequest,
		CodeSourceURLBad:      http.StatusBadRequest,
		CodeTooLarge:          http.StatusRequestEntityTooLarge,
		CodeInternal:          http.StatusInternalServerError,
	}
	codes := accountCodeValues(t)
	for code := range want {
		if _, ok := codes[code]; !ok {
			t.Errorf("status table names %q, which is no longer a code of this package", code)
		}
	}
	for code, name := range codes {
		status, ok := want[code]
		if !ok {
			t.Errorf("%s (%q) has no entry in the status table and falls back to 500", name, code)
			continue
		}
		if got := (&Error{Code: code}).Status(); got != status {
			t.Errorf("Status() for %q = %d, want %d", code, got, status)
		}
	}
	if got := (&Error{Code: "not.a.real.code"}).Status(); got != http.StatusInternalServerError {
		t.Errorf("unknown code mapped to %d; answering 400 would blame the client for a code we do not know", got)
	}
}

func TestErrorBodyAlwaysCarriesCode(t *testing.T) {
	cases := []struct {
		name string
		err  *Error
	}{
		{"plain", &Error{Code: CodeNotFound, Message: "player not found"}},
		{"wrapped", &Error{Code: CodeInternal, Message: "db error", Err: http.ErrServerClosed}},
		{
			"duplicate flattens its details",
			&Error{Code: CodeDuplicatePub, Message: "already public",
				Details: map[string]any{"tid": int64(7)}},
		},
		{
			"in use names what still blocks",
			&Error{Code: CodeInUse, Message: "still equipped",
				Details: map[string]any{"players": []string{"Steve"}, "playerCount": 1}},
		},
		{
			"dimensions carry the numbers the uploader sent",
			&Error{Code: CodeDimensions, Message: "texture: dimensions not allowed: 100x100 for skin",
				Details: map[string]any{"width": uint32(100), "height": uint32(100), "type": "skin"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := tc.err.Body()
			for _, key := range []string{"error", "code", "message"} {
				v, ok := body[key].(string)
				if !ok || v == "" {
					t.Errorf("body[%q] = %v, want a non-empty string", key, body[key])
				}
			}
			if body["code"] != tc.err.Code {
				t.Errorf("code = %v, want %q", body["code"], tc.err.Code)
			}
			if !errorgate.CodePattern.MatchString(tc.err.Code) {
				t.Errorf("code %q is not a namespaced key", tc.err.Code)
			}
			for k := range tc.err.Details {
				if _, ok := body[k]; !ok {
					t.Errorf("detail %q was not flattened into the body", k)
				}
			}
		})
	}
	// A code that does not advertise details must not leak them: they are
	// internal bookkeeping, not a client contract.
	plain := (&Error{Code: CodeLimit, Message: "too many",
		Details: map[string]any{"players": "should not appear"}}).Body()
	if _, ok := plain["players"]; ok {
		t.Error("details leaked for a code that does not advertise them")
	}
}
