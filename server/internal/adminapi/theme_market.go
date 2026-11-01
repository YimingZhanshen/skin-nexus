// theme_market.go: admin surface for the theme market (catalog / install /
// uninstall) and local theme-pack uploads. Gating mirrors the plugin market:
// catalog reads need any admin; every mutation needs a super-admin WEB
// session (X-Admin-Token rejected) plus the same-origin CSRF gate.
package adminapi

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"skin-nexus/server/internal/i18n"
	"skin-nexus/server/internal/thememkt"
)

func (b *Backend) handleThemeMarketCatalog(w http.ResponseWriter, r *http.Request) {
	list, err := b.appearance.ThemeMarket.Catalog(r.Context(), r.URL.Query().Get("refresh") == "1")
	if err != nil {
		if errors.Is(err, thememkt.ErrMarketOff) {
			// Not an outage: this deployment switched the market off.
			writeDomain(w, http.StatusNotFound, err, i18n.MsgUnavailable)
			return
		}
		writeDomain(w, http.StatusServiceUnavailable, err, i18n.MsgMarketUpstream)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"packages": list, "enabled": true})
}

func (b *Backend) handleThemeMarketInstall(w http.ResponseWriter, r *http.Request) {
	actor, ok := b.pluginSuper(w, r)
	if !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := jsonDecode(r, &body); err != nil || body.Name == "" {
		writeErrCode(w, http.StatusBadRequest, "name required", i18n.MsgMissingField)
		return
	}
	res, err := b.appearance.ThemeMarket.Install(r.Context(), body.Name)
	if err != nil {
		b.themeMarketError(r, w, err)
		return
	}
	b.Store.InsertAudit(r.Context(), actor, "theme_market_install", map[string]any{"id": res.ID, "version": res.Version})
	writeJSON(w, http.StatusOK, res)
}

func (b *Backend) handleThemeUninstall(w http.ResponseWriter, r *http.Request) {
	actor, ok := b.pluginSuper(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if err := b.appearance.ThemeMarket.Uninstall(id); err != nil {
		switch {
		case errors.Is(err, thememkt.ErrActiveTheme):
			writeDomain(w, http.StatusConflict, err, i18n.MsgThemeActive)
		case errors.Is(err, thememkt.ErrNotFound):
			writeDomain(w, http.StatusBadRequest, err, i18n.MsgThemeNotFound)
		case errors.Is(err, thememkt.ErrBadThemePkg):
			writeDomain(w, http.StatusBadRequest, err, i18n.MsgInvalidValue)
		default:
			writeLogged(r, w, http.StatusBadGateway, i18n.MsgMarketUpstream, "upstream request failed", err)
		}
		return
	}
	b.Store.InsertAudit(r.Context(), actor, "theme_uninstall", map[string]any{"id": id})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (b *Backend) handleThemeUpload(w http.ResponseWriter, r *http.Request) {
	actor, ok := b.pluginSuper(w, r)
	if !ok {
		return
	}
	svc := b.appearance.ThemeMarket
	maxUpload := int64(16 << 20)
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
		writeErrCode(w, http.StatusRequestEntityTooLarge, "theme package too large (max 16 MiB)", i18n.MsgTooLong)
		return
	}
	if r.FormValue("inspect") == "1" {
		writeJSON(w, http.StatusOK, svc.Inspect(data))
		return
	}
	res, err := svc.UploadInstall(data, r.FormValue("acceptUnverified") == "1",
		strings.TrimSpace(r.FormValue("expectDigest")))
	if err != nil {
		b.themeMarketError(r, w, err)
		return
	}
	b.Store.InsertAudit(r.Context(), actor, "theme_upload_install", map[string]any{"id": res.ID, "version": res.Version, "trust": res.Trust})
	writeJSON(w, http.StatusOK, res)
}

func (b *Backend) themeMarketError(r *http.Request, w http.ResponseWriter, err error) {
	// Each branch is a different decision for the operator: re-confirm an
	// unverified package, pick another theme, or wait for the registry.
	switch {
	case errors.Is(err, thememkt.ErrUnverified):
		writeDomain(w, http.StatusConflict, err, i18n.MsgPluginUnverified)
	case errors.Is(err, thememkt.ErrDigestStale):
		writeDomain(w, http.StatusConflict, err, i18n.MsgDigestMismatch)
	case errors.Is(err, thememkt.ErrBadThemePkg):
		writeDomain(w, http.StatusConflict, err, i18n.MsgInvalidValue)
	case errors.Is(err, thememkt.ErrNotFound):
		writeDomain(w, http.StatusConflict, err, i18n.MsgThemeNotFound)
	case errors.Is(err, thememkt.ErrMarketOff):
		writeDomain(w, http.StatusNotFound, err, i18n.MsgUnavailable)
	default:
		writeLogged(r, w, http.StatusBadGateway, i18n.MsgMarketUpstream, "upstream request failed", err)
	}
}
