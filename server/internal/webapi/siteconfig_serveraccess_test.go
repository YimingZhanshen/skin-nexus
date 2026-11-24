package webapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"skin-nexus/server/internal/store"
)

func TestSiteConfigDeclaresLiveServerAccessCapability(t *testing.T) {
	m, err := store.Open("sqlite", "sqlite:"+filepath.Join(t.TempDir(), "sc.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	read := func(b *Backend) bool {
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
		v, ok := body["serveraccess_enabled"]
		if !ok {
			t.Fatal(`site-config carries no "serveraccess_enabled" — the SPA cannot distinguish default-off from a broken capability response`)
		}
		enabled, ok := v.(bool)
		if !ok {
			t.Fatalf("serveraccess_enabled = %v, want boolean", v)
		}
		return enabled
	}

	if read(&Backend{Store: m}) {
		t.Fatal("serveraccess_enabled must default to false")
	}
	enabled := false
	b := &Backend{Store: m, ServerAccessEnabled: func() bool { return enabled }}
	if read(b) {
		t.Fatal("serveraccess_enabled = true before the module gate opened")
	}
	enabled = true
	if !read(b) {
		t.Fatal("serveraccess_enabled stayed false after the live module gate opened")
	}
}
