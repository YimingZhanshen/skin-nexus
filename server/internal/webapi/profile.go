package webapi

// User-center self-service endpoints (A 级批次, dev-archive-feature-gaps §1):
// profile edit, account deletion, avatar, own-player rename/delete. Admin
// management (B3) stays in adminapi — these are the user's own operations
// with ownership checks, blessing UserController parity.

import (
	"net/http"
	"os"
	"strconv"
	"strings"

	"skin-nexus/server/internal/account"
	"skin-nexus/server/internal/i18n"
	"skin-nexus/server/internal/plugin"
	"skin-nexus/server/internal/store"
	"skin-nexus/server/internal/ygg"
)

// statFile returns a file's size in bytes (0 on any error — the storage
// card degrades to a smaller number rather than failing the whole /me).
func statFile(p string) (int64, error) {
	fi, err := os.Stat(p)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

// handleProfileUpdate: PUT /api/v1/me/profile {nickname?, email?}
//   - nickname: nickRe + unique; email: mailRe + unique (dup → 409)
//   - email change resets email_verified (blessing semantics: a new address
//     must re-verify before a verification gate lets them join)
func (b *Backend) handleProfileUpdate(w http.ResponseWriter, r *http.Request) {
	uid, err := b.currentUser(r)
	if err != nil {
		writeErrCode(w, http.StatusUnauthorized, "请先登录", i18n.MsgUnauthorized)
		return
	}
	var req struct {
		Nickname string `json:"nickname"`
		Email    string `json:"email"`
	}
	if err := jsonDecode(r, &req); err != nil {
		writeErrCode(w, http.StatusBadRequest, "invalid JSON", i18n.MsgBadBody)
		return
	}
	changed := false
	if req.Nickname != "" {
		req.Nickname = strings.TrimSpace(req.Nickname)
		if !nickRe.MatchString(req.Nickname) {
			writeErrCode(w, http.StatusBadRequest, "昵称需为 2-24 位字母/数字/下划线/中文", i18n.MsgNicknameInvalid)
			return
		}
		changed = true
	}
	if req.Email != "" {
		req.Email = strings.TrimSpace(req.Email)
		if !mailRe.MatchString(req.Email) {
			writeErrCode(w, http.StatusBadRequest, "邮箱格式不正确", i18n.MsgEmailInvalid)
			return
		}
		changed = true
	}
	if !changed {
		writeErrCode(w, http.StatusBadRequest, "没有可更新的字段", i18n.MsgNoFields)
		return
	}
	if err := b.checkGuard(r.Context(), "profile.update.check", plugin.EventPayload{
		UserID: uid, IP: ygg.ClientIP(r),
		Data: map[string]any{"nickname": req.Nickname, "email": req.Email},
	}); err != nil {
		writeErrCode(w, http.StatusForbidden, err.Error(), i18n.MsgGuardDenied)
		return
	}
	if req.Nickname != "" {
		if err := b.Store.UpdateUserNickname(r.Context(), uid, req.Nickname); err != nil {
			if err == store.ErrDuplicate {
				writeErrCode(w, http.StatusConflict, "昵称已被占用", i18n.MsgNicknameTaken)
				return
			}
			writeErrCode(w, http.StatusInternalServerError, "update failed", i18n.MsgInternal)
			return
		}
	}
	if req.Email != "" {
		if err := b.Store.UpdateUserEmail(r.Context(), uid, req.Email); err != nil {
			if err == store.ErrDuplicate {
				writeErrCode(w, http.StatusConflict, "邮箱已被占用", i18n.MsgEmailTaken)
				return
			}
			writeErrCode(w, http.StatusInternalServerError, "update failed", i18n.MsgInternal)
			return
		}
		// New address = unverified (blessing parity).
		if err := b.Store.SetEmailVerified(r.Context(), uid, false); err != nil {
			b.Store.InsertAudit(r.Context(), uid, "profile_email_unverify_failed", map[string]any{"error": err.Error()})
		}
	}
	b.Store.InsertAudit(r.Context(), uid, "profile_update", map[string]any{
		"nickname": req.Nickname, "email": req.Email,
	})
	b.emitEvent(r.Context(), "profile.updated", plugin.EventPayload{
		UserID: uid, PlayerName: req.Nickname, IP: ygg.ClientIP(r),
		Data: map[string]any{"email": req.Email},
	})
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

// handleAccountDelete: DELETE /api/v1/me {password}
// Blessing parity: admins cannot delete themselves from the UI (the admin
// panel has its own guarded path); password re-proof guards against a
// borrowed session. Cascade = store.DeleteUser (sessions/tokens/closet/
// uuid_map/players go; textures and audit stay — same contract the admin
// delete advertises).
func (b *Backend) handleAccountDelete(w http.ResponseWriter, r *http.Request) {
	uid, err := b.currentUser(r)
	if err != nil {
		writeErrCode(w, http.StatusUnauthorized, "请先登录", i18n.MsgUnauthorized)
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := jsonDecode(r, &req); err != nil {
		writeErrCode(w, http.StatusBadRequest, "invalid JSON", i18n.MsgBadBody)
		return
	}
	perm, err := b.Store.UserPermission(r.Context(), uid)
	if err != nil {
		writeErrCode(w, http.StatusInternalServerError, "db error", i18n.MsgInternal)
		return
	}
	if perm > 0 {
		writeErrCode(w, http.StatusForbidden, "管理员账号不可自助删除", i18n.MsgSelfDeleteAdmin)
		return
	}
	ok, err := b.Store.VerifyPassword(r.Context(), uid, req.Password)
	if err != nil || !ok {
		writeErrCode(w, http.StatusUnauthorized, "密码不正确", i18n.MsgCredentials)
		return
	}
	if err := b.Store.DeleteUser(r.Context(), uid); err != nil {
		writeErrCode(w, http.StatusInternalServerError, "delete failed", i18n.MsgInternal)
		return
	}
	if b.OnUserLifecycle != nil {
		b.OnUserLifecycle(r.Context(), plugin.UserLifecycleEvent{Kind: "deleted", UserID: uid})
	}
	b.emitEvent(r.Context(), "user.deleted", plugin.EventPayload{UserID: uid, IP: ygg.ClientIP(r)})
	// Audit carries the (now-gone) uid as actor — audit rows are kept by
	// contract and actor_uid has no FK to users.
	b.Store.InsertAudit(r.Context(), uid, "account_self_delete", map[string]any{"uid": uid})
	// Caller's own session row is already gone with the user; expire the
	// cookie so the SPA drops the state.
	secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: secure,
	})
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

// handleAvatarSet: PUT /api/v1/me/avatar {textureId} — the texture must be
// the caller's own (IDOR guard mirroring setPlayerTexture). handleAvatarReset:
// DELETE /api/v1/me/avatar — back to the default skin.
func (b *Backend) handleAvatarSet(w http.ResponseWriter, r *http.Request) {
	uid, err := b.currentUser(r)
	if err != nil {
		writeErrCode(w, http.StatusUnauthorized, "请先登录", i18n.MsgUnauthorized)
		return
	}
	var req struct {
		TextureID int64 `json:"textureId"`
	}
	if err := jsonDecode(r, &req); err != nil || req.TextureID <= 0 {
		writeErrCode(w, http.StatusBadRequest, "textureId 无效", i18n.MsgTextureBadID)
		return
	}
	owner, err := b.Store.TextureOwner(r.Context(), req.TextureID)
	if err != nil || owner != uid {
		writeErrCode(w, http.StatusForbidden, "not yours", i18n.MsgTextureNotOwner)
		return
	}
	// Type gate (legacy TextureController.php:141 parity: 422 unless the
	// texture is a skin) — a cape must never become an avatar.
	if typ, err := b.Store.TextureType(r.Context(), req.TextureID); err != nil || typ != "skin" {
		writeErrCode(w, http.StatusUnprocessableEntity, "只有皮肤可以设为头像", i18n.MsgTextureAvatarSkin)
		return
	}
	if err := b.Store.SetUserAvatar(r.Context(), uid, req.TextureID); err != nil {
		writeErrCode(w, http.StatusInternalServerError, "update failed", i18n.MsgInternal)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (b *Backend) handleAvatarReset(w http.ResponseWriter, r *http.Request) {
	uid, err := b.currentUser(r)
	if err != nil {
		writeErrCode(w, http.StatusUnauthorized, "请先登录", i18n.MsgUnauthorized)
		return
	}
	if err := b.Store.SetUserAvatar(r.Context(), uid, 0); err != nil {
		writeErrCode(w, http.StatusInternalServerError, "update failed", i18n.MsgInternal)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

// ---- own-player management (user center 角色管理) ----

// ownPlayer verifies pid belongs to the caller (ownership gate shared by
// rename/delete; admin paths in adminapi skip it by role).
func (b *Backend) ownPlayer(w http.ResponseWriter, r *http.Request, uid int64) (int64, bool) {
	pid, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if pid <= 0 {
		writeErrCode(w, http.StatusBadRequest, "invalid pid", i18n.MsgPlayerBadID)
		return 0, false
	}
	players, err := b.Store.PlayersByUID(r.Context(), uid)
	if err != nil {
		writeErrCode(w, http.StatusInternalServerError, "db error", i18n.MsgInternal)
		return 0, false
	}
	for _, p := range players {
		if p.ID == pid {
			return pid, true
		}
	}
	writeErrCode(w, http.StatusNotFound, "角色不存在或不属于你", account.CodeNotFound)
	return 0, false
}

// handlePlayerRename: PUT /api/v1/me/players/{id}/name {name}
// ValidatePlayerName (rule group, only new writes) + uniqueness. UUID stays
// byte-identical — AdminRenamePlayer never touches uuid_map.uuid (zero-drift
// iron rule; integration test asserts it).
func (b *Backend) handlePlayerRename(w http.ResponseWriter, r *http.Request) {
	uid, err := b.currentUser(r)
	if err != nil {
		writeErrCode(w, http.StatusUnauthorized, "请先登录", i18n.MsgUnauthorized)
		return
	}
	pid, ok := b.ownPlayer(w, r, uid)
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := jsonDecode(r, &req); err != nil {
		writeErrCode(w, http.StatusBadRequest, "invalid JSON", i18n.MsgBadBody)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if err := b.Store.ValidatePlayerName(r.Context(), req.Name); err != nil {
		writeErrCode(w, http.StatusBadRequest, err.Error(), i18n.MsgPlayerNameRuleBad)
		return
	}
	_, invalidated, aerr := b.Account.RenamePlayer(r.Context(), uid, pid, req.Name, account.CallMeta{IP: ygg.ClientIP(r)})
	if aerr != nil {
		writeAccountErr(w, aerr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "tokensInvalidated": invalidated})
}

// handlePlayerDeleteSelf: DELETE /api/v1/me/players/{id}
// Double-confirm lives client-side; the uuid_map row goes with the player
// (AdminDeletePlayer cascade). No hub announce here — union syncs on its own
// schedule (B3 admin path announces; user-side delete is local-first).
func (b *Backend) handlePlayerDeleteSelf(w http.ResponseWriter, r *http.Request) {
	uid, err := b.currentUser(r)
	if err != nil {
		writeErrCode(w, http.StatusUnauthorized, "请先登录", i18n.MsgUnauthorized)
		return
	}
	pid, ok := b.ownPlayer(w, r, uid)
	if !ok {
		return
	}
	if aerr := b.Account.DeletePlayer(r.Context(), uid, pid, account.CallMeta{IP: ygg.ClientIP(r)}); aerr != nil {
		writeAccountErr(w, aerr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}
