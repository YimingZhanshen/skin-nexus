package webapi

// Skinlib API — blessing SkinlibController parity surface for the public
// texture library page. Like = closet add (legacy semantics: the heart
// button on the old site writes user_closet, Texture.php:65 likers()).
// Visibility/filter/sort/paging live in store.SkinlibList.

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"skin-nexus/server/internal/account"
	"skin-nexus/server/internal/avatargen"
	"skin-nexus/server/internal/i18n"
	"skin-nexus/server/internal/store"
	"skin-nexus/server/internal/ygg"
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
		writeErrCode(w, http.StatusInternalServerError, "list failed", i18n.MsgInternal)
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
			"source":     row.Source,
			"sourceUrl":  row.SourceURL,
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
		writeErrCode(w, http.StatusUnauthorized, "请先登录", i18n.MsgUnauthorized)
		return
	}
	tid, perr := strconv.ParseInt(r.PathValue("tid"), 10, 64)
	if perr != nil || tid <= 0 {
		writeErrCode(w, http.StatusBadRequest, "bad tid", i18n.MsgTextureBadID)
		return
	}
	created, err := b.Store.AddClosetEntry(r.Context(), uid, tid, "")
	if errors.Is(err, store.ErrNotFound) {
		writeErrCode(w, http.StatusNotFound, "纹理不存在", i18n.MsgTextureNotFound)
		return
	}
	if err != nil {
		writeErrCode(w, http.StatusInternalServerError, "like failed", i18n.MsgInternal)
		return
	}
	likes, _ := b.Store.ClosetLikes(r.Context(), tid)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "created": created, "likes": likes})
}

