package webapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"skin-nexus/server/internal/i18n"
	"skin-nexus/server/internal/store"
	"skin-nexus/server/internal/ugctext"
)

// closetAlias is the one policy for a closet label: strip what a renderer cannot
// undo, then bound it. item_name is a TEXT column, so the database imposes no
// ceiling of its own. Empty stays legal — it means "no alias".
//
// The reason comes back because the two failures are different conversations: a
// long alias is someone typing past the limit, an unreadable one is their client
// sending bytes the JSON decoder could not keep. Go replaces invalid UTF-8 with
// U+FFFD rather than erroring, so the handler only ever sees valid UTF-8 — the
// replacement character is the only trace left, and keeping it would store a
// label that renders as black diamonds everywhere.
const (
	aliasOK = iota
	aliasTooLong
	aliasUnreadable
)

func closetAlias(raw string) (string, int) {
	if strings.ContainsRune(raw, utf8.RuneError) {
		return "", aliasUnreadable
	}
	name := ugctext.Visible(strings.TrimSpace(raw))
	if len([]rune(name)) > 50 {
		return "", aliasTooLong
	}
	return name, aliasOK
}

func writeClosetAliasError(w http.ResponseWriter, reason int) {
	if reason == aliasUnreadable {
		writeErrCode(w, http.StatusBadRequest,
			"closet alias did not arrive as readable text", i18n.MsgInvalidValue)
		return
	}
	writeErrCode(w, http.StatusBadRequest, "closet alias is limited to 50 characters", i18n.MsgTooLong)
}

