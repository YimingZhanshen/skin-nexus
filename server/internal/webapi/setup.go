// setup.go: first-run install wizard API (empty-database initialization).
//
// Active only while the site is uninitialized (schema missing or zero users
// and setup_done unset). After completion the endpoint answers 404 forever —
// the wizard is a one-way door. Mounted always; the gate is state-based so a
// fresh docker volume self-initializes without operator commands.
package webapi

import (
	"net/http"
	"strings"

	"skin-nexus/server/internal/auth"
	"skin-nexus/server/internal/i18n"
)

// SetupState is the wizard's bootstrap snapshot (GET /api/v1/setup/status).
type SetupState struct {
	NeedsSetup bool `json:"needs_setup"`
}

func (b *Backend) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	need, err := b.Store.NeedsSetup(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "setup state unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"needs_setup": need})
}

// POST /api/v1/setup — one-shot initialization:
// create the admin account, write site_name + SEO defaults, mark done.
func (b *Backend) handleSetup(w http.ResponseWriter, r *http.Request) {
	need, err := b.Store.NeedsSetup(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "setup state unavailable")
		return
	}
	if !need {
		// one-way door
		writeErr(w, http.StatusNotFound, "setup already completed")
		return
	}
	var req struct {
		SiteName    string   `json:"site_name"`
		BaseURL     string   `json:"base_url"`
		Nickname    string   `json:"nickname"`
		Email       string   `json:"email"`
		Password    string   `json:"password"`
		PlayerLimit int      `json:"player_limit"`
		SkinDomains []string `json:"skin_domains"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErrCode(w, http.StatusBadRequest, "invalid JSON", i18n.Msg(i18n.MsgSetupBadInput))
		return
	}
	req.SiteName = strings.TrimSpace(req.SiteName)
	req.Nickname = strings.TrimSpace(req.Nickname)
	req.Email = strings.TrimSpace(req.Email)
	if req.Nickname == "" || len(req.Password) < 8 {
		writeErrCode(w, http.StatusBadRequest, "昵称必填、密码至少 8 位", i18n.Msg(i18n.MsgSetupBadInput))
		return
	}
	if req.Email == "" {
		req.Email = "admin@localhost"
	}
	if req.PlayerLimit <= 0 || req.PlayerLimit > 16 {
		req.PlayerLimit = 3
	}
	hash, err := auth.HashArgon2id(req.Password)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "hash failed")
		return
	}
	// The wizard's bootstrap account is the site SUPER_ADMIN (permission=2):
	// plugin install/market operations require exactly 2, not merely >0.
	uid, err := b.Store.CreateUserAdmin(r.Context(), req.Email, req.Nickname, hash, req.PlayerLimit)
	if err != nil {
		writeErr(w, http.StatusConflict, "账号创建失败（昵称或邮箱可能已存在）")
		return
	}
	// site identity + SEO defaults
	if req.SiteName != "" {
		_ = b.Store.SetOption(r.Context(), "site_name", req.SiteName)
	}
	if req.BaseURL != "" {
		_ = b.Store.SetOption(r.Context(), "base_url", strings.TrimRight(req.BaseURL, "/"))
	}
	if len(req.SkinDomains) > 0 {
		_ = b.Store.SetOption(r.Context(), "skin_domains", strings.Join(req.SkinDomains, ","))
	}
	if req.SiteName != "" { // review P3: empty SiteName used to wipe seo_title
		_ = b.Store.SetOption(r.Context(), "seo_title", req.SiteName)
	}
	_ = b.Store.SetOption(r.Context(), "setup_done", "1")
	b.Store.InsertAudit(r.Context(), uid, "setup_completed", map[string]interface{}{
		"site_name": req.SiteName,
	})
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "uid": uid})
}
