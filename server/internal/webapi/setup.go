// setup.go: first-run install wizard API (empty-database initialization).
//
// Active only while the site is uninitialized (schema missing or zero users
// and setup_done unset). After completion the endpoint answers 404 forever —
// the wizard is a one-way door. Mounted always; the gate is state-based so a
// fresh docker volume self-initializes without operator commands.
//
// The window between "the container answers on the public interface" and "the
// operator got around to the wizard" is precisely when nobody is authenticated,
// so the door needs a key the operator already holds: SKIN_SETUP_TOKEN when set,
// otherwise a token generated for that boot and printed to the log. Before this,
// whoever POSTed first simply became the site's super admin.
package webapi

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"skin-nexus/server/internal/auth"
	"skin-nexus/server/internal/i18n"
	"skin-nexus/server/internal/store"
)

// SetupState is the wizard's bootstrap snapshot (GET /api/v1/setup/status).
type SetupState struct {
	NeedsSetup bool `json:"needs_setup"`
}

func (b *Backend) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	need, err := b.Store.NeedsSetup(r.Context())
	if err != nil {
		writeInternal(r, w, err)
		return
	}
	// The token itself is never returned — only whether the wizard has to ask.
	writeJSON(w, http.StatusOK, map[string]any{
		"needs_setup":    need,
		"token_required": b.SetupToken != "",
	})
}

// POST /api/v1/setup — one-shot initialization: claim the one-way marker, create
// the admin account and write the collected options, in one transaction.
func (b *Backend) handleSetup(w http.ResponseWriter, r *http.Request) {
	need, err := b.Store.NeedsSetup(r.Context())
	if err != nil {
		writeInternal(r, w, err)
		return
	}
	if !need {
		// one-way door
		writeErrCode(w, http.StatusNotFound, "setup already completed", i18n.MsgSetupDone)
		return
	}
	// `needs_setup` is true for two very different sites: tables built and nobody
	// registered yet, versus no tables at all (a bare binary started without
	// SKIN_AUTO_MIGRATE). The second one used to fail on the first INSERT and the
	// operator was told the nickname or email was already taken.
	ok, err := b.Store.TableExists(r.Context(), "users")
	if err != nil {
		writeInternal(r, w, err)
		return
	}
	if !ok {
		writeErrCode(w, http.StatusServiceUnavailable, "the database has no tables yet; start once with SKIN_AUTO_MIGRATE=true or run skin-nexus migrate", i18n.MsgSetupDBMissing)
		return
	}
	var req struct {
		SiteName    string   `json:"site_name"`
		SiteURL     string   `json:"site_url"`
		Nickname    string   `json:"nickname"`
		Email       string   `json:"email"`
		Password    string   `json:"password"`
		PlayerLimit int      `json:"player_limit"`
		SkinDomains []string `json:"skin_domains"`
		Token       string   `json:"setup_token"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErrCode(w, http.StatusBadRequest, "invalid JSON", i18n.MsgSetupBadInput)
		return
	}
	if b.SetupToken != "" && subtle.ConstantTimeCompare([]byte(req.Token), []byte(b.SetupToken)) != 1 {
		writeErrCode(w, http.StatusForbidden, "the install token from SKIN_SETUP_TOKEN or the server log is required", i18n.MsgSetupTokenBad)
		return
	}
	req.SiteName = strings.TrimSpace(req.SiteName)
	req.Nickname = strings.TrimSpace(req.Nickname)
	req.Email = strings.TrimSpace(req.Email)
	siteURL := strings.TrimRight(strings.TrimSpace(req.SiteURL), "/")
	if req.Nickname == "" || len(req.Password) < 8 {
		writeErrCode(w, http.StatusBadRequest, "nickname is required and the password needs 8+ characters", i18n.MsgSetupBadInput)
		return
	}
	// Same rule as the panel's site_url setting, checked BEFORE anything is
	// written: a half-finished setup (admin created, address refused) is the
	// one state this one-way door cannot recover from.
	if siteURL != "" && !strings.HasPrefix(siteURL, "http://") && !strings.HasPrefix(siteURL, "https://") {
		writeErrCode(w, http.StatusBadRequest, "site_url must be an absolute http(s) URL", i18n.MsgInvalidValue)
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
		writeInternal(r, w, err)
		return
	}
	// site identity + SEO defaults. An empty site_name must not wipe seo_title
	// (review P3), so the two keys move together or not at all.
	opts := map[string]string{}
	if req.SiteName != "" {
		opts["site_name"] = req.SiteName
		opts["seo_title"] = req.SiteName
	}
	if siteURL != "" {
		// `site_url` is what the panel reads and writes. The wizard used to
		// write the legacy `base_url` key instead: same precedence over
		// SKIN_BASE_URL, but invisible in the settings table — so an address
		// typed as a LAN IP during install silently outranked the env forever and
		// nobody could find the row to fix. Old `base_url` values keep working
		// (resolveBaseURL falls back to them); only new writes move.
		opts["site_url"] = siteURL
	}
	if len(req.SkinDomains) > 0 {
		opts["skin_domains"] = strings.Join(req.SkinDomains, ",")
	}
	// The bootstrap account is the site SUPER_ADMIN (permission=2): plugin
	// install/market operations require exactly 2, not merely >0. Claiming
	// setup_done inside the same transaction is what makes a racing second caller
	// lose cleanly, and what lets a failed account creation be retried instead of
	// leaving an installed-looking site with nobody able to sign in.
	uid, claimed, err := b.Store.ClaimSetup(r.Context(), store.SetupSpec{
		Email: req.Email, Nickname: req.Nickname, PasswordHash: hash,
		PlayerLimit: req.PlayerLimit, Options: opts,
	})
	if err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			writeErrCode(w, http.StatusConflict, "could not create the account (nickname or email may already exist)", i18n.MsgIdentifierTaken)
			return
		}
		writeInternal(r, w, err)
		return
	}
	if !claimed {
		// Somebody won the claim while this request was in flight; the door is
		// one-way, so answer exactly as if we had checked first.
		writeErrCode(w, http.StatusNotFound, "setup already completed", i18n.MsgSetupDone)
		return
	}
	b.Store.InsertAudit(r.Context(), uid, "setup_completed", map[string]interface{}{
		"site_name": req.SiteName,
	})
	// The completion screen has to say which address the site will actually use
	// and whether it disagrees with the env the operator set — that is the one
	// moment where a wrong (LAN-IP) value is still cheap to fix.
	envBase := strings.TrimRight(b.BaseURL, "/")
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok": true, "uid": uid,
		"site_url":     siteURL,
		"env_base_url": envBase,
		"mismatch":     siteURL != "" && envBase != "" && !strings.EqualFold(siteURL, envBase),
	})
}
