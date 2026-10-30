package webapi

// Skinlib API — blessing SkinlibController parity surface for the public
// texture library page. Like = closet add (legacy semantics: the heart
// button on the old site writes user_closet, Texture.php:65 likers()).
// Visibility/filter/sort/paging live in store.SkinlibList.

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"skin-nexus/server/internal/avatargen"
	"skin-nexus/server/internal/store"
)

// GET /api/v1/skinlib?filter=&keyword=&uploader=&sort=&page=
// Public endpoint: anonymous sees public textures only; the store layer
// applies visibility from the session (absent = anonymous).
func (b *Backend) handleSkinlibList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	uid, _ := b.currentUser(r) // 0 when anonymous — fine
	var admin bool
	if uid != 0 {
		perm, err := b.Store.UserPermission(r.Context(), uid)
		if err == nil && perm > 0 {
			admin = true
		}
	}
	page, _ := strconv.Atoi(q.Get("page"))
	uploader, _ := strconv.ParseInt(q.Get("uploader"), 10, 64)
	rows, total, err := b.Store.SkinlibList(r.Context(), store.SkinlibOpts{
		ViewerUID:   uid,
		ViewerAdmin: admin,
		Type:        q.Get("filter"),
		Keyword:     q.Get("keyword"),
		Uploader:    uploader,
		Sort:        q.Get("sort"),
		Page:        page,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		var uploadAt any
		if row.UploadAt.Valid {
			uploadAt = row.UploadAt.Time.UTC().Format("2006-01-02T15:04:05Z")
		}
		item := map[string]any{
			"tid":        row.TID,
			"name":       row.Name,
			"type":       row.Type,
			"model":      row.Model,
			"hash":       row.Hash,
			"public":     row.Public,
			"uploader":   row.UID,
			"nickname":   row.Nickname,
			"likes":      row.Likes,
			"liked":      row.Liked,
			"uploadAt":   uploadAt,
			"textureURL": b.textureURL(r.Context(), row.Hash, row.Type),
		}
		// Renderer version in the URL: previews are immutable-cached for a
		// year, so a rasterizer fix (e.g. HD-sheet sampling, v2) must change
		// the URL or browsers keep serving the old render.
		if row.Type == "skin" {
			item["previewURL"] = "/preview/" + strconv.FormatInt(row.TID, 10) + "?v=" + avatargen.RenderVersion + "&small=1"
		}
		items = append(items, item)
	}
	perPage := 20
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items, "total": total, "page": max(1, page), "perPage": perPage,
	})
}

// POST /api/v1/skinlib/{tid}/like — add to closet (idempotent).
func (b *Backend) handleSkinlibLike(w http.ResponseWriter, r *http.Request) {
	uid, err := b.currentUser(r)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "请先登录")
		return
	}
	tid, perr := strconv.ParseInt(r.PathValue("tid"), 10, 64)
	if perr != nil || tid <= 0 {
		writeErr(w, http.StatusBadRequest, "bad tid")
		return
	}
	created, err := b.Store.AddClosetEntry(r.Context(), uid, tid, "")
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "纹理不存在")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "like failed")
		return
	}
	likes, _ := b.Store.ClosetLikes(r.Context(), tid)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "created": created, "likes": likes})
}

// DELETE /api/v1/skinlib/{tid}/like — remove from closet (unlike).
func (b *Backend) handleSkinlibUnlike(w http.ResponseWriter, r *http.Request) {
	uid, err := b.currentUser(r)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "请先登录")
		return
	}
	tid, perr := strconv.ParseInt(r.PathValue("tid"), 10, 64)
	if perr != nil || tid <= 0 {
		writeErr(w, http.StatusBadRequest, "bad tid")
		return
	}
	if err := b.Store.RemoveClosetEntryByTID(r.Context(), uid, tid); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "未收藏")
		return
	} else if err != nil {
		writeErr(w, http.StatusInternalServerError, "unlike failed")
		return
	}
	likes, _ := b.Store.ClosetLikes(r.Context(), tid)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "likes": likes})
}

// PATCH /api/v1/skinlib/{tid} — owner renames / toggles visibility
// (blessing SkinlibController rename + privacy).
func (b *Backend) handleSkinlibPatch(w http.ResponseWriter, r *http.Request) {
	uid, err := b.currentUser(r)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "请先登录")
		return
	}
	tid, perr := strconv.ParseInt(r.PathValue("tid"), 10, 64)
	if perr != nil || tid <= 0 {
		writeErr(w, http.StatusBadRequest, "bad tid")
		return
	}
	var req struct {
		Name   *string `json:"name"`
		Public *bool   `json:"public"`
	}
	if err := jsonDecode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Name != nil {
		*req.Name = strings.TrimSpace(*req.Name)
		if len([]rune(*req.Name)) > 50 {
			writeErr(w, http.StatusBadRequest, "名称过长")
			return
		}
	}
	// Owner-scoped unless admin (admins may fix any row).
	owner := uid
	if perm, err := b.Store.UserPermission(r.Context(), uid); err == nil && perm > 0 {
		owner = 0
	}
	if err := b.Store.UpdateTextureMeta(r.Context(), tid, owner, req.Name, req.Public); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "纹理不存在")
		return
	} else if errors.Is(err, store.ErrForbidden) {
		writeErr(w, http.StatusForbidden, "只有上传者可以修改")
		return
	} else if err != nil {
		writeErr(w, http.StatusInternalServerError, "update failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
