package plugin

import (
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const maxAssetBytes = 8 << 20

// AssetHandler serves /plugin-assets/<name>/<path> for enabled plugins with a
// declared ui.assets root. It rejects traversal, symlinks, directories, and
// oversized files; only regular files inside the declared root are reachable.
func (m *Manager) AssetHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, "/plugin-assets/")
		name, rel, _ := strings.Cut(rest, "/")
		if name == "" || rel == "" {
			http.NotFound(w, r)
			return
		}
		m.mu.RLock()
		inst, ok := m.instances[name]
		enabled := ok && inst.enabled && inst.info.UI != nil && inst.info.UI.Assets != ""
		var root, assets string
		if enabled {
			root = inst.info.Dir
			assets = inst.info.UI.Assets
		}
		m.mu.RUnlock()
		if !enabled {
			http.NotFound(w, r)
			return
		}
		clean := path.Clean("/" + rel)
		if clean == "/" || hasDotDotSegment(clean) || strings.Contains(clean, "\\") {
			http.NotFound(w, r)
			return
		}
		base := filepath.Join(root, filepath.FromSlash(assets))
		full := filepath.Join(base, filepath.FromSlash(strings.TrimPrefix(clean, "/")))
		if relPath, err := filepath.Rel(base, full); err != nil || relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
			http.NotFound(w, r)
			return
		}
		// Resolve symlinks before the containment check: a symlinked
		// directory inside the assets root must not escape it.
		realBase, err := filepath.EvalSymlinks(base)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		realFull, err := filepath.EvalSymlinks(full)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if relPath, err := filepath.Rel(realBase, realFull); err != nil || relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
			http.NotFound(w, r)
			return
		}
		lst, err := os.Lstat(full)
		if err != nil || !lst.Mode().IsRegular() || lst.Mode()&os.ModeSymlink != 0 || lst.Size() > maxAssetBytes {
			http.NotFound(w, r)
			return
		}
		f, err := os.Open(full)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		ctype := mime.TypeByExtension(filepath.Ext(full))
		if ctype == "" {
			ctype = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// Plugin assets are mutable (a plugin upgrade replaces the directory)
		// and are not content-hashed; always revalidate instead of a 1h stale
		// window. Iframe URLs also carry ?v=<plugin version>.
		w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
		http.ServeContent(w, r, filepath.Base(full), time.Time{}, f)
	})
}
