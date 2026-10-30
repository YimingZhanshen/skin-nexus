package adminapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"skin-nexus/server/internal/jobs"
)

type jobView struct {
	ID          string `json:"id"`
	Plugin      string `json:"plugin"`
	Handler     string `json:"handler"`
	OwnerUID    int64  `json:"ownerUid"`
	State       string `json:"state"`
	Progress    int    `json:"progress"`
	Message     string `json:"message"`
	LastError   string `json:"lastError,omitempty"`
	Attempts    int    `json:"attempts"`
	MaxAttempts int    `json:"maxAttempts"`
	Reclaims    int    `json:"reclaims"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
	StartedAt   string `json:"startedAt,omitempty"`
	FinishedAt  string `json:"finishedAt,omitempty"`
	Payload     any    `json:"payload,omitempty"`
	Cursor      any    `json:"cursor,omitempty"`
	Result      any    `json:"result,omitempty"`
}

func jobToView(j jobs.Job) jobView {
	v := jobView{
		ID: j.ID, Plugin: j.Plugin, Handler: j.Handler, OwnerUID: j.OwnerUID,
		State: j.State, Progress: j.Progress, Message: j.Message, LastError: j.LastError,
		Attempts: j.Attempts, MaxAttempts: j.MaxAttempts, Reclaims: j.Reclaims,
		CreatedAt: j.CreatedAt.UTC().Format(time.RFC3339), UpdatedAt: j.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if !j.StartedAt.IsZero() {
		v.StartedAt = j.StartedAt.UTC().Format(time.RFC3339)
	}
	if !j.FinishedAt.IsZero() {
		v.FinishedAt = j.FinishedAt.UTC().Format(time.RFC3339)
	}
	if len(j.Payload) > 0 {
		_ = json.Unmarshal(j.Payload, &v.Payload)
	}
	if len(j.Cursor) > 0 {
		_ = json.Unmarshal(j.Cursor, &v.Cursor)
	}
	if len(j.Result) > 0 {
		_ = json.Unmarshal(j.Result, &v.Result)
	}
	return v
}

func (b *Backend) handleJobsList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	perPage, _ := strconv.Atoi(q.Get("perPage"))
	list, total, err := b.Jobs.List(r.Context(), strings.TrimSpace(q.Get("plugin")), strings.TrimSpace(q.Get("state")), page, perPage)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "jobs query failed"})
		return
	}
	out := make([]jobView, 0, len(list))
	for _, j := range list {
		out = append(out, jobToView(j))
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": out, "total": total})
}

func (b *Backend) handleJobGet(w http.ResponseWriter, r *http.Request) {
	j, err := b.Jobs.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "job not found"})
		return
	}
	writeJSON(w, http.StatusOK, jobToView(j))
}

func (b *Backend) handleJobCancel(w http.ResponseWriter, r *http.Request) {
	if err := b.Jobs.RequestCancel(r.Context(), r.PathValue("id")); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cancel failed"})
		return
	}
	b.Store.InsertAudit(r.Context(), actorUID(r), "plugin_job_cancel", map[string]any{"job": r.PathValue("id")})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (b *Backend) handleJobResume(w http.ResponseWriter, r *http.Request) {
	if err := b.Jobs.Resume(r.Context(), r.PathValue("id")); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "job cannot be resumed"})
		return
	}
	b.Store.InsertAudit(r.Context(), actorUID(r), "plugin_job_resume", map[string]any{"job": r.PathValue("id")})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
