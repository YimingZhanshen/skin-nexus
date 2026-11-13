package adminapi

import (
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"skin-nexus/server/internal/auth"
	"skin-nexus/server/internal/i18n"
	"skin-nexus/server/internal/plugin"
	"skin-nexus/server/internal/store"
)

// Admin user management (P1): reset password / delete account. B2 adds the
// blessing-parity inline edits (email / nickname / verification / permission).

// Local copies of webapi's validation patterns (same rules, admin path must
// reject what registration rejects — see webapi.nickRe / webapi.mailRe).
var (
	adminNickRe = regexp.MustCompile(`^[A-Za-z0-9_一-龥]{2,24}$`)
	adminMailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
)

// idParam parses the {id} path value.
func idParam(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

// writeStoreErr maps store sentinel errors to the HTTP envelope. Only the
// caller knows which column collided, so the duplicate code and prose are
// passed in; not-found is always the user this route is addressed to.
func writeStoreErr(w http.ResponseWriter, err error, dupCode, dupMsg string) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErrCode(w, http.StatusNotFound, "user not found", i18n.MsgUserNotFound)
	case errors.Is(err, store.ErrDuplicate):
		writeErrCode(w, http.StatusConflict, dupMsg, dupCode)
	default:
		writeErrCode(w, http.StatusInternalServerError, "update failed", i18n.MsgInternal)
	}
}

// guardReason is why an inline user edit was refused. The guard answers with a
// reason, not a sentence, because the sentence belongs in the envelope: the
// panel translates the code and the message is only the fallback.
type guardReason int

const (
	guardOK guardReason = iota
	guardSelfEdit
	guardRank
	guardNoUser
	guardCheckFailed
)

// inlineGuard enforces the blessing user-management guards (archive §6.2)
// on an inline edit:
//  1. no self-edit (禁改自己)
//  2. the target's permission must be STRICTLY below the actor's
//     (只能操作严格小于自己的)
//
// The token/API path (actorUID == 0) is the operator's machine fallback and
// bypasses these identity guards — except the permission endpoint below,
// which refuses it outright.
func (b *Backend) inlineGuard(r *http.Request, target int64) guardReason {
	actor := actorUID(r)
	if actor == 0 {
		return guardOK // machine fallback: operator authority, no identity guards
	}
	if actor == target {
		return guardSelfEdit
	}
	actorPerm, err := b.Store.UserPermission(r.Context(), actor)
	if err != nil {
		return guardCheckFailed
	}
	targetPerm, err := b.Store.UserPermission(r.Context(), target)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return guardNoUser
		}
		return guardCheckFailed
	}
	if targetPerm >= actorPerm {
		return guardRank
	}
	return guardOK
}

// writeGuard answers a refused inline edit. Statuses are unchanged from the
// string-returning guard; only the code is new.
func writeGuard(w http.ResponseWriter, reason guardReason) {
	switch reason {
	case guardSelfEdit:
		writeErrCode(w, http.StatusForbidden, "you cannot edit your own admin entry", i18n.MsgAdminSelfEdit)
	case guardRank:
		writeErrCode(w, http.StatusForbidden, "you may only manage strictly lower-ranked accounts", i18n.MsgAdminRank)
	case guardNoUser:
		writeErrCode(w, http.StatusForbidden, "user not found", i18n.MsgUserNotFound)
	case guardCheckFailed:
		writeErrCode(w, http.StatusForbidden, "permission check failed", i18n.MsgForbidden)
	}
}

