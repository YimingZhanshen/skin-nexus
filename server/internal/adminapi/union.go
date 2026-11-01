// union.go: admin surface of the MUA Union member module — signer status,
// rotation control, ops buttons (B5.3) and the union blacklist proxy (B5.4).
// Dependencies are injected from main (the signer/API live in the union
// package; adminapi stays decoupled and 404s when unset). Wire contract in
// internal/union is FROZEN — this file only orchestrates existing calls.
package adminapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"skin-nexus/server/internal/i18n"
	"skin-nexus/server/internal/union"
)

// UnionDeps are wired from main; nil = routes absent. Enabled (optional, hot
// toggle 2026-10-24 拍板) makes every admin union route answer 404 while the
// module is switched off — same zero-surface semantics as /api/union/*.
type UnionDeps struct {
	Enabled func() bool
	// Status returns the current signer/rotation view for the dashboard.
	Status func() map[string]any
	// ResumeRotation lifts the daily-switch suspension (operator action).
	ResumeRotation func() error
	// ForceRotation runs one hub key pull/switch now ("更新密钥对").
	ForceRotation func(ctx context.Context) (bool, error)
	// RefreshServerList pulls the hub roster of member sites ("更新服务器列表")
	// and returns it for display.
	RefreshServerList func(ctx context.Context) (any, error)
	// SyncRoster pushes the full local profile roster ("同步角色数据");
	// returns the pushed count.
	SyncRoster func(ctx context.Context) (int, error)
	// Diagnose pings the hub and reports in the legacy triggerDiagnose
	// shape ({status,data}) — "自助诊断" 1:1 with yggdrasil-connect.
	Diagnose func(ctx context.Context) (*union.DiagnoseReport, error)
	// Blacklist proxy (B5.4): query/add/release(=invalidate, keeps record)/
	// delete(irreversible). Release and delete are PERMANENT operator actions
	// — frontend double-confirms and both write audit rows here.
	BlacklistQuery   func(ctx context.Context, q url.Values) (status int, body json.RawMessage, err error)
	BlacklistAdd     func(ctx context.Context, body json.RawMessage) (status int, resp json.RawMessage, err error)
	BlacklistRelease func(ctx context.Context, id string) (status int, resp json.RawMessage, err error)
	BlacklistDelete  func(ctx context.Context, id string) (status int, resp json.RawMessage, err error)
}

// MountUnion registers the union admin routes when deps are present.
func (b *Backend) MountUnion(deps UnionDeps) {
	b.union = deps
}

// unionGate wraps every admin union handler with the hot toggle (2026-10-24
// 拍板): 404 for the whole subtree while the module is switched off, so the
// disabled state is indistinguishable from "route absent" (zero surface).
func (b *Backend) unionGate(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if b.union.Enabled != nil && !b.union.Enabled() {
			http.NotFound(w, r)
			return
		}
		next(w, r)
	}
}

func (b *Backend) handleUnionStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, b.union.Status())
}

func (b *Backend) handleUnionRotationResume(w http.ResponseWriter, r *http.Request) {
	if err := b.union.ResumeRotation(); err != nil {
		writeDomain(w, http.StatusConflict, err, i18n.MsgConflict)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- B5.3 ops buttons ----

// POST /api/admin/v1/union/ops/key-update — one hub key pull/switch pass.
func (b *Backend) handleUnionKeyUpdate(w http.ResponseWriter, r *http.Request) {
	switched, err := b.union.ForceRotation(r.Context())
	if err != nil {
		writeLogged(r, w, http.StatusBadGateway, i18n.MsgUnionUpstream, "upstream request failed", err)
		return
	}
	b.Store.InsertAudit(r.Context(), actorUID(r), "union_key_update",
		map[string]any{"switched": switched})
	writeJSON(w, http.StatusOK, map[string]any{
		"switched":            switched,
		"key_fingerprint":     b.union.Status()["key_fingerprint"],
		"private_key_version": b.union.Status()["private_key_version"],
	})
}

// POST /api/admin/v1/union/ops/serverlist — pull the member-site roster.
func (b *Backend) handleUnionServerList(w http.ResponseWriter, r *http.Request) {
	list, err := b.union.RefreshServerList(r.Context())
	if err != nil {
		writeLogged(r, w, http.StatusBadGateway, i18n.MsgUnionUpstream, "upstream request failed", err)
		return
	}
	b.Store.InsertAudit(r.Context(), actorUID(r), "union_serverlist_refresh", nil)
	writeJSON(w, http.StatusOK, map[string]any{"serverlist": list})
}

// POST /api/admin/v1/union/ops/roster-sync — full roster push.
func (b *Backend) handleUnionRosterSync(w http.ResponseWriter, r *http.Request) {
	n, err := b.union.SyncRoster(r.Context())
	if err != nil {
		writeLogged(r, w, http.StatusBadGateway, i18n.MsgUnionUpstream, "upstream request failed", err)
		return
	}
	b.Store.InsertAudit(r.Context(), actorUID(r), "union_roster_sync",
		map[string]any{"profiles": n})
	writeJSON(w, http.StatusOK, map[string]any{"profiles": n})
}

// POST /api/admin/v1/union/ops/diagnose — connectivity + versions probe.
// Response is the legacy yggdrasil-connect triggerDiagnose shape ({status,
// data}) so the admin UI renders 1:1 rows; site key metadata is appended
// for the exported report header.
func (b *Backend) handleUnionDiagnose(w http.ResponseWriter, r *http.Request) {
	rep, err := b.union.Diagnose(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"status": "error",
			"data":   map[string]any{"exception": err.Error()},
		})
		return
	}
	st := b.union.Status()
	writeJSON(w, http.StatusOK, map[string]any{
		"status":              rep.Status,
		"data":                json.RawMessage(rep.Data),
		"key_fingerprint":     st["key_fingerprint"],
		"private_key_version": st["private_key_version"],
		"server_list_version": st["server_list_version"],
	})
}

