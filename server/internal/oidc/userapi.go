// userapi.go is the session-authenticated "authorized apps" surface: list the
// caller's live OIDC grants and revoke them. Mounted under the existing
// /api/v1/oidc/ DeviceAPIHandler mux (single ServeMux, exact-path patterns).
package oidc

import (
	"encoding/json"
	"net/http"
	"time"

	"skin-nexus/server/internal/i18n"
)

// writeErrStatus reports a real HTTP status (review B/C P2: every error used to
// answer 200, so any non-SPA client read failures as success) and carries the
// same {error, code, message} envelope as the rest of the API. `error` stays the
// code rather than prose so an older client that only reads that key still gets
// a stable token; `message` is the English fallback.
func writeErrStatus(w http.ResponseWriter, status int, msg, code string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errEnvelope(code, msg))
}

// GET /api/v1/oidc/tokens — the caller's live (non-revoked, non-expired)
// access-token ledger entries, newest first.
func (p *Provider) listTokens(w http.ResponseWriter, r *http.Request) {
	uid, err := p.sessionUser(r)
	if err != nil {
		unauthorized(w)
		return
	}
	recs, err := p.backend.ListUserAccessTokens(r.Context(), uid)
	if err != nil {
		writeErrStatus(w, http.StatusInternalServerError, "could not list authorized apps", i18n.MsgInternal)
		return
	}
	type item struct {
		JTI         string    `json:"jti"`
		ClientID    string    `json:"client_id"`
		Scopes      []string  `json:"scopes"`
		SelectedPID int64     `json:"selected_pid"`
		CreatedAt   time.Time `json:"created_at"`
		ExpiresAt   time.Time `json:"expires_at"`
	}
	out := make([]item, 0, len(recs))
	for _, rec := range recs {
		out = append(out, item{
			JTI:         rec.JTI,
			ClientID:    rec.ClientID,
			Scopes:      rec.Scopes,
			SelectedPID: rec.SelectedPID,
			CreatedAt:   rec.CreatedAt,
			ExpiresAt:   rec.ExpiresAt,
		})
	}
	writeJSON(w, map[string]any{"tokens": out})
}

// POST /api/v1/oidc/tokens/revoke — body {"jti": "..."} (or
// {"all": true} to sign out everywhere). Single revocation kills the jti plus
// the caller's live refresh tokens for the same client (the refresh table has
// no jti column — uid+client is the narrowest granularity; the response
// reports that scope). {"all": true} revokes every access + refresh token.
func (p *Provider) revokeToken(w http.ResponseWriter, r *http.Request) {
	uid, err := p.sessionUser(r)
	if err != nil {
		unauthorized(w)
		return
	}
	var body struct {
		JTI string `json:"jti"`
		All bool   `json:"all"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErrStatus(w, http.StatusBadRequest, "invalid request body", i18n.MsgBadBody)
		return
	}
	if body.All {
		// Both families are attempted even when one fails (review B P3: partial
		// completion was invisible). Which code answers depends on how much
		// survived: nothing revoked is an internal failure, half revoked is a
		// partial one the caller must not read as "signed out everywhere".
		errAccess := p.backend.RevokeUserAccessTokens(r.Context(), uid)
		errRefresh := p.backend.RevokeUserRefreshTokens(r.Context(), uid)
		switch {
		case errAccess != nil && errRefresh != nil:
			writeErrStatus(w, http.StatusInternalServerError, "could not revoke tokens", i18n.MsgInternal)
			return
		case errAccess != nil || errRefresh != nil:
			writeErrStatus(w, http.StatusInternalServerError, "some tokens survived the revocation", i18n.MsgOIDCRevokePartial)
			return
		}
		p.auditEvent("oidc.tokens_revoke_all", map[string]any{"uid": uid})
		writeJSON(w, map[string]any{"ok": true, "scope": "all"})
		return
	}
	if body.JTI == "" {
		writeErrStatus(w, http.StatusBadRequest, "jti is required", i18n.MsgMissingField)
		return
	}
	// Ownership check: never let one user revoke another user's jti.
	rec, err := p.backend.GetAccessToken(r.Context(), body.JTI)
	if err != nil || rec.UserID != uid {
		writeErrStatus(w, http.StatusNotFound, "no such authorization", i18n.MsgOIDCGrantNotFound)
		return
	}
	if err := p.backend.RevokeAccessToken(r.Context(), body.JTI); err != nil {
		writeErrStatus(w, http.StatusInternalServerError, "could not revoke the grant", i18n.MsgInternal)
		return
	}
	p.auditEvent("oidc.token_revoke", map[string]any{
		"uid": uid, "jti": body.JTI, "client_id": rec.ClientID,
	})
	// Refresh table carries no jti: revoke the uid+client refresh scope. The jti
	// is already dead and cannot be un-revoked, so a failure here is reported as
	// partial rather than swallowed — answering ok would tell the caller a still
	// live refresh token is dead, and it can mint new access tokens.
	if err := p.backend.RevokeClientRefreshTokens(r.Context(), uid, rec.ClientID); err != nil {
		writeErrStatus(w, http.StatusInternalServerError, "the grant was revoked but its refresh tokens survived", i18n.MsgOIDCRevokePartial)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "scope": "jti+client_refresh"})
}