// POST /api/admin/v1/users/{id}/password {password} — force-set a user's
// password. Side effects mirror the self-service change: ygg tokens die
// immediately (InvalidateUserTokens) and OIDC grants are revoked via the
// main-wired OnUserCredentialChange hook.
func (b *Backend) handleUserSetPassword(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErrCode(w, http.StatusBadRequest, "bad id", i18n.MsgBadID)
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := jsonDecode(r, &req); err != nil {
		writeErrCode(w, http.StatusBadRequest, "invalid JSON", i18n.MsgBadBody)
		return
	}
	// Same blessing guards as the inline edits: without this, any admin could
	// reset a SUPER_ADMIN's password and take over the account.
	if reason := b.inlineGuard(r, id); reason != guardOK {
		writeGuard(w, reason)
		return
	}
	req.Password = strings.TrimSpace(req.Password)
	if len(req.Password) < 8 {
		writeErrCode(w, http.StatusBadRequest, "password must be at least 8 characters", i18n.MsgPasswordTooShort)
		return
	}
	hash, err := auth.HashArgon2id(req.Password)
	if err != nil {
		writeErrCode(w, http.StatusInternalServerError, "hash error", i18n.MsgInternal)
		return
	}
	if err := b.Store.AdminSetPassword(r.Context(), id, hash); errors.Is(err, store.ErrNotFound) {
		writeErrCode(w, http.StatusNotFound, "user not found", i18n.MsgUserNotFound)
		return
	} else if err != nil {
		writeErrCode(w, http.StatusInternalServerError, "update failed", i18n.MsgInternal)
		return
	}
	_ = b.Store.InvalidateUserTokens(r.Context(), id)
	if b.OnUserCredentialChange != nil {
		b.OnUserCredentialChange(id)
	}
	b.Store.InsertAudit(r.Context(), actorUID(r), "admin_user_password_reset", map[string]any{"target": id})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// DELETE /api/admin/v1/users/{id} — remove a user and their working
// credentials (transaction in store.DeleteUser; textures/audit kept).
// Protection is permission-based first: SUPER_ADMIN (permission >= 2)
// accounts are never deletable — migrated databases do NOT have the operator
// at uid 1 (here uid 1 is a legacy regular user), so the old uid==1-only
// guard left the real super admin deletable by any admin. uid 1 stays
// undeletable too (blessing parity, belt and suspenders). inlineGuard applies:
// no self-delete, only strictly-lower-permission targets.
func (b *Backend) handleUserDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r)
	if !ok {
		writeErrCode(w, http.StatusBadRequest, "bad id", i18n.MsgBadID)
		return
	}
	if id == 1 {
		writeErrCode(w, http.StatusBadRequest, "uid 1 (the bootstrap admin) cannot be deleted", i18n.MsgUserAdminProtected)
		return
	}
	if targetPerm, err := b.Store.UserPermission(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErrCode(w, http.StatusNotFound, "user not found", i18n.MsgUserNotFound)
			return
		}
		writeErrCode(w, http.StatusInternalServerError, "permission check failed", i18n.MsgInternal)
		return
	} else if targetPerm >= 2 {
		writeErrCode(w, http.StatusBadRequest, "super admin accounts cannot be deleted", i18n.MsgUserAdminProtected)
		return
	}
	if reason := b.inlineGuard(r, id); reason != guardOK {
		writeGuard(w, reason)
		return
	}
	// Union profile lifecycle: the (uuid, name) pairs must be captured BEFORE
	// DeleteUser — the transaction removes the players and their uuid_map
	// rows. Best-effort: a lookup failure just skips the hub announcement
	// (DeleteUser itself must not be blocked by union bookkeeping).
	var players [][2]string
	if b.OnUserDeleted != nil {
		if ps, err := b.Store.PlayersByUID(r.Context(), id); err == nil {
			for _, p := range ps {
				if p.UUID != "" { // no uuid_map row => no union identity to retire
					players = append(players, [2]string{p.UUID, p.Name})
				}
			}
		}
	}
	if err := b.Store.DeleteUser(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeErrCode(w, http.StatusNotFound, "user not found", i18n.MsgUserNotFound)
		return
	} else if err != nil {
		writeErrCode(w, http.StatusInternalServerError, "delete failed", i18n.MsgInternal)
		return
	}
	b.Store.InsertAudit(r.Context(), actorUID(r), "admin_user_delete", map[string]any{"target": id})
	if b.OnUserDeleted != nil {
		b.OnUserDeleted(id, players) // async inside the hook; never blocks this response
	}
	if b.OnUserLifecycle != nil {
		b.OnUserLifecycle(r.Context(), plugin.UserLifecycleEvent{Kind: "deleted", UserID: id})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- inline edits (B2, blessing 管理面板 用户管理 parity) ----

// PUT /api/admin/v1/users/{id}/email {email} — set or clear ("") the email.
// Same-value writes are a no-op success (changed-rows semantics would make
// a failed save indistinguishable from an unchanged one in the UI).
func (b *Backend) handleUserSetEmail(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r)
	if !ok {
		writeErrCode(w, http.StatusBadRequest, "bad id", i18n.MsgBadID)
		return
	}
	var req struct {
		Email string `json:"email"`
	}
	if err := jsonDecode(r, &req); err != nil {
		writeErrCode(w, http.StatusBadRequest, "invalid JSON", i18n.MsgBadBody)
		return
	}
	req.Email = strings.TrimSpace(req.Email)
	if req.Email != "" && !adminMailRe.MatchString(req.Email) {
		writeErrCode(w, http.StatusBadRequest, "email address is not valid", i18n.MsgEmailInvalid)
		return
	}
	if reason := b.inlineGuard(r, id); reason != guardOK {
		writeGuard(w, reason)
		return
	}
	if err := b.Store.UpdateUserEmail(r.Context(), id, req.Email); err != nil {
		writeStoreErr(w, err, i18n.MsgEmailTaken, "that email is already used by another account")
		return
	}
	b.Store.InsertAudit(r.Context(), actorUID(r), "admin_user_email_update", map[string]any{"target": id, "email": req.Email})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// PUT /api/admin/v1/users/{id}/nickname {nickname} — set the nickname
// (nickRe + uniqueness; the column is NOT NULL so empty is rejected).
func (b *Backend) handleUserSetNickname(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r)
	if !ok {
		writeErrCode(w, http.StatusBadRequest, "bad id", i18n.MsgBadID)
		return
	}
	var req struct {
		Nickname string `json:"nickname"`
	}
	if err := jsonDecode(r, &req); err != nil {
		writeErrCode(w, http.StatusBadRequest, "invalid JSON", i18n.MsgBadBody)
		return
	}
	req.Nickname = strings.TrimSpace(req.Nickname)
	if !adminNickRe.MatchString(req.Nickname) {
		writeErrCode(w, http.StatusBadRequest, "nickname must be 2-24 letters, digits, underscores or Chinese characters", i18n.MsgNicknameInvalid)
		return
	}
	if reason := b.inlineGuard(r, id); reason != guardOK {
		writeGuard(w, reason)
		return
	}
	if err := b.Store.UpdateUserNickname(r.Context(), id, req.Nickname); err != nil {
		writeStoreErr(w, err, i18n.MsgNicknameTaken, "that nickname is already used by another account")
		return
	}
	b.Store.InsertAudit(r.Context(), actorUID(r), "admin_user_nickname_update", map[string]any{"target": id, "nickname": req.Nickname})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// PUT /api/admin/v1/users/{id}/verification {verified} — flip the
// email_verified flag (blessing 用户管理「验证」列).
func (b *Backend) handleUserSetVerification(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r)
	if !ok {
		writeErrCode(w, http.StatusBadRequest, "bad id", i18n.MsgBadID)
		return
	}
	var req struct {
		Verified bool `json:"verified"`
	}
	if err := jsonDecode(r, &req); err != nil {
		writeErrCode(w, http.StatusBadRequest, "invalid JSON", i18n.MsgBadBody)
		return
	}
	if reason := b.inlineGuard(r, id); reason != guardOK {
		writeGuard(w, reason)
		return
	}
	if err := b.Store.SetEmailVerified(r.Context(), id, req.Verified); err != nil {
		writeStoreErr(w, err, i18n.MsgEmailTaken, "that email is already used by another account")
		return
	}
	b.Store.InsertAudit(r.Context(), actorUID(r), "admin_user_verification_update", map[string]any{"target": id, "verified": req.Verified})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// PUT /api/admin/v1/users/{id}/permission {permission} — change the legacy
// blessing permission. Three guards (archive §6.2, blessing source-verified):
//   - only strictly-lower-permission targets (inlineGuard, incl. no self)
//   - granting ADMIN (1) requires the actor to be SUPER_ADMIN (2)
//   - value must be one of -1 (banned) / 0 (normal) / 1 (admin) — the
//     blessing UI never grants 2 (setup-created only), neither do we
//
// The X-Admin-Token machine path (actorUID == 0) is refused outright: a
// shared fallback credential must not be able to reshape the permission
// surface. Session-authenticated admins only.
func (b *Backend) handleUserSetPermission(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r)
	if !ok {
		writeErrCode(w, http.StatusBadRequest, "bad id", i18n.MsgBadID)
		return
	}
	var req struct {
		Permission *int `json:"permission"`
	}
	if err := jsonDecode(r, &req); err != nil || req.Permission == nil {
		writeErrCode(w, http.StatusBadRequest, "invalid JSON", i18n.MsgBadBody)
		return
	}
	perm := *req.Permission
	if perm < -1 || perm > 1 {
		writeErrCode(w, http.StatusBadRequest, "permission must be -1 (banned), 0 (normal) or 1 (admin)", i18n.MsgInvalidValue)
		return
	}
	actor := actorUID(r)
	if actor == 0 {
		writeErrCode(w, http.StatusForbidden, "permission changes need an admin web session, not X-Admin-Token", i18n.MsgWebSessionRequired)
		return
	}
	if reason := b.inlineGuard(r, id); reason != guardOK {
		writeGuard(w, reason)
		return
	}
	// Guard 2: granting ADMIN requires SUPER_ADMIN (permission = 2).
	if perm == 1 {
		actorPerm, err := b.Store.UserPermission(r.Context(), actor)
		if err != nil || actorPerm != 2 {
			writeErrCode(w, http.StatusForbidden, "granting admin needs a super admin account (permission=2)", i18n.MsgSuperAdminRequired)
			return
		}
	}
	// The notice fires on the transition, not on every write: re-saving the
	// same permission must not add another row to the inbox.
	prevPerm, prevErr := b.Store.UserPermission(r.Context(), id)
	if err := b.Store.SetUserPermission(r.Context(), id, perm); err != nil {
		writeStoreErr(w, err, i18n.MsgEmailTaken, "that email is already used by another account")
		return
	}
	if prevErr == nil {
		if perm == -1 && prevPerm != -1 {
			b.notifyUser(r.Context(), id, store.NotifAccountBanned)
			// A ban that leaves the web session alive only stops the launcher:
			// the suspended account keeps publishing from the tab it already has
			// open. Purge now — the login handler refuses the next one.
			if err := b.Store.DeleteUserSessionsExcept(r.Context(), id, ""); err != nil {
				slog.Info("admin ban: purge web sessions", "uid", id, "error", err)
			}
		} else if perm != -1 && prevPerm == -1 {
			b.notifyUser(r.Context(), id, store.NotifAccountUnbanned)
		}
	}
	// A demoted/banned admin's session must die now — the session gate
	// re-checks permission per request, but their tokens/grants linger.
	if perm < 1 {
		_ = b.Store.InvalidateUserTokens(r.Context(), id)
	}
	b.Store.InsertAudit(r.Context(), actorUID(r), "admin_user_permission_update", map[string]any{"target": id, "permission": perm})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
