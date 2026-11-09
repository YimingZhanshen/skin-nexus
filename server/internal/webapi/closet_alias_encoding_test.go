package webapi

// The wear picker can rename a closet entry now, which puts a text input in
// front of a TEXT column that used to be reached only from the closet page.
//
// The length and invisible-character policy was already here (see
// TestClosetItemNameIsBoundedAndVisible), but encoding was not — and the check
// that looks right does not work: Go's json.Decoder replaces invalid UTF-8 with
// U+FFFD instead of failing, so by the time the handler holds the string it is
// valid UTF-8, just no longer what the client meant. Live on the local rig a
// GBK-encoded alias sent with curl returned 200 and stored four U+FFFD, which
// every surface then renders as a row of black diamonds with nothing to explain
// it. The upload path does catch those bytes, because multipart fields are not
// JSON-decoded (internal/account/textures.go checks utf8.ValidString on the raw
// value) — so the two ways of naming the same thing disagreed.
//
// A replacement character in a label can only mean "this arrived unreadable",
// so it is refused rather than kept.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"skin-nexus/server/internal/store"
)

// mojibake is what four GBK bytes look like once the JSON decoder is done with
// them: valid UTF-8, unreadable text.
var mojibake = strings.Repeat("\uFFFD", 4)

func closetCall(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "skin_session", Value: "sess-own"})
	h.ServeHTTP(rec, req)
	return rec
}

type closetEntry struct {
	ID       int64  `json:"id"`        // the closet entry id PUT /me/closet/{id} takes
	TID      int64  `json:"textureId"` // /me/closet spells it this way; /me/wearable calls it "id"
	ItemName string `json:"itemName"`
}

func closetEntries(t *testing.T, h http.Handler) []closetEntry {
	t.Helper()
	rec := closetCall(t, h, http.MethodGet, "/api/v1/me/closet?page=1&pageSize=50", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("closet list = %d, body %s", rec.Code, rec.Body.String())
	}
	var page struct {
		Closet []closetEntry `json:"closet"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode closet: %v / %s", err, rec.Body.String())
	}
	return page.Closet
}

func entryFor(rows []closetEntry, tid int64) *closetEntry {
	for i := range rows {
		if rows[i].TID == tid {
			return &rows[i]
		}
	}
	return nil
}

func assertRefused(t *testing.T, rec *httptest.ResponseRecorder, what string) {
	t.Helper()
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("%s = %d, want 400: unreadable text must not be stored silently (body %s)",
			what, rec.Code, rec.Body.String())
	}
	var errBody struct{ Code string }
	if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil {
		t.Fatal(err)
	}
	if errBody.Code != "common.invalid_value" {
		t.Fatalf("%s code = %q, want common.invalid_value", what, errBody.Code)
	}
}

func TestClosetAliasRefusesUnreadableEncoding(t *testing.T) {
	h, m, uid, tid := provenanceFixture(t)

	if rec := closetCall(t, h, http.MethodPost, "/api/v1/me/closet",
		`{"textureId":`+strconv.FormatInt(tid, 10)+`,"itemName":"readable"}`); rec.Code != http.StatusOK {
		t.Fatalf("seed closet entry = %d, body %s", rec.Code, rec.Body.String())
	}
	own := entryFor(closetEntries(t, h), tid)
	if own == nil || own.ItemName != "readable" {
		t.Fatalf("seed entry = %+v", own)
	}

	// Rename: wholly unreadable, and partly unreadable, are both a loss.
	for _, body := range []string{
		`{"itemName":"` + mojibake + `"}`,
		`{"itemName":"ok` + mojibake + `"}`,
	} {
		assertRefused(t, closetCall(t, h, http.MethodPut,
			"/api/v1/me/closet/"+strconv.FormatInt(own.ID, 10), body), "PUT "+body)
		if after := entryFor(closetEntries(t, h), tid); after.ItemName != "readable" {
			t.Fatalf("the refused rename still wrote %q", after.ItemName)
		}
	}

	// Add takes the same field, so it gets the same answer.
	other, err := m.AddTexture(t.Context(), uid, "skin", strings.Repeat("c", 64), "default",
		store.TextureMeta{})
	if err != nil {
		t.Fatal(err)
	}
	assertRefused(t, closetCall(t, h, http.MethodPost, "/api/v1/me/closet",
		`{"textureId":`+strconv.FormatInt(other, 10)+`,"itemName":"`+mojibake+`"}`), "POST add")
	if entryFor(closetEntries(t, h), other) != nil {
		t.Fatal("the refused add still created an entry")
	}

	// And the guard is not a blanket ban: a clean alias still lands.
	if rec := closetCall(t, h, http.MethodPut,
		"/api/v1/me/closet/"+strconv.FormatInt(own.ID, 10), `{"itemName":"改名了"}`); rec.Code != http.StatusOK {
		t.Fatalf("a readable rename = %d, body %s", rec.Code, rec.Body.String())
	}
	if after := entryFor(closetEntries(t, h), tid); after.ItemName != "改名了" {
		t.Fatalf("clean rename stored %q, want 改名了", after.ItemName)
	}
}
