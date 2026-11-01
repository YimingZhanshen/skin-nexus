package adminapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"skin-nexus/server/internal/account"
	"skin-nexus/server/internal/i18n"
	"skin-nexus/server/internal/store"
)

// Admin player management (B3, blessing 玩家管理 parity): list / rename /
// re-home / texture swap / delete. All writes audit-attributed; destructive
// actions are confirm-gated in the frontend.

// OnPlayerDeleted fires after a successful admin player deletion with the
// (uuid, name) pair captured BEFORE the transactional delete — main wires
// the best-effort union hub announcement (same semantics as OnUserDeleted).
type OnPlayerDeletedHook func(uuid, name string)

// uidExists is a minimal target-user check for the owner change.
func (b *Backend) uidExists(r *http.Request, uid int64) bool {
	_, err := b.Store.UserPermission(r.Context(), uid)
	return err == nil
}

// GET /api/admin/v1/players?q=&page=&uid= — paged player list (PID / name /
// owner / UUID / texture hashes for thumbnail previews / updated time).
func (b *Backend) handleAdminPlayers(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	uidFilter, _ := strconv.ParseInt(r.URL.Query().Get("uid"), 10, 64)
	rows, total, err := b.Store.AdminPlayers(r.Context(), r.URL.Query().Get("q"), uidFilter, page, 50)
	if err != nil {
		writeInternal(r, w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"players": rows, "total": total, "page": page})
}

// PUT /api/admin/v1/players/{pid}/name {name} — rename. UUID ZERO-DRIFT:
// the store method never touches uuid_map.uuid, only the name columns.
func (b *Backend) handleAdminPlayerRename(w http.ResponseWriter, r *http.Request) {
	pid, ok := idParam(r)
	if !ok {
		writeErrCode(w, http.StatusBadRequest, "bad id", i18n.MsgBadID)
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
	// Same configurable rule group as user-side creation (blessing parity;
	// legacy imported names are never re-validated).
	if err := b.Store.ValidatePlayerName(r.Context(), req.Name); err != nil {
		writeDomain(w, http.StatusBadRequest, err, i18n.MsgPlayerNameRuleBad)
		return
	}
	before, err := b.Store.PlayerByID(r.Context(), pid)
	if errors.Is(err, store.ErrNotFound) {
		writeErrCode(w, http.StatusNotFound, "角色不存在", account.CodeNotFound)
		return
	} else if err != nil {
		writeErrCode(w, http.StatusInternalServerError, "db error", i18n.MsgInternal)
		return
	}
	if err := b.Store.AdminRenamePlayer(r.Context(), pid, req.Name); errors.Is(err, store.ErrDuplicate) {
		writeErrCode(w, http.StatusConflict, "角色名已被占用", account.CodeNameTaken)
		return
	} else if err != nil {
		writeErrCode(w, http.StatusInternalServerError, "rename failed", i18n.MsgInternal)
		return
	}
	// UUID zero-drift is an iron rule — verify, don't trust (a regression
	// here silently breaks every migrated account's identity).
	after, err := b.Store.PlayerByID(r.Context(), pid)
	if err != nil || after.UUID != before.UUID {
		writeErrCode(w, http.StatusInternalServerError, "UUID 漂移防护触发：改名未通过零漂移校验", i18n.MsgPlayerUUIDDrift)
		return
	}
	b.Store.InsertAudit(r.Context(), actorUID(r), "admin_player_rename",
		map[string]any{"pid": pid, "from": before.Name, "to": req.Name, "uuid": after.UUID})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "uuid": after.UUID})
}

// PUT /api/admin/v1/players/{pid}/owner {uid} — re-home a character. The
// old owner's ygg tokens are revoked (their launcher selection pointed
// here); the new owner must exist.
func (b *Backend) handleAdminPlayerOwner(w http.ResponseWriter, r *http.Request) {
	pid, ok := idParam(r)
	if !ok {
		writeErrCode(w, http.StatusBadRequest, "bad id", i18n.MsgBadID)
		return
	}
	var req struct {
		UID int64 `json:"uid"`
	}
	if err := jsonDecode(r, &req); err != nil || req.UID <= 0 {
		writeErrCode(w, http.StatusBadRequest, "invalid JSON", i18n.MsgBadBody)
		return
	}
	if !b.uidExists(r, req.UID) {
		writeErrCode(w, http.StatusNotFound, "目标用户不存在", i18n.MsgUserNotFound)
		return
	}
	before, err := b.Store.PlayerByID(r.Context(), pid)
	if errors.Is(err, store.ErrNotFound) {
		writeErrCode(w, http.StatusNotFound, "角色不存在", account.CodeNotFound)
		return
	} else if err != nil {
		writeErrCode(w, http.StatusInternalServerError, "db error", i18n.MsgInternal)
		return
	}
	if before.UID == req.UID {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "unchanged": true})
		return
	}
	if err := b.Store.AdminSetPlayerOwner(r.Context(), pid, req.UID); err != nil {
		writeErrCode(w, http.StatusInternalServerError, "update failed", i18n.MsgInternal)
		return
	}
	// Old owner's launcher profile selection dies with the re-home.
	_ = b.Store.InvalidateUserTokens(r.Context(), before.UID)
	b.Store.InsertAudit(r.Context(), actorUID(r), "admin_player_owner_update",
		map[string]any{"pid": pid, "name": before.Name, "from": before.UID, "to": req.UID})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// PUT /api/admin/v1/players/{pid}/textures {type, textureId} — mount/unmount
