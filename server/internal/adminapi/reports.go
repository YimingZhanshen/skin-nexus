package adminapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"skin-nexus/server/internal/account"
	"skin-nexus/server/internal/i18n"
	"skin-nexus/server/internal/store"
	"skin-nexus/server/internal/ygg"
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
	Moderation  string `json:"moderation,omitempty"`
	CreatedAt   string `json:"createdAt"`
	HandledAt   string `json:"handledAt,omitempty"`
}

func reportToView(r store.ReportRow) reportView {
	v := reportView{
		ID: r.ID, TextureID: r.TextureID, TextureName: r.TextureName,
		TextureType: r.TextureType, Uploader: r.Uploader, Reporter: r.Reporter,
		Reason: r.Reason, Detail: r.Detail, Status: r.Status, Public: r.Public,
		Moderation: r.Moderation,
		CreatedAt:  r.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
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
		writeErrCode(w, http.StatusInternalServerError, "reports query failed", i18n.MsgInternal)
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
		writeErrCode(w, http.StatusBadRequest, "bad report id", i18n.MsgBadID)
		return
	}
	var body struct {
		Action string `json:"action"`
	}
	if err := jsonDecode(r, &body); err != nil {
		writeErrCode(w, http.StatusBadRequest, "invalid JSON", i18n.MsgBadBody)
		return
	}
	row, err := b.Store.GetReport(r.Context(), id)
	if err != nil {
		writeErrCode(w, http.StatusNotFound, "report not found", i18n.MsgReportNotFound)
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
			writeErrCode(w, http.StatusConflict, "report already handled", i18n.MsgAlreadyDone)
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
		if err := b.Store.StrikeTexture(r.Context(), row.TextureID, id, handledBy); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeErrCode(w, http.StatusConflict, "report already handled", i18n.MsgAlreadyDone)
				return
			}
			writeErrCode(w, http.StatusInternalServerError, "hide texture failed", i18n.MsgInternal)
			return
		}
		b.Store.InsertAudit(r.Context(), handledBy, "texture_report_hide", map[string]any{
			"report": row.ID, "texture": row.TextureID, "moderation": "violation",
		})
	default:
		writeErrCode(w, http.StatusBadRequest, "action must be resolve|dismiss|hide", i18n.MsgUnknownAction)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleTextureModeration lifts a violation verdict. Lifting restores the
// right to publish, not the publication itself — the owner decides that.
func (b *Backend) handleTextureModeration(w http.ResponseWriter, r *http.Request) {
	tid, err := strconv.ParseInt(r.PathValue("tid"), 10, 64)
	if err != nil || tid <= 0 {
		writeErrCode(w, http.StatusBadRequest, "bad texture id", i18n.MsgTextureBadID)
		return
	}
	var body struct {
		Moderation string `json:"moderation"`
	}
	if err := jsonDecode(r, &body); err != nil {
		writeErrCode(w, http.StatusBadRequest, "invalid JSON", i18n.MsgBadBody)
		return
	}
	if body.Moderation != "" {
		writeErrCode(w, http.StatusBadRequest, "only an empty moderation value can be set here", i18n.MsgInvalidValue)
		return
	}
	handledBy := actorUID(r)
	if err := b.Store.LiftTextureModeration(r.Context(), tid); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErrCode(w, http.StatusNotFound, "texture not found", i18n.MsgTextureNotFound)
			return
		}
		writeErrCode(w, http.StatusInternalServerError, "moderation update failed", i18n.MsgInternal)
		return
	}
	b.Store.InsertAudit(r.Context(), handledBy, "texture_moderation_lift", map[string]any{"texture": tid})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleTextureDelete removes a texture for good: row, evidence copy, and the
// file when nothing else shares the content address. ?force=1 unbinds the
// characters/closet/avatar that still refer to it; without it the answer is 409
// naming them.
func (b *Backend) handleTextureDelete(w http.ResponseWriter, r *http.Request) {
	tid, err := strconv.ParseInt(r.PathValue("tid"), 10, 64)
	if err != nil || tid <= 0 {
		writeErrCode(w, http.StatusBadRequest, "bad texture id", i18n.MsgTextureBadID)
		return
	}
	reason := strings.TrimSpace(r.URL.Query().Get("reason"))
	if len([]rune(reason)) > 500 {
		writeErrCode(w, http.StatusBadRequest, "reason too long", i18n.MsgTooLong)
		return
	}
	force := r.URL.Query().Get("force") == "1"
	if b.Account == nil {
		writeErrCode(w, http.StatusInternalServerError, "texture service unavailable", i18n.MsgUnavailable)
		return
	}
	aerr := b.Account.DeleteTexture(r.Context(), actorUID(r), tid, 0, force, reason, account.CallMeta{IP: ygg.ClientIP(r)})
	if aerr == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	writeAccountErr(w, aerr)
}