// GET /api/v1/me/closet — the user's closet entries with texture metadata.
func (b *Backend) handleClosetList(w http.ResponseWriter, r *http.Request) {
	uid, err := b.currentUser(r)
	if err != nil {
		writeErrCode(w, http.StatusUnauthorized, "not logged in", i18n.MsgUnauthorized)
		return
	}
	limit, offset := pageParams(r)
	rows, total, err := b.Store.ListClosetPage(r.Context(), uid,
		r.URL.Query().Get("q"), r.URL.Query().Get("type"), limit, offset)
	if err != nil {
		writeInternal(r, w, err)
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, c := range rows {
		item := map[string]any{
			"id": c.ID, "textureId": c.TextureID,
			"itemName": c.ItemName, "type": c.Type,
			"description": c.Description,           // the texture's own name, for pickers
			"model":       c.Model, "hash": c.Hash, // hash: closet search + dedupe hints
		}
		if c.Hash != "" {
			item["url"] = b.textureURL(r.Context(), c.Hash, c.Type)
		} else {
			item["url"] = "" // orphan legacy row — visible, removable
		}
		if c.Type == "skin" {
			item["previewURL"] = previewThumbURL(c.TextureID)
		} else {
			item["previewURL"] = ""
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"closet": out, "total": total})
}

// GET /api/v1/me/wearable?type=skin|cape — the compact set a player may wear.
// The picker used to read /me/textures, which both carried fields it never
// shows and missed the collected half of what is wearable.
//
// Paged and searchable (?page=&pageSize=&q=): the set is the whole closet, and a
// player who has collected for a year is not served a 400-row dropdown — that is
// also why the surface is a modal with thumbnails now instead of a <select>.
func (b *Backend) handleMyWearable(w http.ResponseWriter, r *http.Request) {
	uid, err := b.currentUser(r)
	if err != nil {
		writeErrCode(w, http.StatusUnauthorized, "not logged in", i18n.MsgUnauthorized)
		return
	}
	typ := r.URL.Query().Get("type")
	if typ != "skin" && typ != "cape" {
		writeErrCode(w, http.StatusBadRequest, "type must be skin|cape", i18n.MsgInvalidValue)
		return
	}
	limit, offset := pageParams(r)
	rows, total, err := b.Store.WearableTextures(r.Context(), uid, typ, r.URL.Query().Get("q"), limit, offset)
	if err != nil {
		writeInternal(r, w, err)
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, x := range rows {
		label := x.Name
		if x.Alias != "" {
			label = x.Alias // the user's own name for it wins, as in blessing
		}
		if label == "" {
			label = x.Hash
		}
		item := map[string]any{
			"id": x.ID, "hash": x.Hash, "model": x.Model, "label": label,
			// closetId is the entry this row stands for — the rename endpoint
			// takes an entry id, and a texture can sit in the closet twice.
			"closetId": x.ClosetID,
			// named distinguishes "this has a title" from "label is a hash".
			"named": x.Alias != "" || x.Name != "",
		}
		if typ == "skin" {
			item["previewURL"] = previewThumbURL(x.ID)
		} else {
			item["previewURL"] = "" // no body render for a cape; the flat sheet is the honest thumb
		}
		item["url"] = b.textureURL(r.Context(), x.Hash, typ)
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"textures": out, "total": total})
}

// POST /api/v1/me/closet {textureId, itemName?} — idempotent add.
func (b *Backend) handleClosetAdd(w http.ResponseWriter, r *http.Request) {
	uid, err := b.currentUser(r)
	if err != nil {
		writeErrCode(w, http.StatusUnauthorized, "not logged in", i18n.MsgUnauthorized)
		return
	}
	var req struct {
		TextureID int64  `json:"textureId"`
		ItemName  string `json:"itemName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrCode(w, http.StatusBadRequest, "invalid JSON", i18n.MsgBadBody)
		return
	}
	if req.TextureID == 0 {
		// Separate code: "no material selected" is actionable, "your request was
		// malformed" is not.
		writeErrCode(w, http.StatusBadRequest, "textureId required", i18n.MsgMissingField)
		return
	}
	itemName, reason := closetAlias(req.ItemName)
	if reason != aliasOK {
		writeClosetAliasError(w, reason)
		return
	}
	created, err := b.Store.AddClosetEntry(r.Context(), uid, req.TextureID, itemName)
	if errors.Is(err, store.ErrNotFound) {
		writeErrCode(w, http.StatusNotFound, "texture not found", i18n.MsgTextureNotFound)
		return
	}
	if err != nil {
		writeInternal(r, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "created": created})
}

// PUT /api/v1/me/closet/{id} {itemName} — rename the alias (legacy
// rename_closet_item_name).
func (b *Backend) handleClosetRename(w http.ResponseWriter, r *http.Request) {
	uid, err := b.currentUser(r)
	if err != nil {
		writeErrCode(w, http.StatusUnauthorized, "not logged in", i18n.MsgUnauthorized)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErrCode(w, http.StatusBadRequest, "bad id", i18n.MsgClosetBadID)
		return
	}
	var req struct {
		ItemName string `json:"itemName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrCode(w, http.StatusBadRequest, "invalid JSON", i18n.MsgBadBody)
		return
	}
	itemName, reason := closetAlias(req.ItemName)
	if reason != aliasOK {
		writeClosetAliasError(w, reason)
		return
	}
	if err := b.Store.SetClosetItemName(r.Context(), uid, id, itemName); errors.Is(err, store.ErrNotFound) {
		writeErrCode(w, http.StatusNotFound, "closet entry not found", i18n.MsgClosetNotFound)
		return
	} else if err != nil {
		writeInternal(r, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// DELETE /api/v1/me/closet/{id} — remove one entry (legacy remove_closet_item).
func (b *Backend) handleClosetRemove(w http.ResponseWriter, r *http.Request) {
	uid, err := b.currentUser(r)
	if err != nil {
		writeErrCode(w, http.StatusUnauthorized, "not logged in", i18n.MsgUnauthorized)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErrCode(w, http.StatusBadRequest, "bad id", i18n.MsgClosetBadID)
		return
	}
	if err := b.Store.RemoveClosetEntry(r.Context(), uid, id); errors.Is(err, store.ErrNotFound) {
		writeErrCode(w, http.StatusNotFound, "closet entry not found", i18n.MsgClosetNotFound)
		return
	} else if err != nil {
		writeInternal(r, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
