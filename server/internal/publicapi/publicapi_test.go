package publicapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Regression: Go 1.22+ ServeMux panics at registration on wildcard segments
// with a literal suffix ("GET /{player}.json" -> "bad wildcard segment").
// That panic only fires at runtime wiring, so constructing the handlers is
// itself the assertion.
func TestHandlerRegistrationDoesNotPanic(t *testing.T) {
	b := &Backend{}
	if b.Handler() == nil {
		t.Fatal("Handler() nil")
	}
	if b.WrapRoot(http.NotFoundHandler()) == nil {
		t.Fatal("WrapRoot() nil")
	}
}

func TestPlayerFromPath(t *testing.T) {
	cases := []struct {
		path, prefix, suffix string
		wantName             string
		wantOK               bool
	}{
		{"/Steve.json", "/", ".json", "Steve", true},
		{"/Alex.json", "/", ".json", "Alex", true},
		{"/usm/Steve.json", "/usm/", ".json", "Steve", true},
		{"/skin/Steve.png", "/skin/", ".png", "Steve", true},
		{"/cape/Steve.png", "/cape/", ".png", "Steve", true},
		// negative: no suffix / empty / multi-segment / wrong prefix
		{"/Steve", "/", ".json", "", false},
		{"/.json", "/", ".json", "", false},
		{"/a/b.json", "/", ".json", "", false},
		{"/skin/a/b.png", "/skin/", ".png", "", false},
		{"/usm/Steve.png", "/usm/", ".json", "", false},
		{"/other/Steve.json", "/usm/", ".json", "", false},
	}
	for _, c := range cases {
		got, ok := playerFromPath(c.path, c.prefix, c.suffix)
		if ok != c.wantOK || got != c.wantName {
			t.Errorf("playerFromPath(%q,%q,%q) = (%q,%v), want (%q,%v)",
				c.path, c.prefix, c.suffix, got, ok, c.wantName, c.wantOK)
		}
	}
}

// WrapRoot must intercept exactly GET|HEAD single-segment *.json and pass
// everything else to the next handler (SPA catch-all semantics preserved).
func TestWrapRootFallThrough(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	b := &Backend{}
	h := b.WrapRoot(next)

	for _, c := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/a/b.json", http.StatusTeapot},    // multi-segment -> SPA
		{"GET", "/index.html", http.StatusTeapot},  // wrong suffix -> SPA
		{"GET", "/api/v1/me", http.StatusTeapot},   // api path -> mux
		{"POST", "/Steve.json", http.StatusTeapot}, // method -> SPA (parity)
		// intercepted: nil store resolves to 404 inside the handler
		{"GET", "/Steve.json", http.StatusNotFound},
		{"HEAD", "/Alex.json", http.StatusNotFound},
	} {
		req := httptest.NewRequest(c.method, c.path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s %s -> %d, want %d", c.method, c.path, rec.Code, c.want)
		}
	}
}

// The legacy sub-paths must 404 (no store) instead of 500/panic once routed.
func TestLegacyRoutesReachHandlers(t *testing.T) {
	b := &Backend{}
	h := b.Handler()
	for _, path := range []string{"/usm/Steve.json", "/skin/Steve.png", "/cape/Steve.png", "/usm/a/b.json"} {
		req := httptest.NewRequest("GET", path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s -> %d, want 404", path, rec.Code)
		}
	}
}
