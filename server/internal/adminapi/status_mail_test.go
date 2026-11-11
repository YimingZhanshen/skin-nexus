package adminapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

// The status page carries the 24h undelivered-letter count so an operator sees
// a broken mail path before players report it. A failing count must degrade to
// zero, never turn /status into a 500.
func TestStatusReportsMailFailures(t *testing.T) {
	b := newDiagBackend(t)
	ctx := context.Background()
	now := time.Now().UTC()
	b.Store.InsertAudit(ctx, 1, "mail_test_failed", nil)
	b.Store.InsertAudit(ctx, 1, "password_reset_mail_failed", nil)
	b.Store.InsertAudit(ctx, 1, "mail_test_sent", nil)
	b.Store.InsertAudit(ctx, 1, "email_verify_mail_failed", nil)
	_ = now

	w := httptest.NewRecorder()
	b.handleStatus(w, httptest.NewRequest("GET", "/api/admin/v1/status", nil))
	if w.Code != 200 {
		t.Fatalf("status = %d %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if got, ok := body["mailFailures24h"].(float64); !ok || got != 3 {
		t.Fatalf("mailFailures24h = %v, want 3", body["mailFailures24h"])
	}
	for _, key := range []string{"version", "goVersion", "dbVersion", "dbEngine", "migration", "build", "runtime", "environment", "modules", "signKeyFingerprint"} {
		if _, ok := body[key]; !ok {
			t.Errorf("status lost existing field %q", key)
		}
	}
}
