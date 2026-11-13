package webapi

import (
	"net/http"
	"strconv"

	"skin-nexus/server/internal/i18n"
	"skin-nexus/server/internal/store"
)

// handleMyReportsList is the player's own report tracking (the /user/reports
// page): the same envelope as the notifications list, scoped server-side to
// the caller's uid by ListMyReports — the handler never takes a uid from the
// query, so there is nothing to tamper with. Moderation internals (who
// handled it, the texture's verdict) stay out of the payload: the player
// gets the outcome (status + handled time), not the operator's notes.
func (b *Backend) handleMyReportsList(w http.ResponseWriter, r *http.Request) {
	uid, err := b.currentUser(r)
	if err != nil {
		writeErrCode(w, http.StatusUnauthorized, "not logged in", i18n.MsgUnauthorized)
		return
	}
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	perPage, _ := strconv.Atoi(q.Get("perPage"))
	rows, total, err := b.Store.ListMyReports(r.Context(), uid, store.ReportOpts{Page: page, PerPage: perPage})
	if err != nil {
		writeInternal(r, w, err)
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		item := map[string]any{
			"id": row.ID, "textureId": row.TextureID,
			"textureName": row.TextureName, "textureType": row.TextureType,
			"reason": row.Reason, "status": row.Status,
			"createdAt": row.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		}
		if row.Detail != "" {
			item["detail"] = row.Detail
		}
		if row.TextureType == "skin" {
			// Same renderer thumb as the closet rows: one source, versioned
			// by the server, so a rasterizer bump refreshes it everywhere.
			item["previewURL"] = previewThumbURL(row.TextureID)
		}
		if row.HandledAt.Valid {
			item["handledAt"] = row.HandledAt.Time.UTC().Format("2006-01-02T15:04:05Z")
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"reports": out, "total": total, "page": max(1, page),
	})
}
