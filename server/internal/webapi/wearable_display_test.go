package webapi

// GET /api/v1/me/wearable is the wear picker's payload. The picker wants to do
// two things with a row: show it a readable label, and let the player rename
// the closet entry behind it (PUT /api/v1/me/closet/{id} takes a *closet entry*
// id, not a texture id).
//
// Neither was safe to do. The query grouped the whole closet by texture_tid and
// took MAX(item_name) — the lexicographically largest alias — while ordering by
// MAX(c.id), the newest entry. user_closet has no unique key (0007 mirrors the
// legacy table, and data-compat rule 7 keeps it that way), and AddClosetEntry
// is a check-then-insert, so a double-click or an imported closet can leave two
// entries for one texture. When those two carry different aliases, the label the
// player reads and the entry a rename would touch are different rows.
//
// A row is now one closet entry: the newest for that texture, which is already
// what the ordering promised. `named` says whether a real name exists, so the
// picker can stop showing a hash as if it were a title.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"skin-nexus/server/internal/store"
)

type wearableRow struct {
	ID       int64  `json:"id"`
	Hash     string `json:"hash"`
	Label    string `json:"label"`
	ClosetID int64  `json:"closetId"`
	Named    *bool  `json:"named"`
}

func getWearable(t *testing.T, h http.Handler, typ string) []wearableRow {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me/wearable?type="+typ, nil)
	req.AddCookie(&http.Cookie{Name: "skin_session", Value: "sess-own"})
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("wearable(%s) = %d, body %s", typ, rec.Code, rec.Body.String())
	}
	var page struct {
		Textures []wearableRow `json:"textures"`
		Total    int           `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return page.Textures
}

func rowByHash(rows []wearableRow, hash string) *wearableRow {
	for i := range rows {
		if rows[i].Hash == hash {
			return &rows[i]
		}
	}
	return nil
}

// One texture, two closet entries, and the older one sorts higher as a string:
// the label and the renameable id must both come from the newest entry.
func TestWearableRowIsOneClosetEntry(t *testing.T) {
	h, m, uid, tid := provenanceFixture(t)
	ctx := context.Background()

	// Newest last, lexicographically smallest alias, so a MAX(item_name) over
	// the group would answer with the OTHER row.
	for _, alias := range []string{"zzz-older", "aaa-newest"} {
		if _, err := m.DB().ExecContext(ctx,
			`INSERT INTO user_closet (user_uid, texture_tid, item_name) VALUES (?, ?, ?)`,
			uid, tid, alias); err != nil {
			t.Fatal(err)
		}
	}
	var newest int64
	if err := m.DB().QueryRowContext(ctx,
		`SELECT MAX(id) FROM user_closet WHERE user_uid = ? AND texture_tid = ?`, uid, tid).
		Scan(&newest); err != nil {
		t.Fatal(err)
	}

	rows := getWearable(t, h, "skin")
	if len(rows) != 1 {
		t.Fatalf("two entries for one texture must collapse to one row, got %d", len(rows))
	}
	if rows[0].Label != "aaa-newest" {
		t.Fatalf("label = %q, want the newest entry's alias: a label taken from another row "+
			"cannot be renamed by the id we hand back", rows[0].Label)
	}
	if rows[0].ClosetID != newest {
		t.Fatalf("closetId = %d, want %d — the entry the label came from", rows[0].ClosetID, newest)
	}
	if rows[0].Named == nil || !*rows[0].Named {
		t.Fatalf("named = %v, want true", rows[0].Named)
	}
}

// An entry with neither alias nor texture description has no name to show. The
// payload must say so, instead of handing the picker a 64-char hash dressed up
// as a label.
func TestWearableMarksUnnamedEntries(t *testing.T) {
	h, m, uid, _ := provenanceFixture(t)
	ctx := context.Background()

	unnamed := strings.Repeat("b", 64)
	tid, err := m.AddTexture(ctx, uid, "skin", unnamed, "default", store.TextureMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddClosetEntry(ctx, uid, tid, ""); err != nil {
		t.Fatal(err)
	}

	row := rowByHash(getWearable(t, h, "skin"), unnamed)
	if row == nil {
		t.Fatal("the unnamed texture is missing from the wearable set")
	}
	if row.Named == nil || *row.Named {
		t.Fatalf("named = %v, want false for an entry with no alias and no description", row.Named)
	}
	if row.ClosetID == 0 {
		t.Fatal("closetId = 0, want the entry id so the picker can rename it")
	}
}
