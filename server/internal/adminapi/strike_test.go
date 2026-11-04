package adminapi

// The panel's material list can now strike a texture without a player report
// behind it (站长自己看见违禁内容不该只能等举报). Two properties are worth
// pinning here rather than at the store: the id guard runs before anything
// reaches for the database — the Backend below has a nil Store on purpose, so a
// handler that touched it first would panic instead of answering 400 — and the
// verdict write goes through the same store call the report queue uses, so the
// two entry points cannot grow different meanings of「已下架」.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStrikeChecksTheIdBeforeTheStore(t *testing.T) {
	b := &Backend{AdminTok: "sekret"}
	for _, bad := range []string{"0", "-3", "abc", ""} {
		r := httptest.NewRequest(http.MethodPost, "/api/admin/v1/textures/"+bad+"/strike", nil)
		r.SetPathValue("tid", bad)
		rec := httptest.NewRecorder()
		b.handleTextureStrike(rec, r) // no panic means the guard ran first
		if rec.Code != http.StatusBadRequest {
			t.Errorf("tid %q = %d, want 400", bad, rec.Code)
			continue
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("tid %q: bad JSON %v", bad, err)
		}
		if body["code"] != "texture.bad_id" {
			t.Errorf("tid %q code = %v, want texture.bad_id", bad, body["code"])
		}
	}
}