// ---- B5.4 blacklist proxy ----

// relayBlacklist pipes a hub ProxyResult back untouched (status + body), the
// same pass-through the upstream UnionBlacklistController performs.
func relayBlacklist(w http.ResponseWriter, status int, body json.RawMessage) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if len(body) > 0 {
		_, _ = w.Write(body)
	}
}

// GET /api/admin/v1/union/blacklist?q=&page= — hub query (params passed
// through as-is; hub owns pagination semantics).
func (b *Backend) handleUnionBlacklistQuery(w http.ResponseWriter, r *http.Request) {
	status, body, err := b.union.BlacklistQuery(r.Context(), r.URL.Query())
	if err != nil {
		writeLogged(r, w, http.StatusBadGateway, i18n.MsgUnionUpstream, "upstream request failed", err)
		return
	}
	relayBlacklist(w, status, body)
}

// POST /api/admin/v1/union/blacklist {email, reason} — add an entry.
func (b *Backend) handleUnionBlacklistAdd(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email  string `json:"email"`
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Email) == "" {
		writeErrCode(w, http.StatusBadRequest, "email is required", i18n.MsgMissingField)
		return
	}
	payload, _ := json.Marshal(map[string]string{
		"email": strings.TrimSpace(req.Email), "reason": strings.TrimSpace(req.Reason),
	})
	status, resp, err := b.union.BlacklistAdd(r.Context(), payload)
	if err != nil {
		writeLogged(r, w, http.StatusBadGateway, i18n.MsgUnionUpstream, "upstream request failed", err)
		return
	}
	b.Store.InsertAudit(r.Context(), actorUID(r), "union_blacklist_add",
		map[string]any{"email": req.Email, "hub_status": status})
	relayBlacklist(w, status, resp)
}

// PUT /api/admin/v1/union/blacklist/{id}/invalidate — 释放 (unban, record
// stays). NOT reversible-by-accident: one wrong id un-bans a banned account,
// so the frontend double-confirms too.
func (b *Backend) handleUnionBlacklistRelease(w http.ResponseWriter, r *http.Request) {
	id := blacklistIDParam(r)
	if id == "" {
		writeErrCode(w, http.StatusBadRequest, "id is required", i18n.MsgMissingField)
		return
	}
	status, resp, err := b.union.BlacklistRelease(r.Context(), id)
	if err != nil {
		writeLogged(r, w, http.StatusBadGateway, i18n.MsgUnionUpstream, "upstream request failed", err)
		return
	}
	b.Store.InsertAudit(r.Context(), actorUID(r), "union_blacklist_release",
		map[string]any{"id": id, "hub_status": status})
	relayBlacklist(w, status, resp)
}

// DELETE /api/admin/v1/union/blacklist/{id} — 彻底删 (irreversible: the hub
// erases the record). Double-confirm + audit + actor attribution mandatory.
func (b *Backend) handleUnionBlacklistDelete(w http.ResponseWriter, r *http.Request) {
	id := blacklistIDParam(r)
	if id == "" {
		writeErrCode(w, http.StatusBadRequest, "id is required", i18n.MsgMissingField)
		return
	}
	status, resp, err := b.union.BlacklistDelete(r.Context(), id)
	if err != nil {
		writeLogged(r, w, http.StatusBadGateway, i18n.MsgUnionUpstream, "upstream request failed", err)
		return
	}
	b.Store.InsertAudit(r.Context(), actorUID(r), "union_blacklist_delete",
		map[string]any{"id": id, "hub_status": status, "irreversible": true})
	relayBlacklist(w, status, resp)
}

// blacklistIDParam extracts the trailing {id} segment registered in the route
// pattern /blacklist/{id}/invalidate and /blacklist/{id}.
func blacklistIDParam(r *http.Request) string {
	p := r.PathValue("id")
	return strings.TrimSpace(p)
}
