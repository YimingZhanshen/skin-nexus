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

	"skin-nexus/server/internal/thememkt"
)

func (b *Backend) handleThemeMarketCatalog(w http.ResponseWriter, r *http.Request) {
	list, err := b.appearance.ThemeMarket.Catalog(r.Context(), r.URL.Query().Get("refresh") == "1")
	if err != nil {
		status := http.StatusServiceUnavailable
		if errors.Is(err, thememkt.ErrMarketOff) {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]any{"error": err.Error()})
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
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name required"})
		return
	}
	res, err := b.appearance.ThemeMarket.Install(r.Context(), body.Name)
	if err != nil {
		b.themeMarketError(w, err)
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
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		case errors.Is(err, thememkt.ErrNotFound), errors.Is(err, thememkt.ErrBadThemePkg):
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		default:
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
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
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "multipart parse failed"})
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "file required"})
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxUpload+1))
	if err != nil || int64(len(data)) > maxUpload {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": "theme package too large (max 16 MiB)"})
		return
	}
	if r.FormValue("inspect") == "1" {
		writeJSON(w, http.StatusOK, svc.Inspect(data))
		return
	}
	res, err := svc.UploadInstall(data, r.FormValue("acceptUnverified") == "1",
		strings.TrimSpace(r.FormValue("expectDigest")))
	if err != nil {
		b.themeMarketError(w, err)
		return
	}
	b.Store.InsertAudit(r.Context(), actor, "theme_upload_install", map[string]any{"id": res.ID, "version": res.Version, "trust": res.Trust})
	writeJSON(w, http.StatusOK, res)
}

func (b *Backend) themeMarketError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, thememkt.ErrUnverified), errors.Is(err, thememkt.ErrDigestStale),
		errors.Is(err, thememkt.ErrBadThemePkg), errors.Is(err, thememkt.ErrNotFound):
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
	case errors.Is(err, thememkt.ErrMarketOff):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
	default:
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
	}
}
