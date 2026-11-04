package webapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"skin-nexus/server/internal/i18n"
	"skin-nexus/server/internal/store"
)

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
	created, err := b.Store.AddClosetEntry(r.Context(), uid, req.TextureID, strings.TrimSpace(req.ItemName))
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
	if err := b.Store.SetClosetItemName(r.Context(), uid, id, strings.TrimSpace(req.ItemName)); errors.Is(err, store.ErrNotFound) {
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
