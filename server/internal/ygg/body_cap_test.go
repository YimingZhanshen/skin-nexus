package ygg

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestYggJSONHandlersRejectOversizedBodies(t *testing.T) {
	api, _ := newTestAPI(t)
	h := api.Handler()
	padding := strings.Repeat("x", 128<<10)
	cases := []struct {
		name string
		path string
		body string
	}{
		{"authenticate", "/api/yggdrasil/authserver/authenticate", `{"username":"steve","password":"pw-ok","padding":"` + padding + `"}`},
		{"refresh", "/api/yggdrasil/authserver/refresh", `{"accessToken":"token","padding":"` + padding + `"}`},
		{"validate", "/api/yggdrasil/authserver/validate", `{"accessToken":"token","padding":"` + padding + `"}`},
		{"invalidate", "/api/yggdrasil/authserver/invalidate", `{"accessToken":"token","padding":"` + padding + `"}`},
		{"signout", "/api/yggdrasil/authserver/signout", `{"username":"steve","password":"pw-ok","padding":"` + padding + `"}`},
		{"join", "/api/yggdrasil/sessionserver/session/minecraft/join", `{"accessToken":"token","selectedProfile":{"id":"` + testUUID + `"},"serverId":"server","padding":"` + padding + `"}`},
		{"bulk lookup", "/api/yggdrasil/api/profiles/minecraft", `["` + padding + `"]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("oversized %s body = %d %s, want 413", tc.name, rec.Code, rec.Body.String())
			}
			var body map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("overflow response is not legacy JSON: %v", err)
			}
			if body["error"] != "IllegalArgumentException" || body["errorMessage"] != "request body too large" {
				t.Fatalf("overflow response = %v", body)
			}
		})
	}
}

func TestYggBodyCapChecksCompleteBody(t *testing.T) {
	api, _ := newTestAPI(t)
	const path = "/api/yggdrasil/authserver/validate"
	cases := []struct {
		name          string
		body          string
		contentLength int64
		want          int
	}{
		{
			name:          "declared length at cap with trailing whitespace",
			body:          `{}` + strings.Repeat(" ", (64<<10)-2),
			contentLength: 64 << 10,
			want:          http.StatusForbidden,
		},
		{
			name:          "unknown length at cap with trailing whitespace",
			body:          `{}` + strings.Repeat(" ", (64<<10)-2),
			contentLength: -1,
			want:          http.StatusForbidden,
		},
		{
			name:          "declared length over cap rejects before decode",
			body:          `{}`,
			contentLength: (64 << 10) + 1,
			want:          http.StatusRequestEntityTooLarge,
		},
		{
			name:          "known length trailing whitespace over cap",
			body:          `{}` + strings.Repeat(" ", 64<<10),
			contentLength: (64 << 10) + 2,
			want:          http.StatusRequestEntityTooLarge,
		},
		{
			name:          "unknown length trailing whitespace over cap",
			body:          `{}` + strings.Repeat(" ", 64<<10),
			contentLength: -1,
			want:          http.StatusRequestEntityTooLarge,
		},
		{
			name:          "unknown length extra content over cap",
			body:          `{}` + strings.Repeat("x", 64<<10),
			contentLength: -1,
			want:          http.StatusRequestEntityTooLarge,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(tc.body))
			req.ContentLength = tc.contentLength
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			api.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("body length %d (Content-Length %d) = %d %s, want %d", len(tc.body), tc.contentLength, rec.Code, rec.Body.String(), tc.want)
			}
		})
	}
}

type countingBodyReader struct {
	r io.Reader
	n int
}

func (r *countingBodyReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	r.n += n
	return n, err
}

func TestYggBodyCapStopsBeforeReadingEntireOversizedBody(t *testing.T) {
	api, _ := newTestAPI(t)
	payload := []byte(`{"username":"steve","password":"pw-ok","padding":"` + strings.Repeat("x", 1<<20) + `"}`)
	reader := &countingBodyReader{r: bytes.NewReader(payload)}
	req := httptest.NewRequest(http.MethodPost, "/api/yggdrasil/authserver/authenticate", reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized authenticate = %d, want 413", rec.Code)
	}
	if reader.n > (64<<10)+1 {
		t.Fatalf("handler read %d bytes; cap must stop at 65537, total body is %d", reader.n, len(payload))
	}
}

func TestYggBodyCapPreservesOptionalHandlerMalformedJSONSemantics(t *testing.T) {
	api, _ := newTestAPI(t)
	for _, path := range []string{
		"/api/yggdrasil/authserver/invalidate",
		"/api/yggdrasil/authserver/signout",
	} {
		rec := do(t, api.Handler(), http.MethodPost, path, `{malformed`, nil)
		if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
			t.Fatalf("%s malformed under-cap body = %d %q, want legacy 204", path, rec.Code, rec.Body.String())
		}
	}
}
