package adminapi

import (
	"net/http"
	"strconv"
	"strings"

	"skin-nexus/server/internal/store"
)

type reportView struct {
	ID          int64  `json:"id"`
	TextureID   int64  `json:"textureId"`
	TextureName string `json:"textureName"`
	TextureType string `json:"textureType"`
	Uploader    string `json:"uploader"`
	Reporter    string `json:"reporter"`
	Reason      string `json:"reason"`
	Detail      string `json:"detail,omitempty"`
	Status      string `json:"status"`
	Public      bool   `json:"public"`
	CreatedAt   string `json:"createdAt"`
	HandledAt   string `json:"handledAt,omitempty"`
}

func reportToView(r store.ReportRow) reportView {
	v := reportView{
		ID: r.ID, TextureID: r.TextureID, TextureName: r.TextureName,
		TextureType: r.TextureType, Uploader: r.Uploader, Reporter: r.Reporter,
		Reason: r.Reason, Detail: r.Detail, Status: r.Status, Public: r.Public,
		CreatedAt: r.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if r.HandledAt.Valid {
		v.HandledAt = r.HandledAt.Time.UTC().Format("2006-01-02T15:04:05Z")
	}
	return v
}

func (b *Backend) handleReportsList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	perPage, _ := strconv.Atoi(q.Get("perPage"))
	rows, total, err := b.Store.ListReports(r.Context(), store.ReportOpts{
		Status: strings.TrimSpace(q.Get("status")), Page: page, PerPage: perPage,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "reports query failed"})
		return
	}
	out := make([]reportView, 0, len(rows))
	for _, row := range rows {
		out = append(out, reportToView(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"reports": out, "total": total, "page": max(1, page)})
}

func (b *Backend) handleReportResolve(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad report id"})
		return
	}
	var body struct {
		Action string `json:"action"`
	}
	if err := jsonDecode(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	row, err := b.Store.GetReport(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "report not found"})
		return
	}
	handledBy := actorUID(r)
	switch body.Action {
	case "resolve", "dismiss":
		status := "resolved"
		if body.Action == "dismiss" {
			status = "dismissed"
		}
		if err := b.Store.ResolveReport(r.Context(), id, status, handledBy); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "report already handled"})
			return
		}
		event := "texture_report_resolve"
		if status == "dismissed" {
			event = "texture_report_dismiss"
		}
		b.Store.InsertAudit(r.Context(), handledBy, event, map[string]any{
			"report": row.ID, "texture": row.TextureID,
		})
	case "hide":
		hide := false
		if err := b.Store.UpdateTextureMeta(r.Context(), row.TextureID, 0,
			store.TextureMetaPatch{Public: &hide}); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "hide texture failed"})
			return
		}
		if err := b.Store.ResolveReport(r.Context(), id, "resolved", handledBy); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "report already handled"})
			return
		}
		b.Store.InsertAudit(r.Context(), handledBy, "texture_report_hide", map[string]any{
			"report": row.ID, "texture": row.TextureID,
		})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "action must be resolve|dismiss|hide"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
