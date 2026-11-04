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

func TestSkinlibHugePageIsNotAFiveHundred(t *testing.T) {
	m, err := store.Open("sqlite", "sqlite:"+filepath.Join(t.TempDir(), "paging.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer m.Close()
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

	h := (&Backend{Store: m}).Handler()
	for _, page := range []string{"9223372036854775807", "99999999999999", "-5", "abc"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/skinlib?page="+page, nil))
		if rec.Code == http.StatusInternalServerError {
			t.Fatalf("page=%q produced a 500: %s", page, rec.Body.String())
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("page=%q status = %d, want 200 with an empty page: %s", page, rec.Code, rec.Body.String())
		}
		var body struct {
			Textures []any `json:"textures"`
			Total    int   `json:"total"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("page=%q decode: %v (%s)", page, err, rec.Body.String())
		}
		if body.Total != 1 {
			t.Fatalf("page=%q total = %d, want the count to stay honest", page, body.Total)
		}
	}
}
