package main

// Assembled-mux guard. The hub-facing defect was not in a handler: the metadata
// handler had been setting Access-Control-Allow-Origin since 2026-10-24, and its
// package tests passed the whole time. What was missing was the registration
// that gets the request to that handler at the canonical URL. A test written one
// layer below cannot see that, so this one is written at the layer where the
// muxes compose.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func dispatch(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestMountCompatSurfacesAnswersCanonicalYggdrasilURL(t *testing.T) {
	// A handler that answers anything that reaches it, so the only variable
	// left under test is the routing.
	reached := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("reached"))
	})

	root := http.NewServeMux()
	mountCompatSurfaces(root, reached)

	// The URL the hub actually fetches, and the one
	// X-Authlib-Injector-API-Location advertises.
	if rec := dispatch(root, http.MethodGet, "/api/yggdrasil"); rec.Code != http.StatusOK || rec.Body.String() != "reached" {
		t.Fatalf("GET /api/yggdrasil = %d %q (Location %q); the canonical URL must reach the "+
			"yggdrasil handler instead of taking a mux-written 307 with no CORS headers",
			rec.Code, rec.Body.String(), rec.Header().Get("Location"))
	}
	// Both spellings stay live, and the subtree still routes everything below.
	for _, p := range []string{"/api/yggdrasil/", "/api/yggdrasil/authserver/authenticate", "/api/yggdrasil/sessionserver/session/minecraft/profile/x"} {
		if rec := dispatch(root, http.MethodGet, p); rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want the subtree to keep routing to the handler", p, rec.Code)
		}
	}
	// Registering GET must not quietly turn the prefix into a catch-all for
	// other methods on the bare path: that behaviour is left exactly as it was.
	if rec := dispatch(root, http.MethodPost, "/api/yggdrasil"); rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("POST /api/yggdrasil = %d, want the unchanged 307 (only GET was added)", rec.Code)
	}
	// And the redirect the mux does still emit must not be mistaken for a
	// response we control: it has no body of ours in it.
	if rec := dispatch(root, http.MethodPost, "/api/yggdrasil"); strings.Contains(rec.Body.String(), "reached") {
		t.Fatal("the 307 path reached the handler; the exact registration widened past GET")
	}
}
