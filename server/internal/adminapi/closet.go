package adminapi

// The admin closet surface: browse, alias and remove entries of ONE chosen
// user. The player-side closet already does all of this for yourself; the
// gap this fills is operational — a player's closet can hold a broken or
// unwanted entry (orphan legacy rows, a bad alias) and the operator had no
//处置入口 short of asking the player to do it. Everything is scoped by the
// {id} path parameter (never a query value), the alias rule is the shared
// ugctext.Alias, and both writes land in the audit log.

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"skin-nexus/server/internal/dbx"
	"skin-nexus/server/internal/i18n"
	"skin-nexus/server/internal/store"
	"skin-nexus/server/internal/ugctext"
)

// adminClosetPath reads the two path parameters every closet route carries.
// The uid half is the user whose closet is being managed; a bad value is a
// bad request before anything reaches the store (routeparams gate pins that
// the pattern names and the PathValue reads agree).
func adminClosetPath(r *http.Request, withEntry bool) (uid, id int64, ok bool) {
	uid, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || uid <= 0 {
		return 0, 0, false
	}
	if !withEntry {
		return uid, 0, true
	}
	id, err = strconv.ParseInt(r.PathValue("cid"), 10, 64)
	if err != nil || id <= 0 {
		return 0, 0, false
	}
	return uid, id, true
}

// GET /api/admin/v1/users/{id}/closet — one page of that user's closet with
// the same row shape the player list serves (minus the playable url: this is
// a moderation surface, the preview thumb is what an operator scans).
func (b *Backend) handleAdminClosetList(w http.ResponseWriter, r *http.Request) {
	uid, _, ok := adminClosetPath(r, false)
	if !ok {
		writeErrCode(w, http.StatusBadRequest, "bad user id", i18n.MsgBadID)
		return
	}
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	perPage, _ := strconv.Atoi(q.Get("perPage"))
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	rows, total, err := b.Store.ListClosetPage(r.Context(), uid,
		strings.TrimSpace(q.Get("q")), strings.TrimSpace(q.Get("typ")),
		perPage, dbx.Offset(page, perPage))
	if err != nil {
		writeErrCode(w, http.StatusInternalServerError, "closet query failed", i18n.MsgInternal)
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, c := range rows {
		item := map[string]any{
			"id": c.ID, "textureId": c.TextureID,
			"itemName": c.ItemName, "type": c.Type,
			"description": c.Description, "model": c.Model, "hash": c.Hash,
		}
		if c.Type == "skin" {
			item["previewURL"] = previewThumb(c.TextureID)
		} else {
			item["previewURL"] = "" // no body render for a cape
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"closet": out, "total": total, "page": page})
}

// PUT /api/admin/v1/users/{id}/closet/{cid} {itemName} — set (or clear, with
// an empty name) the entry's alias, under the same 50-rune rule the player
// surface uses.
func (b *Backend) handleAdminClosetRename(w http.ResponseWriter, r *http.Request) {
	uid, cid, ok := adminClosetPath(r, true)
	if !ok {
		writeErrCode(w, http.StatusBadRequest, "bad path id", i18n.MsgClosetBadID)
		return
	}
	var req struct {
		ItemName string `json:"itemName"`
	}
	if err := jsonDecode(r, &req); err != nil {
		writeErrCode(w, http.StatusBadRequest, "invalid JSON", i18n.MsgBadBody)
		return
	}
	name, reason := ugctext.Alias(req.ItemName)
	switch reason {
	case ugctext.AliasUnreadable:
		writeErrCode(w, http.StatusBadRequest, "alias did not arrive as readable text", i18n.MsgInvalidValue)
		return
	case ugctext.AliasTooLong:
		writeErrCode(w, http.StatusBadRequest, "alias is limited to 50 characters", i18n.MsgTooLong)
		return
	}
	if err := b.Store.SetClosetItemName(r.Context(), uid, cid, name); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErrCode(w, http.StatusNotFound, "closet entry not found", i18n.MsgClosetNotFound)
			return
		}
		writeErrCode(w, http.StatusInternalServerError, "closet rename failed", i18n.MsgInternal)
		return
	}
	b.Store.InsertAudit(r.Context(), actorUID(r), "admin_closet_rename",
		map[string]any{"uid": uid, "id": cid, "name": name})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// DELETE /api/admin/v1/users/{id}/closet/{cid} — drop one entry (the texture
// itself stays; only the collection row goes).
func (b *Backend) handleAdminClosetRemove(w http.ResponseWriter, r *http.Request) {
	uid, cid, ok := adminClosetPath(r, true)
	if !ok {
		writeErrCode(w, http.StatusBadRequest, "bad path id", i18n.MsgClosetBadID)
		return
	}
	if err := b.Store.RemoveClosetEntry(r.Context(), uid, cid); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErrCode(w, http.StatusNotFound, "closet entry not found", i18n.MsgClosetNotFound)
			return
		}
		writeErrCode(w, http.StatusInternalServerError, "closet remove failed", i18n.MsgInternal)
		return
	}
	b.Store.InsertAudit(r.Context(), actorUID(r), "admin_closet_remove",
		map[string]any{"uid": uid, "id": cid})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
