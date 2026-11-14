package webapi

// site-config must declare the OIDC capability. The SPA gates its authorized-
// apps and device pages on this flag: without it a deployment with OIDC off
// still offers the nav entry, and the page lands on a raw HTTP 404 because
// /api/v1/oidc/* is only mounted when the provider exists.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"skin-nexus/server/internal/store"
)

func TestSiteConfigDeclaresOIDCCapability(t *testing.T) {
	m, err := store.Open("sqlite", "sqlite:"+filepath.Join(t.TempDir(), "sc.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	read := func(b *Backend) map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		b.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/site-config", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("site-config: %d: %s", w.Code, w.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}

	off := read(&Backend{Store: m})
	v, ok := off["oidc_enabled"]
	if !ok {
		t.Fatal(`site-config carries no "oidc_enabled" — the SPA cannot tell "OIDC is off" from "the route broke"`)
	}
	if v != false {
		t.Fatalf("oidc_enabled = %v, want false for a Backend that did not declare OIDC", v)
	}

	on := read(&Backend{Store: m, OIDCEnabled: true})
	if v, ok := on["oidc_enabled"]; !ok || v != true {
		t.Fatalf("oidc_enabled = %v (present %v), want true when the Backend declares OIDC", v, ok)
	}
}
