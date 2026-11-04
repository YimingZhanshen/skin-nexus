package webapi

// source_url is rendered into an href on the texture detail page. Until now a
// PATCH that touched only that field skipped the scheme check whenever the row's
// provenance said "original", so any scheme the browser will execute could be
// stored — the only thing standing between that and a stored XSS was Go's
// template urlFilter rewriting it to #ZgotmplZ.
import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"skin-nexus/server/internal/dialect"
	"skin-nexus/server/internal/migrate"
	"skin-nexus/server/internal/store"
)

func provenanceFixture(t *testing.T) (http.Handler, *store.Mysql, int64, int64) {
	t.Helper()
	m, err := store.Open("sqlite", "sqlite:"+filepath.Join(t.TempDir(), "prov.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	if err := migrate.ApplySQLDirTracked(m.DB(), filepath.Join("..", "..", "migrations"), dialect.SQLite); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	ctx := context.Background()
	uid, err := m.CreateUser(ctx, "own@example.com", "owner", "x", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.CreateSession(ctx, "sess-own", uid, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	tid, err := m.AddTexture(ctx, uid, "skin", strings.Repeat("a", 64), "default", store.TextureMeta{Description: "mine", Source: "original"})
	if err != nil {
		t.Fatal(err)
	}
	return (&Backend{Store: m}).Handler(), m, uid, tid
}

func patchTexture(h http.Handler, tid int64, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/api/v1/skinlib/"+strconv.FormatInt(tid, 10), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "skin_session", Value: "sess-own"})
	h.ServeHTTP(rec, req)
	return rec
}

func TestPatchRejectsExecutableSourceURLs(t *testing.T) {
	h, m, _, tid := provenanceFixture(t)
	for _, bad := range []string{"javascript:alert(1)", "data:text/html,<script>alert(1)</script>", "javascript:alert(1)//", "ftp://example/x"} {
		rec := patchTexture(h, tid, `{"sourceUrl":"`+bad+`"}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("PATCH sourceUrl=%q = %d, want 400; body %s", bad, rec.Code, rec.Body.String())
		}
		var body struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if body.Code != "texture.source_url_invalid" {
			t.Fatalf("PATCH sourceUrl=%q code = %q, want texture.source_url_invalid", bad, body.Code)
		}
	}
	if got := sourceURL(t, m, tid); got != "" {
		t.Fatalf("a rejected PATCH still stored %q", got)
	}
}

func TestPatchStillAcceptsHonestLinks(t *testing.T) {
	h, m, _, tid := provenanceFixture(t)
	rec := patchTexture(h, tid, `{"sourceUrl":"https://example.com/skin/1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid link = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if got := sourceURL(t, m, tid); got != "https://example.com/skin/1" {
		t.Fatalf("stored source_url = %q", got)
	}
	// Clearing is still allowed.
	if rec := patchTexture(h, tid, `{"sourceUrl":""}`); rec.Code != http.StatusOK {
		t.Fatalf("clearing the link = %d, want 200", rec.Code)
	}
}

// sourceURL reads the column straight: the point of the test is what landed in
// the row, not what some handler chooses to report back.
func sourceURL(t *testing.T, m *store.Mysql, tid int64) string {
	t.Helper()
	var v string
	err := m.DB().QueryRowContext(context.Background(), `SELECT COALESCE(source_url,'') FROM textures WHERE id = ?`, tid).Scan(&v)
	if err != nil {
		t.Fatalf("read source_url: %v", err)
	}
	return v
}

// The same PATCH path is where a name reaches the library. A BiDi override in
// the name changes what a reader sees while keeping the length check happy, so
// it has to be stripped on the way in, not hoped away at render time.
func TestPatchStripsInvisibleControls(t *testing.T) {
	h, m, _, tid := provenanceFixture(t)
	rec := patchTexture(h, tid, `{"name":"‮gnails","detail":"a​b"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	var name, detail string
	if err := m.DB().QueryRowContext(context.Background(),
		`SELECT description, COALESCE(detail_md,'') FROM textures WHERE id = ?`, tid).Scan(&name, &detail); err != nil {
		t.Fatal(err)
	}
	if name != "gnails" {
		t.Fatalf("stored name = %q, want the override removed", name)
	}
	if detail != "ab" {
		t.Fatalf("stored description = %q, want the zero-width space removed", detail)
	}
}
