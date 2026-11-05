// appearance.go: admin surface for SEO settings, appearance (custom CSS/JS),
// and the theme pack catalog. Engines are injected from main (decoupled from
// storage details).
package adminapi

import (
	"context"
	"errors"
	"net/http"

	"skin-nexus/server/internal/appearance"
	"skin-nexus/server/internal/i18n"
	"skin-nexus/server/internal/seo"
	"skin-nexus/server/internal/theme"
	"skin-nexus/server/internal/thememkt"
)

// AppearanceDeps wires the SEO + appearance engines and the theme registry.
type AppearanceDeps struct {
	SEO    *seo.Engine
	Appear *appearance.Engine
	Themes *theme.Registry
	// ThemeMarket (nil = no market routes) backs catalog/install/upload.
	ThemeMarket *thememkt.Service
}

// MountAppearance registers the SEO/appearance/theme admin routes.
func (b *Backend) MountAppearance(deps AppearanceDeps) {
	b.appearance = &deps
}

func (b *Backend) handleSEOGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, b.appearance.SEO.Settings(r.Context()))
}

// POST /api/admin/v1/seo/oneclick — fill defaults without saving (preview).
func (b *Backend) handleSEOOneClick(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, b.appearance.SEO.FillDefaults(r.Context()))
}

func (b *Backend) handleSEOSave(w http.ResponseWriter, r *http.Request) {
	var s seo.Settings
	if err := jsonDecode(r, &s); err != nil {
		writeErrCode(w, http.StatusBadRequest, "bad body", i18n.MsgBadBody)
		return
	}
	if err := b.appearance.SEO.Save(r.Context(), s); err != nil {
		writeInternal(r, w, err)
		return
	}
	b.Store.InsertAudit(r.Context(), actorUID(r), "seo_save", nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (b *Backend) handleAppearanceGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, b.appearance.Appear.Settings(r.Context()))
}

func (b *Backend) handleAppearanceSave(w http.ResponseWriter, r *http.Request) {
	var s appearance.Settings
	if err := jsonDecode(r, &s); err != nil {
		writeErrCode(w, http.StatusBadRequest, "bad body", i18n.MsgBadBody)
		return
	}
	if err := b.appearance.Appear.Save(r.Context(), s); err != nil {
		var ve *appearance.ValidationError
		if errors.As(err, &ve) {
			// A rejected setting is the operator's mistake and the reason is
			// theirs to read; anything else is our failure and only goes to the log.
			writeDomain(w, http.StatusBadRequest, ve, i18n.MsgInvalidValue)
			return
		}
		writeInternal(r, w, err)
		return
	}
	b.Store.InsertAudit(r.Context(), actorUID(r), "appearance_save", map[string]any{
		"active_theme": s.ActiveTheme,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (b *Backend) handleThemesList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"themes": b.appearance.Themes.List()})
}

// mountThemeMarket registers the theme market routes (called from the
// appearance block in Handler once a ThemeMarket service is wired).
//
// Every one of them rides `b.auth` even though the handlers also call
// pluginSuper: that gate reads the actor out of the request context, and only
// auth puts it there. Register a mutation without it and the caller is told
// "this action needs a super admin web session" no matter who they are — the
// refusal is indistinguishable from the one a plain admin is supposed to get.
func (b *Backend) mountThemeMarket(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/v1/themes/market", b.auth(b.handleThemeMarketCatalog))
	mux.HandleFunc("POST /api/admin/v1/themes/market/install", b.auth(b.handleThemeMarketInstall))
	mux.HandleFunc("DELETE /api/admin/v1/themes/market/{id}", b.auth(b.handleThemeUninstall))
	mux.HandleFunc("POST /api/admin/v1/themes/upload", b.auth(b.handleThemeUpload))
}

var _ = context.Background
