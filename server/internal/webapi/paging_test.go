package webapi

// A page number is a query parameter, so its arithmetic is attacker-controlled.
// (page-1)*limit used to overflow into a negative OFFSET, which the engine
// rejects — an anonymous request turned into a 500 and a database error log.
import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"skin-nexus/server/internal/dialect"
	"skin-nexus/server/internal/migrate"
	"skin-nexus/server/internal/store"
)

// rig returns a handler over a migrated store holding exactly one public
// texture, so a page past the end has a known shape: no rows, honest total.
func rig(t *testing.T) http.Handler {
	t.Helper()
	m, err := store.Open("sqlite", "sqlite:"+filepath.Join(t.TempDir(), "paging.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	if err := migrate.ApplySQLDirTracked(m.DB(), filepath.Join("..", "..", "migrations"), dialect.SQLite); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	ctx := context.Background()
	uid, err := m.CreateUser(ctx, "pager@example.com", "pager", "x", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddTexture(ctx, uid, "skin", "f3e0b0b3d61f0d1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192", "default", store.TextureMeta{Description: "one"}); err != nil {
		t.Fatal(err)
	}
	return (&Backend{Store: m}).Handler()
}

func decodeSkinlibPage(t *testing.T, body []byte) (int, []any) {
	t.Helper()
	var got struct {
		Items []any `json:"items"`
		Total int   `json:"total"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	return got.Total, got.Items
}

func TestSkinlibHugePageIsNotAFiveHundred(t *testing.T) {
	h := rig(t)
	for _, page := range []string{"9223372036854775807", "99999999999999", "-5", "abc"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/skinlib?page="+page, nil))
		if rec.Code == http.StatusInternalServerError {
			t.Fatalf("page=%q produced a 500: %s", page, rec.Body.String())
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("page=%q status = %d, want 200 with an empty page: %s", page, rec.Code, rec.Body.String())
		}
		total, _ := decodeSkinlibPage(t, rec.Body.Bytes())
		if total != 1 {
			t.Fatalf("page=%q total = %d, want the count to stay honest", page, total)
		}
	}
}

// The status check above is not enough, and SQLite is why. A hand-computed
// (page-1)*perPage wraps to a negative offset; MySQL and PostgreSQL answer that
// with their own error — the 500 A3 was filed to remove — but SQLite reads a
// negative OFFSET as "start at zero" and cheerfully returns the FIRST page for a
// page number past the end. Same request, two different bugs, and only one of
// them is a 500. So assert the rows: a page beyond the end must be empty here,
// on the engine that does not complain.
func TestSkinlibPagePastTheEndReturnsNoRows(t *testing.T) {
	h := rig(t)
	for _, page := range []string{"9223372036854775807", "99999999999999", "4611686018427387907"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/skinlib?page="+page, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("page=%q status = %d, want 200: %s", page, rec.Code, rec.Body.String())
		}
		total, items := decodeSkinlibPage(t, rec.Body.Bytes())
		if len(items) != 0 {
			t.Errorf("page=%q returned %d rows of %d total; a page past the end must be empty, not the first page again", page, len(items), total)
		}
	}
}
