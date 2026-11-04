package adminapi

// A malformed request body must be rejected before anything reaches for the
// database. The Backend below has a nil Store on purpose, exactly like
// strike_test.go: a handler that decoded after touching storage would panic
// instead of answering, and the panic would be the finding.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMalformedBodiesAreRejectedBeforeTheStore(t *testing.T) {
	cases := []struct {
		name     string
		path     string
		handler  func(*Backend, http.ResponseWriter, *http.Request)
		bodies   []string
		wantCode string
	}{
		{
			name:     "settings",
			path:     "/api/admin/v1/settings",
			handler:  func(b *Backend, w http.ResponseWriter, r *http.Request) { b.handleSettingsPut(w, r) },
			bodies:   []string{``, `{`, `{"key": }`, `[]`, `"a string"`, `{"key": true}`, `{"key": 1, "value": null}`},
			wantCode: "common.bad_body",
		},
		{
			name:     "oidc client",
			path:     "/api/admin/v1/oidc/clients",
			handler:  func(b *Backend, w http.ResponseWriter, r *http.Request) { b.handleOIDCClientsCreate(w, r) },
			bodies:   []string{``, `{`, `{"client_id": }`, `{"client_id": 7}`, `["x"]`, `{"client_id": "   "}`},
			wantCode: "common.bad_body",
		},
		{
			name:     "union blacklist",
			path:     "/api/admin/v1/union/blacklist",
			handler:  func(b *Backend, w http.ResponseWriter, r *http.Request) { b.handleUnionBlacklistAdd(w, r) },
			bodies:   []string{``, `{`, `{"email": }`, `{"email": 7}`, `{"email": "   "}`, `{"reason": "no address"}`},
			wantCode: "common.missing_field",
		},
	}
	for _, c := range cases {
		for _, body := range c.bodies {
			t.Run(c.name+"/"+strings.ReplaceAll(body, " ", "_"), func(t *testing.T) {
				b := &Backend{}
				r := httptest.NewRequest(http.MethodPost, c.path, strings.NewReader(body))
				r.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				c.handler(b, rec, r)

				if rec.Code != http.StatusBadRequest {
					t.Fatalf("body %q = %d, want 400 (%s)", body, rec.Code, rec.Body.String())
				}
				var env struct {
					Error   string `json:"error"`
					Code    string `json:"code"`
					Message string `json:"message"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
					t.Fatalf("body %q: reply is not the envelope: %v (%s)", body, err, rec.Body.String())
				}
				if env.Code != c.wantCode {
					t.Errorf("body %q code = %q, want %q", body, env.Code, c.wantCode)
				}
				if env.Error == "" || env.Message == "" {
					t.Errorf("body %q: envelope fields empty: %+v", body, env)
				}
			})
		}
	}
}
