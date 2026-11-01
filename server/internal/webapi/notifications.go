package webapi

// Inbox read side (2026-11-02). The rows are written by the admin surface
// (report outcome, texture moderation, ban state); this is how a player sees
// them. Delivery is pull-only on purpose — no polling badge, no push channel:
// the count rides along on /me and the list is fetched when the page opens.

import (
	"context"
	"net/http"
	"strconv"

	"skin-nexus/server/internal/i18n"
	"skin-nexus/server/internal/store"
)

func (b *Backend) handleNotificationsList(w http.ResponseWriter, r *http.Request) {
	uid, err := b.currentUser(r)
	if err != nil {
		writeErrCode(w, http.StatusUnauthorized, "not logged in", i18n.MsgUnauthorized)
		return
	}
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	perPage, _ := strconv.Atoi(q.Get("perPage"))
	rows, total, err := b.Store.ListNotifications(r.Context(), uid, q.Get("unread") == "1", page, perPage)
	if err != nil {
		writeInternal(r, w, err)
		return
	}
	if rows == nil {
		rows = []store.NotifRow{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"notifications": rows, "total": total, "page": max(1, page),
	})
}

// handleNotificationMarkRead accepts either an id (one row) or "all". The store
// scopes every write to the caller's uid, so a guessed id belonging to another
// player simply matches nothing.
func (b *Backend) handleNotificationMarkRead(w http.ResponseWriter, r *http.Request) {
	uid, err := b.currentUser(r)
	if err != nil {
		writeErrCode(w, http.StatusUnauthorized, "not logged in", i18n.MsgUnauthorized)
		return
	}
	var req struct {
		ID  int64 `json:"id"`
		All bool  `json:"all"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErrCode(w, http.StatusBadRequest, "invalid JSON", i18n.MsgBadBody)
		return
	}
	switch {
	case req.All:
		if err := b.Store.MarkAllNotificationsRead(r.Context(), uid); err != nil {
			writeInternal(r, w, err)
			return
		}
	case req.ID > 0:
		if err := b.Store.MarkNotificationRead(r.Context(), uid, req.ID); err != nil {
			writeInternal(r, w, err)
			return
		}
	default:
		writeErrCode(w, http.StatusBadRequest, "nothing to mark", i18n.MsgNoFields)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// unreadNotifications is the badge count carried on /me so the shell does not
// need a second round trip per boot.
func (b *Backend) unreadNotifications(ctx context.Context, uid int64) int {
	n, err := b.Store.UnreadNotificationCount(ctx, uid)
	if err != nil {
		return 0
	}
	return n
}