// DELETE /api/v1/skinlib/{tid}/like — remove from closet (unlike).
func (b *Backend) handleSkinlibUnlike(w http.ResponseWriter, r *http.Request) {
	uid, err := b.currentUser(r)
	if err != nil {
		writeErrCode(w, http.StatusUnauthorized, "请先登录", i18n.MsgUnauthorized)
		return
	}
	tid, perr := strconv.ParseInt(r.PathValue("tid"), 10, 64)
	if perr != nil || tid <= 0 {
		writeErrCode(w, http.StatusBadRequest, "bad tid", i18n.MsgTextureBadID)
		return
	}
	if err := b.Store.RemoveClosetEntryByTID(r.Context(), uid, tid); errors.Is(err, store.ErrNotFound) {
		writeErrCode(w, http.StatusNotFound, "未收藏", i18n.MsgLikeMissing)
		return
	} else if err != nil {
		writeErrCode(w, http.StatusInternalServerError, "unlike failed", i18n.MsgInternal)
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
		writeErrCode(w, http.StatusUnauthorized, "请先登录", i18n.MsgUnauthorized)
		return
	}
	tid, perr := strconv.ParseInt(r.PathValue("tid"), 10, 64)
	if perr != nil || tid <= 0 {
		writeErrCode(w, http.StatusBadRequest, "bad tid", i18n.MsgTextureBadID)
		return
	}
	var req struct {
		Name      *string `json:"name"`
		Detail    *string `json:"detail"`
		Source    *string `json:"source"`
		SourceURL *string `json:"sourceUrl"`
		Public    *bool   `json:"public"`
		Model     *string `json:"model"`
	}
	if err := jsonDecode(r, &req); err != nil {
		writeErrCode(w, http.StatusBadRequest, "invalid JSON", i18n.MsgBadBody)
		return
	}
	if req.Name != nil {
		*req.Name = strings.TrimSpace(*req.Name)
		if len([]rune(*req.Name)) > 50 {
			writeErrCode(w, http.StatusBadRequest, "名称过长", i18n.MsgTooLong)
			return
		}
	}
	if req.Detail != nil {
		*req.Detail = strings.TrimSpace(*req.Detail)
		if len([]rune(*req.Detail)) > 4000 {
			writeErrCode(w, http.StatusBadRequest, "描述最多 4000 个字", i18n.MsgTooLong)
			return
		}
	}
	if req.Source != nil {
		*req.Source = strings.ToLower(strings.TrimSpace(*req.Source))
		switch *req.Source {
		case "", "original":
			if req.SourceURL != nil {
				*req.SourceURL = ""
			}
		case "derivative", "repost":
			if req.SourceURL == nil || strings.TrimSpace(*req.SourceURL) == "" {
				writeErrCode(w, http.StatusBadRequest, "二创/转载必须填写原始发布地址", account.CodeSourceURLReq)
				return
			}
			if !validSourceURL(*req.SourceURL) {
				writeErrCode(w, http.StatusBadRequest, "原始发布地址必须是 http(s) 链接", account.CodeSourceURLBad)
				return
			}
		default:
			writeErrCode(w, http.StatusBadRequest, "材质来源不合法", i18n.MsgTextureSourceBad)
			return
		}
	}
	if req.SourceURL != nil {
		*req.SourceURL = strings.TrimSpace(*req.SourceURL)
		if len(*req.SourceURL) > 512 {
			writeErrCode(w, http.StatusBadRequest, "来源地址过长", i18n.MsgTooLong)
			return
		}
	}
	// Owner-scoped unless admin (admins may fix any row).
	owner := uid
	if perm, err := b.Store.UserPermission(r.Context(), uid); err == nil && perm > 0 {
		owner = 0
	}
	// Validate the resulting provenance even when only one of
	// source/sourceUrl is being edited.
	cur, curErr := b.Store.SkinlibOne(r.Context(), tid, uid, owner == 0)
	if curErr == nil {
		src := cur.Source
		srcURL := cur.SourceURL
		if req.Source != nil {
			src = *req.Source
		}
		if req.SourceURL != nil {
			srcURL = *req.SourceURL
		}
		if (src == "derivative" || src == "repost") && !validSourceURL(srcURL) {
			writeErrCode(w, http.StatusBadRequest, "二创/转载必须填写合法的原始发布地址", account.CodeSourceURLBad)
			return
		}
	}
	patch := store.TextureMetaPatch{
		Name: req.Name, DetailMD: req.Detail, Source: req.Source, SourceURL: req.SourceURL, Public: req.Public,
	}
	if req.Model != nil {
		m := strings.TrimSpace(*req.Model)
		if m != "default" && m != "slim" {
			writeErrCode(w, http.StatusBadRequest, "手臂模型只能是 default 或 slim", i18n.MsgTextureModelBad)
			return
		}
		patch.Model = &m
	}
	if err := b.Store.UpdateTextureMeta(r.Context(), tid, owner, patch); errors.Is(err, store.ErrNotFound) {
		writeErrCode(w, http.StatusNotFound, "纹理不存在", i18n.MsgTextureNotFound)
		return
	} else if errors.Is(err, store.ErrForbidden) {
		writeErrCode(w, http.StatusForbidden, "只有上传者可以修改", i18n.MsgTextureNotOwner)
		return
	} else if errors.Is(err, store.ErrModerationBan) {
		writeErrCode(w, http.StatusForbidden, "该材质已被判定违规，不能再次公开", i18n.MsgTextureModeration)
		return
	} else if err != nil {
		writeErrCode(w, http.StatusInternalServerError, "update failed", i18n.MsgInternal)
		return
	}
	// Visibility and arm model are moderation-relevant changes: without this
	// line the panel cannot tell a republished texture from one that never left
	// the library, nor who corrected a legacy skin.
	if req.Public != nil {
		event := "texture_make_private"
		if *req.Public {
			event = "texture_make_public"
		}
		b.Store.InsertAudit(r.Context(), uid, event, map[string]any{"tid": tid, "hash": cur.Hash})
	}
	if patch.Model != nil {
		b.Store.InsertAudit(r.Context(), uid, "texture_model_update", map[string]any{
			"tid": tid, "hash": cur.Hash, "model": *patch.Model,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func validSourceURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 512 {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil
}

// handleTextureDeleteSelf deletes the caller's own texture. ?force=1 unbinds the
// characters, closet entries and avatar that still point at it — the first
// attempt without it answers 409 with that list so the user sees what it costs.
func (b *Backend) handleTextureDeleteSelf(w http.ResponseWriter, r *http.Request) {
	uid, err := b.currentUser(r)
	if err != nil {
		writeErrCode(w, http.StatusUnauthorized, "请先登录", i18n.MsgUnauthorized)
		return
	}
	tid, err := strconv.ParseInt(r.PathValue("tid"), 10, 64)
	if err != nil || tid <= 0 {
		writeErrCode(w, http.StatusBadRequest, "材质编号不合法", i18n.MsgTextureBadID)
		return
	}
	force := r.URL.Query().Get("force") == "1"
	if aerr := b.Account.DeleteTexture(r.Context(), uid, tid, uid, force, "", account.CallMeta{IP: ygg.ClientIP(r)}); aerr != nil {
		writeAccountErr(w, aerr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

var reportReasons = map[string]bool{
	"copyright": true, "sexual": true, "violence": true,
	"hate": true, "spam": true, "other": true,
}

func (b *Backend) handleTextureReport(w http.ResponseWriter, r *http.Request) {
	uid, err := b.currentUser(r)
	if err != nil {
		writeErrCode(w, http.StatusUnauthorized, "请先登录", i18n.MsgUnauthorized)
		return
	}
	tid, perr := strconv.ParseInt(r.PathValue("tid"), 10, 64)
	if perr != nil || tid <= 0 {
		writeErrCode(w, http.StatusBadRequest, "bad tid", i18n.MsgTextureBadID)
		return
	}
	var req struct {
		Reason string `json:"reason"`
		Detail string `json:"detail"`
	}
	if err := jsonDecode(r, &req); err != nil {
		writeErrCode(w, http.StatusBadRequest, "invalid JSON", i18n.MsgBadBody)
		return
	}
	req.Reason = strings.ToLower(strings.TrimSpace(req.Reason))
	req.Detail = strings.TrimSpace(req.Detail)
	if !reportReasons[req.Reason] {
		writeErrCode(w, http.StatusBadRequest, "举报原因不合法", i18n.MsgInvalidValue)
		return
	}
	if len([]rune(req.Detail)) > 500 {
		writeErrCode(w, http.StatusBadRequest, "补充说明最多 500 个字", i18n.MsgTooLong)
		return
	}
	pub, err := b.Store.TextureIsPublic(r.Context(), tid)
	if err != nil || !pub {
		writeErrCode(w, http.StatusNotFound, "纹理不存在或未公开", i18n.MsgTextureNotVisible)
		return
	}
	owner, err := b.Store.TextureOwner(r.Context(), tid)
	if err != nil {
		writeErrCode(w, http.StatusNotFound, "纹理不存在", i18n.MsgTextureNotFound)
		return
	}
	if owner == uid {
		writeErrCode(w, http.StatusBadRequest, "不能举报自己的材质", i18n.MsgReportSelf)
		return
	}
	since := time.Now().Add(-24 * time.Hour)
	if n, err := b.Store.CountReportsByUserSince(r.Context(), uid, since); err == nil && n >= 10 {
		writeErrCode(w, http.StatusTooManyRequests, "今日举报次数过多，请稍后再试", i18n.MsgReportQuota)
		return
	}
	if n, err := b.Store.CountReportsByTextureSince(r.Context(), tid, since); err == nil && n >= 50 {
		writeErrCode(w, http.StatusTooManyRequests, "该材质今日举报次数过多", i18n.MsgReportQuota)
		return
	}
	if _, err := b.Store.CreateReport(r.Context(), tid, uid, req.Reason, req.Detail); errors.Is(err, store.ErrDuplicateReport) {
		writeErrCode(w, http.StatusConflict, "你已经举报过该材质", i18n.MsgReportDuplicate)
		return
	} else if err != nil {
		writeErrCode(w, http.StatusInternalServerError, "提交举报失败", i18n.MsgReportFailed)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true})
}
