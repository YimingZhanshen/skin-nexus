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
		writeErrCode(w, http.StatusUnauthorized, "请先登录", i18n.MsgUnauthorized)
		return
	}
	rows, err := b.Store.ListCloset(r.Context(), uid)
	if err != nil {
		writeErrCode(w, http.StatusInternalServerError, "query failed", i18n.MsgInternal)
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, c := range rows {
		item := map[string]any{
			"id": c.ID, "textureId": c.TextureID,
			"itemName": c.ItemName, "type": c.Type,
			"model": c.Model, "hash": c.Hash, // hash: closet search + dedupe hints
		}
		if c.Hash != "" {
			item["url"] = b.textureURL(r.Context(), c.Hash, c.Type)
		} else {
			item["url"] = "" // orphan legacy row — visible, removable
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"closet": out})
}

// POST /api/v1/me/closet {textureId, itemName?} — idempotent add.
func (b *Backend) handleClosetAdd(w http.ResponseWriter, r *http.Request) {
	uid, err := b.currentUser(r)
	if err != nil {
		writeErrCode(w, http.StatusUnauthorized, "请先登录", i18n.MsgUnauthorized)
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
		writeErrCode(w, http.StatusNotFound, "纹理不存在", i18n.MsgTextureNotFound)
		return
	}
	if err != nil {
		writeErrCode(w, http.StatusInternalServerError, "insert failed", i18n.MsgInternal)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "created": created})
}

// PUT /api/v1/me/closet/{id} {itemName} — rename the alias (legacy
// rename_closet_item_name).
func (b *Backend) handleClosetRename(w http.ResponseWriter, r *http.Request) {
	uid, err := b.currentUser(r)
	if err != nil {
		writeErrCode(w, http.StatusUnauthorized, "请先登录", i18n.MsgUnauthorized)
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
		writeErrCode(w, http.StatusNotFound, "条目不存在", i18n.MsgClosetNotFound)
		return
	} else if err != nil {
		writeErrCode(w, http.StatusInternalServerError, "update failed", i18n.MsgInternal)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// DELETE /api/v1/me/closet/{id} — remove one entry (legacy remove_closet_item).
func (b *Backend) handleClosetRemove(w http.ResponseWriter, r *http.Request) {
	uid, err := b.currentUser(r)
	if err != nil {
		writeErrCode(w, http.StatusUnauthorized, "请先登录", i18n.MsgUnauthorized)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErrCode(w, http.StatusBadRequest, "bad id", i18n.MsgClosetBadID)
		return
	}
	if err := b.Store.RemoveClosetEntry(r.Context(), uid, id); errors.Is(err, store.ErrNotFound) {
		writeErrCode(w, http.StatusNotFound, "条目不存在", i18n.MsgClosetNotFound)
		return
	} else if err != nil {
		writeErrCode(w, http.StatusInternalServerError, "delete failed", i18n.MsgInternal)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