// a texture (type ∈ skin|cape, textureId 0 = clear). Admin surface: any
// EXISTING texture id is assignable (admins curate the shared library —
// ownership is a user-path rule, see webapi.setPlayerTexture).
func (b *Backend) handleAdminPlayerTextures(w http.ResponseWriter, r *http.Request) {
	pid, ok := idParam(r)
	if !ok {
		writeErrCode(w, http.StatusBadRequest, "bad id", i18n.MsgBadID)
		return
	}
	var req struct {
		Type      string `json:"type"`
		TextureID int64  `json:"textureId"`
	}
	if err := jsonDecode(r, &req); err != nil {
		writeErrCode(w, http.StatusBadRequest, "invalid JSON", i18n.MsgBadBody)
		return
	}
	if req.Type != "skin" && req.Type != "cape" {
		writeErrCode(w, http.StatusBadRequest, "type 需为 skin 或 cape", i18n.MsgInvalidValue)
		return
	}
	if req.TextureID != 0 {
		if _, err := b.Store.TextureOwner(r.Context(), req.TextureID); errors.Is(err, store.ErrNotFound) {
			writeErrCode(w, http.StatusNotFound, "材质不存在", i18n.MsgTextureNotFound)
			return
		} else if err != nil {
			writeErrCode(w, http.StatusInternalServerError, "db error", i18n.MsgInternal)
			return
		}
	}
	if _, err := b.Store.PlayerByID(r.Context(), pid); errors.Is(err, store.ErrNotFound) {
		writeErrCode(w, http.StatusNotFound, "角色不存在", account.CodeNotFound)
		return
	} else if err != nil {
		writeErrCode(w, http.StatusInternalServerError, "db error", i18n.MsgInternal)
		return
	}
	skinTid, capeTid, err := b.Store.PlayerTextureIDs(r.Context(), pid)
	if err != nil {
		writeErrCode(w, http.StatusInternalServerError, "db error", i18n.MsgInternal)
		return
	}
	if req.Type == "skin" {
		skinTid = req.TextureID
	} else {
		capeTid = req.TextureID
	}
	if err := b.Store.SetPlayerTextures(r.Context(), pid, skinTid, capeTid); err != nil {
		writeErrCode(w, http.StatusInternalServerError, "update failed", i18n.MsgInternal)
		return
	}
	b.Store.InsertAudit(r.Context(), actorUID(r), "admin_player_textures_update",
		map[string]any{"pid": pid, "type": req.Type, "textureId": req.TextureID})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// DELETE /api/admin/v1/players/{pid} — remove a character (uuid_map row +
// token selections cleaned in one transaction; textures kept — shared
// library semantics). Frontend double-confirms; union hub announcement is
// best-effort via the main-wired OnPlayerDeleted hook.
func (b *Backend) handleAdminPlayerDelete(w http.ResponseWriter, r *http.Request) {
	pid, ok := idParam(r)
	if !ok {
		writeErrCode(w, http.StatusBadRequest, "bad id", i18n.MsgBadID)
		return
	}
	uuid, name, err := b.Store.AdminDeletePlayer(r.Context(), pid)
	if errors.Is(err, store.ErrNotFound) {
		writeErrCode(w, http.StatusNotFound, "角色不存在", account.CodeNotFound)
		return
	} else if err != nil {
		writeErrCode(w, http.StatusInternalServerError, "delete failed", i18n.MsgInternal)
		return
	}
	b.Store.InsertAudit(r.Context(), actorUID(r), "admin_player_delete",
		map[string]any{"pid": pid, "name": name, "uuid": uuid})
	if b.OnPlayerDeleted != nil && uuid != "" {
		b.OnPlayerDeleted(uuid, name)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
