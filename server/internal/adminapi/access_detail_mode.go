// Access-log diagnostic mode switch (2026-11-05 拍板).
//
// Slice 5b is ONLY the switch + its retention setting. The detail WRITER, the
// reader API and the sweeper are later slices — nothing here inserts into or
// deletes from access_log_details. These three endpoints read/write the
// `access_detail_until` option (UTC RFC3339 expiry) and record one audit row
// per flip; the writer consults accesslog.Diagnostic elsewhere.
//
// Auth shape: only a SUPER ADMIN (permission = 2) may change the switch, and —
// matching the surrounding sensitive writes (userSetPermission granting admin,
// pluginSuper) — the change must arrive on a WEB SESSION, not X-Admin-Token:
// actorUID(r) == 0 is the machine/API channel, where "who flipped diagnostics"
// is unrecoverable for the audit trail. The read stays open to every admin
// (any permission > 0) under the shared b.auth wrapper, like GET /settings.
package adminapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"skin-nexus/server/internal/accesslog"
	"skin-nexus/server/internal/i18n"
)

// detailMaxWindow is the hard cap on how far the diagnostic window may be
// opened from now. Beyond it the request is rejected, never silently clamped
// — a silent clamp would let an operator believe they opened a week and be
// surprised when detail capture stops after 24h.
const detailMaxWindow = 24 * time.Hour

// Seams so the handlers are unit-testable without a database or the real
// clock: option read/write, audit insert, and the clock. Defaults hit the
// store and the wall clock; tests override them. (Same package-var pattern as
// sessionAdmin / actorPermission.)
var (
	detailReadOption = func(ctx context.Context, b *Backend, key string) (string, bool, error) {
		return b.Store.GetOption(ctx, key)
	}
	detailWriteOption = func(ctx context.Context, b *Backend, key, val string) error {
		return b.Store.SetOption(ctx, key, val)
	}
	detailInsertAudit = func(ctx context.Context, b *Backend, actor int64, action string, detail map[string]any) {
		b.Store.InsertAudit(ctx, actor, action, detail)
	}
	detailNow = func() time.Time { return time.Now().UTC() }
)

// diagnostic builds the shared switch reader from the store seam + the
// injected clock, so the GET endpoint reports exactly what the writer sees.
func (b *Backend) diagnostic() *accesslog.Diagnostic {
	return &accesslog.Diagnostic{
		Option: func(ctx context.Context, key string) (string, bool, error) {
			return detailReadOption(ctx, b, key)
		},
		Now: detailNow,
	}
}

// detailModeSuper is the write gate: web session (not the token channel) +
// permission == 2. It mirrors the inline guard in userSetPermission rather
// than pluginSuper's same-origin check, which belongs to the plugin/market
// mount and is not part of this feature's surrounding surface.
func (b *Backend) detailModeSuper(w http.ResponseWriter, r *http.Request) (int64, bool) {
	actor := actorUID(r)
	if actor == 0 {
		writeErrCode(w, http.StatusForbidden, "this action needs a super admin web session, not X-Admin-Token", i18n.MsgWebSessionRequired)
		return 0, false
	}
	perm, err := actorPermission(r.Context(), b, actor)
	if err != nil || perm != 2 {
		writeErrCode(w, http.StatusForbidden, "a super admin account (permission=2) is required", i18n.MsgSuperAdminRequired)
		return 0, false
	}
	return actor, true
}

// GET /api/admin/v1/access-log/detail-mode — live state for any admin. Reads
// the option and the Go clock; never re-runs a SQL time comparison.
func (b *Backend) handleDetailModeGet(w http.ResponseWriter, r *http.Request) {
	until, secs, active := b.diagnostic().DiagnosticUntil(r.Context())
	out := map[string]any{"enabled": active, "until": "", "seconds_remaining": int64(0)}
	if active {
		out["until"] = until.Format(time.RFC3339)
		out["seconds_remaining"] = secs
	}
	writeJSON(w, http.StatusOK, out)
}

// POST /api/admin/v1/access-log/detail-mode {until} — open the window (super
// admin web session). Validates: absolute RFC3339 time, in the future, within
// 24h of now; then writes the option verbatim and one audit row.
func (b *Backend) handleDetailModeSet(w http.ResponseWriter, r *http.Request) {
	actor, ok := b.detailModeSuper(w, r)
	if !ok {
		return
	}
	var req struct {
		Until string `json:"until"`
	}
	if err := jsonDecode(r, &req); err != nil {
		writeErrCode(w, http.StatusBadRequest, "invalid JSON", i18n.MsgBadBody)
		return
	}
	raw := strings.TrimSpace(req.Until)
	now := detailNow()
	// Parseable + strictly future, or reject with the "bad/expired" code. The
	// two input failures carry distinct codes because the operator fixes them
	// differently — this one means "give me an absolute time in the future".
	until, err := time.Parse(time.RFC3339, raw)
	if err != nil || !until.UTC().After(now) {
		writeErrCode(w, http.StatusBadRequest, "until must be an absolute RFC3339 timestamp in the future", i18n.MsgDetailUntilPast)
		return
	}
	// Cap at 24h from now. The boundary is INCLUSIVE (now+24h is accepted,
	// now+24h+1s is rejected): an operator picking "on for 24 hours" in the
	// panel computes exactly now+24h, and rejecting that would make the
	// largest legitimate preset fail while a sloppy 23:59 still passed.
	if until.UTC().Sub(now) > detailMaxWindow {
		writeErrCode(w, http.StatusBadRequest, "the diagnostic window may not exceed 24 hours from now", i18n.MsgDetailUntilTooFar)
		return
	}
	if err := detailWriteOption(r.Context(), b, accesslog.DetailUntilOption, raw); err != nil {
		writeErrCode(w, http.StatusInternalServerError, "save failed", i18n.MsgOptionSaveFailed)
		return
	}
	detailInsertAudit(r.Context(), b, actor, "access_detail_mode_enable", map[string]any{"until": raw})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "enabled": true, "until": until.UTC().Format(time.RFC3339)})
}

// DELETE /api/admin/v1/access-log/detail-mode — close the window now (super
// admin web session): clears the option and writes one audit row.
func (b *Backend) handleDetailModeDelete(w http.ResponseWriter, r *http.Request) {
	actor, ok := b.detailModeSuper(w, r)
	if !ok {
		return
	}
	if err := detailWriteOption(r.Context(), b, accesslog.DetailUntilOption, ""); err != nil {
		writeErrCode(w, http.StatusInternalServerError, "save failed", i18n.MsgOptionSaveFailed)
		return
	}
	detailInsertAudit(r.Context(), b, actor, "access_detail_mode_disable", nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "enabled": false})
}
