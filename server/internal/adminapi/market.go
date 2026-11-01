package adminapi

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"skin-nexus/server/internal/i18n"
	"skin-nexus/server/internal/plugin"
)

// sameOriginForAdmin is the P2 market CSRF gate: a browser mutation must carry
// an Origin matching Host (or Sec-Fetch-Site: same-origin). The super-admin
// session is still required; X-Admin-Token is rejected by pluginSuper.
func sameOriginForAdmin(r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" && origin != "null" {
		u, err := url.Parse(origin)
		return err == nil && strings.EqualFold(u.Host, r.Host)
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		return site == "same-origin" || site == "none"
	}
	return false
}

func (b *Backend) pluginSuper(w http.ResponseWriter, r *http.Request) (int64, bool) {
	actor := actorUID(r)
	if actor == 0 {
		writeErrCode(w, http.StatusForbidden, "this action needs a super admin web session", i18n.MsgWebSessionRequired)
		return 0, false
	}
	perm, err := actorPermission(r.Context(), b, actor)
	if err != nil || perm != 2 {
		writeErrCode(w, http.StatusForbidden, "a super admin account (permission=2) is required", i18n.MsgSuperAdminRequired)
		return 0, false
	}
	if !sameOriginForAdmin(r) {
		writeErrCode(w, http.StatusForbidden, "origin check failed", i18n.MsgOriginMismatch)
		return 0, false
	}
	return actor, true
}

func (b *Backend) handlePluginRollback(w http.ResponseWriter, r *http.Request) {
	actor, ok := b.pluginSuper(w, r)
	if !ok {
		return
	}
	ctx := plugin.WithActor(r.Context(), actor)
	if err := b.plugins.Host.Rollback(ctx, r.PathValue("name")); err != nil {
		if errors.Is(err, plugin.ErrNoRollback) {
			writeErrCode(w, http.StatusConflict, "there is no version to roll back to", i18n.MsgPluginNoRollback)
			return
		}
		writeLogged(r, w, http.StatusBadGateway, i18n.MsgPluginOpFailed, "plugin operation failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (b *Backend) handlePluginUninstall(w http.ResponseWriter, r *http.Request) {
	actor, ok := b.pluginSuper(w, r)
	if !ok {
		return
	}
	purge := r.URL.Query().Get("purge") == "1"
	ctx := plugin.WithActor(r.Context(), actor)
	if err := b.plugins.Host.Uninstall(ctx, r.PathValue("name"), purge); err != nil {
		writeLogged(r, w, http.StatusBadGateway, i18n.MsgMarketUpstream, "upstream request failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (b *Backend) handleMarketCatalog(w http.ResponseWriter, r *http.Request) {
	list, err := b.plugins.Host.MarketCatalog(r.Context(), r.URL.Query().Get("refresh") == "1")
	if err != nil {
		writeLogged(r, w, http.StatusServiceUnavailable, i18n.MsgMarketUpstream, "upstream request failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"packages": list})
}

func (b *Backend) handleMarketPlan(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := jsonDecode(r, &body); err != nil || body.Name == "" {
		writeErrCode(w, http.StatusBadRequest, "name required", i18n.MsgMissingField)
		return
	}
	plan, err := b.plugins.Host.MarketPlan(r.Context(), body.Name)
	if err != nil {
		writeLogged(r, w, http.StatusBadGateway, i18n.MsgMarketUpstream, "upstream request failed", err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (b *Backend) handleMarketInstall(w http.ResponseWriter, r *http.Request) {
	actor, ok := b.pluginSuper(w, r)
	if !ok {
		return
	}
	var body struct {
		Name    string `json:"name"`
		Consent bool   `json:"consent"`
	}
	if err := jsonDecode(r, &body); err != nil || body.Name == "" {
		writeErrCode(w, http.StatusBadRequest, "name required", i18n.MsgMissingField)
		return
	}
	ctx := plugin.WithActor(r.Context(), actor)
	res, err := b.plugins.Host.MarketInstall(ctx, body.Name, body.Consent)
	switch {
	case errors.Is(err, plugin.ErrConsentRequired):
		// The plan IS the dialog — which capabilities need consent. This error
		// text was written by this repository for that screen, so it stays.
		writeErrBody(w, http.StatusConflict, i18n.MsgPluginConsent, err, map[string]any{"plan": res.Plan})
	case errors.Is(err, plugin.ErrInstallBlocked):
		writeErrBody(w, http.StatusConflict, i18n.MsgPluginBlocked, err, map[string]any{"plan": res.Plan})
	case err != nil:
		writeLogged(r, w, http.StatusBadGateway, i18n.MsgMarketUpstream, "upstream request failed", err)
	default:
		writeJSON(w, http.StatusOK, res)
	}
}

func (b *Backend) handleMarketUpload(w http.ResponseWriter, r *http.Request) {
	actor, ok := b.pluginSuper(w, r)
	if !ok {
		return
	}
	maxUpload := b.plugins.Host.UploadMax()
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload+1<<20)
	if err := r.ParseMultipartForm(maxUpload); err != nil {
		writeErrCode(w, http.StatusBadRequest, "multipart parse failed", i18n.MsgBadBody)
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		writeErrCode(w, http.StatusBadRequest, "file required", i18n.MsgMissingField)
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxUpload+1))
	if err != nil || int64(len(data)) > maxUpload {
		writeErrCode(w, http.StatusRequestEntityTooLarge, "bundle too large", i18n.MsgTooLong)
		return
	}
	consent := r.FormValue("consent") == "1"
	ctx := plugin.WithActor(r.Context(), actor)
	if r.FormValue("inspect") == "1" {
		insp, ierr := b.plugins.Host.InspectBytes(ctx, data)
		if ierr != nil {
			writeDomain(w, http.StatusBadRequest, ierr, i18n.MsgInvalidValue)
			return
		}
		writeJSON(w, http.StatusOK, insp)
		return
	}
	opts := plugin.InstallOptions{
		Consent:          consent,
		Source:           "upload",
		AcceptUnverified: r.FormValue("acceptUnverified") == "1",
		ExpectedZipSHA:   strings.TrimSpace(r.FormValue("expectDigest")),
	}
	res, err := b.plugins.Host.InstallBytes(ctx, data, opts)
	switch {
	case errors.Is(err, plugin.ErrUnverifiedPlugin):
		// The trust dialog is driven by the inspection payload plus this reason;
		// `acceptUnverified` is the operator overriding it, so the body has to
		// say what was unverified.
		writeErrBody(w, http.StatusConflict, i18n.MsgPluginUnverified, err,
			map[string]any{"inspection": res.Inspection})
	case errors.Is(err, plugin.ErrDigestMismatch):
		writeErrBody(w, http.StatusConflict, i18n.MsgDigestMismatch, err,
			map[string]any{"inspection": res.Inspection})
	case errors.Is(err, plugin.ErrConsentRequired):
		writeErrBody(w, http.StatusConflict, i18n.MsgPluginConsent, err,
			map[string]any{"plan": res.Plan, "inspection": res.Inspection})
	case errors.Is(err, plugin.ErrInstallBlocked):
		writeErrBody(w, http.StatusConflict, i18n.MsgPluginBlocked, err,
			map[string]any{"plan": res.Plan, "inspection": res.Inspection})
	case err != nil:
		writeLogged(r, w, http.StatusBadRequest, i18n.MsgInvalidValue, "package rejected", err)
	default:
		writeJSON(w, http.StatusOK, res)
	}
}
