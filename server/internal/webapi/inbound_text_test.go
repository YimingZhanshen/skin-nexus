package webapi

// The site naming rule is read on upload only. A rename goes through a different
// handler that checks length and nothing else, so the rule is a door anyone can
// walk around by uploading a legal name and then editing it.
import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPatchRenameObeysTheSiteNamingRule(t *testing.T) {
	h, m, _, tid := provenanceFixture(t)
	if err := m.SetOption(context.Background(), "texture_name_regexp", `^[A-Za-z0-9 _-]+$`); err != nil {
		t.Fatal(err)
	}
	rec := patchTexture(h, tid, `{"name":"bad/name"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("rename against the naming rule = %d, want 400; body %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "texture.metadata_invalid" {
		t.Fatalf("code = %q, want the same code upload uses", body.Code)
	}
	var stored string
	if err := m.DB().QueryRowContext(context.Background(),
		`SELECT description FROM textures WHERE id = ?`, tid).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != "mine" {
		t.Fatalf("a rejected rename still stored %q", stored)
	}
	// A name the rule allows must still go through.
	if rec := patchTexture(h, tid, `{"name":"good name"}`); rec.Code != http.StatusOK {
		t.Fatalf("compliant rename = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
}

// A closet alias lands in a TEXT column with no ceiling, and the handler only
// trims it: an invisible control character keeps its length while changing what
// the list shows, and a megabyte-long alias is one request away.
func TestClosetItemNameIsBoundedAndVisible(t *testing.T) {
	h, m, _, tid := provenanceFixture(t)
	post := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/v1/me/closet", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "skin_session", Value: "sess-own"})
		h.ServeHTTP(rec, req)
		return rec
	}
	long, _ := json.Marshal(map[string]any{"textureId": tid, "itemName": strings.Repeat("x", 51)})
	rec := post(string(long))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("51-rune alias = %d, want 400; body %s", rec.Code, rec.Body.String())
	}
	var errBody struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil {
		t.Fatal(err)
	}
	if errBody.Code != "common.too_long" {
		t.Fatalf("code = %q, want common.too_long", errBody.Code)
	}
	var count int
	if err := m.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM user_closet WHERE user_uid = (SELECT id FROM users WHERE email = 'own@example.com')`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("a rejected alias still created %d closet rows", count)
	}
	// A legal alias is stored with its invisible characters stripped.
	ok, _ := json.Marshal(map[string]any{"textureId": tid, "itemName": "‮my cloak"})
	if rec := post(string(ok)); rec.Code != http.StatusOK {
		t.Fatalf("legal alias = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	var stored string
	if err := m.DB().QueryRowContext(context.Background(),
		`SELECT COALESCE(item_name,'') FROM user_closet LIMIT 1`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != "my cloak" {
		t.Fatalf("stored alias = %q, want the override removed", stored)
	}
}
