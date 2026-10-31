// appearance.go: admin surface for SEO settings, appearance (custom CSS/JS),
// and the theme pack catalog. Engines are injected from main (decoupled from
// storage details).
package adminapi

import (
	"context"
	"errors"
	"net/http"

	"skin-nexus/server/internal/appearance"
	"skin-nexus/server/internal/seo"
	"skin-nexus/server/internal/theme"
)

// AppearanceDeps wires the SEO + appearance engines and the theme registry.
type AppearanceDeps struct {
	SEO    *seo.Engine
	Appear *appearance.Engine
	Themes *theme.Registry
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
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad body"})
		return
	}
	if err := b.appearance.SEO.Save(r.Context(), s); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
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
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad body"})
		return
	}
	if err := b.appearance.Appear.Save(r.Context(), s); err != nil {
		status := http.StatusInternalServerError
		var ve *appearance.ValidationError
		if errors.As(err, &ve) {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
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

var _ = context.Background
