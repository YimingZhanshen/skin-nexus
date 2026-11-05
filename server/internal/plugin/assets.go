package plugin

import (
	"context"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const maxAssetBytes = 8 << 20

// maxInjectBytes bounds rewriting an HTML document. Plugin pages are small; the
// ceiling exists so a giant file cannot be buffered because someone asked for a
// theme, and beyond it the file is served untouched (the bridge still themes it
// late, which is what happened before this existed).
const maxInjectBytes = 1 << 20

// ThemeHead returns the markup the host wants inside a plugin document's
// <head> — normally the active theme's <link>. nil, or an empty result, means
// "no theme": the plugin's own defaults are then the site's appearance and the
// file must not be touched.
type ThemeHead func(ctx context.Context) string

// pluginThemeMap maps the site's theme tokens onto the plugin contract's
// variables. It lands after the plugin's own `:root` block, which is the whole
// point: at equal specificity the later declaration wins, so the first paint is
// themed instead of waiting for the `ready` → `init` round trip. The fallbacks
// are the contract's defaults and are pinned against the host's own copy by
// TestPluginThemeMapMatchesTheHostBridge.
const pluginThemeMap = `<style id="skin-plugin-theme">:root{` +
	`--plugin-bg:var(--bg,#101825);` +
	`--plugin-panel:var(--panel,#16202e);` +
	`--plugin-border:var(--border,rgba(169,138,95,.24));` +
	`--plugin-text:var(--text,#e9edf1);` +
	`--plugin-muted:var(--muted,#a8b4c3);` +
	`--plugin-accent:var(--accent,#b0453a);` +
	`--plugin-link:var(--jade,#67b7a0);` +
	`--plugin-danger:var(--danger,#d9564c);` +
	`--plugin-ok:var(--ok,#6fbf7d);` +
	`}</style>` + "\n"

// injectHead puts frag at the end of the document's head. Last `</head>` is the
// anchor on purpose: a plugin may carry its own <style> blocks, and after them
// is the only place the theme reliably wins.
func injectHead(doc, frag string) (string, bool) {
	lower := strings.ToLower(doc)
	if i := strings.LastIndex(lower, "</head>"); i > 0 {
		return doc[:i] + frag + doc[i:], true
	}
	if i := strings.Index(lower, "<head"); i >= 0 {
		if j := strings.Index(doc[i:], ">"); j > 0 {
			at := i + j + 1
			return doc[:at] + "\n" + frag + doc[at:], true
		}
	}
	return doc, false
}

// AssetHandler serves /plugin-assets/<name>/<path> for enabled plugins with a
// declared ui.assets root. It rejects traversal, symlinks, directories, and
// oversized files; only regular files inside the declared root are reachable.
// HTML documents additionally receive the active theme in <head> (see ThemeHead).
func (m *Manager) AssetHandler(theme ThemeHead) http.Handler {
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
		// Control characters and NUL have no business in an asset name and can
		// confuse a downstream path consumer; unicode is fine (plugin authors may
		// ship non-ASCII filenames).
		if clean == "/" || hasDotDotSegment(clean) || strings.Contains(clean, "\\") ||
			strings.ContainsFunc(clean, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
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
		// Plugin UI pages are HTML on the site origin. Inside the panel they sit in
		// a sandboxed iframe; reached directly they would run with the viewer's
		// cookies, so the same flags travel with the response.
		isHTML := strings.HasPrefix(ctype, "text/html")
		if isHTML {
			w.Header().Set("Sandbox", "allow-scripts allow-forms")
		}
		// Plugin assets are mutable (a plugin upgrade replaces the directory)
		// and are not content-hashed; always revalidate instead of a 1h stale
		// window. Iframe URLs also carry ?v=<plugin version>. Revalidating is
		// also what makes a theme switch take effect on the next load.
		w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
		if frag, ok := themeFragment(theme, r, lst.Size(), isHTML); ok {
			data, err := io.ReadAll(f)
			if err == nil {
				if out, done := injectHead(string(data), frag); done {
					w.Header().Set("Content-Length", strconv.Itoa(len(out)))
					_, _ = w.Write([]byte(out))
					return
				}
			}
			// Unreadable or head-less: fall through and serve what is on disk.
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				http.Error(w, "asset unreadable", http.StatusInternalServerError)
				return
			}
		}
		http.ServeContent(w, r, filepath.Base(full), time.Time{}, f)
	})
}

// themeFragment decides whether this response gets the head injection. Keeping
// the four conditions in one place is what makes "no rewrite" auditable: not a
// document, no injector wired, no theme active, or too big to buffer.
func themeFragment(theme ThemeHead, r *http.Request, size int64, isHTML bool) (string, bool) {
	if theme == nil || !isHTML || size > maxInjectBytes {
		return "", false
	}
	head := strings.TrimSpace(theme(r.Context()))
	if head == "" {
		return "", false
	}
	return head + pluginThemeMap, true
}
