package webapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"skin-nexus/server/internal/i18n"
	"skin-nexus/server/internal/store"
)

// A bare binary started without SKIN_AUTO_MIGRATE has no tables at all. NeedsSetup
// reports true for exactly that state (`users` is missing), so the wizard appears,
// the operator fills it in, and the first INSERT dies with an opaque driver error —
// the most common install failure there is. The answer must name the fix.
func TestSetupRefusesADatabaseWithNoTables(t *testing.T) {
	m, err := store.Open("sqlite", "sqlite:"+filepath.Join(t.TempDir(), "empty.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer m.Close()

	ctx := context.Background()
	if ok, err := m.TableExists(ctx, "users"); err != nil || ok {
		t.Fatalf("fixture wants a tableless database (users exists=%v err=%v)", ok, err)
	}
	need, err := m.NeedsSetup(ctx)
	if err != nil || !need {
		t.Fatalf("NeedsSetup = %v (%v), want the wizard to be reachable", need, err)
	}

	h := (&Backend{Store: m}).Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/setup",
		strings.NewReader(`{"site_name":"Rig","nickname":"admin","password":"password-1"}`)))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	if body.Code != i18n.MsgSetupDBMissing {
		t.Errorf("code = %q, want %q", body.Code, i18n.MsgSetupDBMissing)
	}
	if !strings.Contains(strings.ToLower(body.Message), "migrate") {
		t.Errorf("message %q does not name the fix (SKIN_AUTO_MIGRATE)", body.Message)
	}
}
