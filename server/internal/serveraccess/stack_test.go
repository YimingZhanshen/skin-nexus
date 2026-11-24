package serveraccess

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStackDefaultsDisabledAndGatesWholeSubtree(t *testing.T) {
	s := NewStack(Config{})
	if s.Enabled() {
		t.Fatal("zero config must leave the module disabled")
	}
	called := false
	h := s.Gate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/serveraccess/v1/health", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("disabled health status = %d, want 404", rec.Code)
	}
	if called {
		t.Fatal("disabled gate reached the module handler")
	}
}

func TestHotToggleExposesOnlyHealthAndEmptyServerList(t *testing.T) {
	s := NewStack(Config{})
	h := s.Gate(s.Handler())
	s.SetEnabled(true)

	for _, tc := range []struct {
		path string
		want string
	}{
		{"/api/serveraccess/v1/health", `"status":"ok"`},
		{"/api/serveraccess/v1/servers", `"servers":[]`},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), tc.want) {
			t.Fatalf("GET %s = %d %s, want 200 containing %s", tc.path, rec.Code, rec.Body.String(), tc.want)
		}
	}

	for _, path := range []string{
		"/api/serveraccess/v1/tenants",
		"/api/serveraccess/v1/servers/srv_public/hasJoined",
		"/api/admin/v1/serveraccess/tenants",
		"/v/secret/sessionserver/session/minecraft/hasJoined",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d, want 404 (not implemented in S1)", path, rec.Code)
		}
	}

	s.SetEnabled(false)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/serveraccess/v1/health", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("health after hot-off = %d, want 404", rec.Code)
	}
}
