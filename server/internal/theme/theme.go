// Package theme loads SkinNexus theme packs from a themes directory
// (default data/themes/<id>/, configurable via SKIN_THEMES_DIR).
//
// Theme packs are pure CSS + optional JS + theme.json — no build step, so an
// AI (or a human) can generate one from docs/theme-spec.md alone. The active
// theme id lives in options (key "active_theme"); custom CSS/JS stored in
// options (custom_css / custom_js) are layered on top by the HTML wrapper.
//
// Security: pack ids and asset file names are whitelist-validated
// ([a-z0-9][a-z0-9_-]*, asset files also allow dots) so a malicious pack
// cannot escape its directory. Theme JS runs with the same trust level as
// admin-supplied custom JS — the admin panel states this explicitly.
package theme

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"skin-nexus/server/internal/version"
)

// Manifest is the theme.json schema (see docs/theme-spec.md).
type Manifest struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Author      string            `json:"author,omitempty"`
	Description string            `json:"description,omitempty"`
	Extends     string            `json:"extends,omitempty"`
	Inject      Inject            `json:"inject"`
	Slots       map[string]string `json:"slots,omitempty"`
	Palette     map[string]string `json:"palette,omitempty"`
	Requires    Requires          `json:"requires,omitempty"`
}

// Inject lists pack-relative files to load, in order.
type Inject struct {
	CSS []string `json:"css,omitempty"`
	JS  []string `json:"js,omitempty"`
}

// Requires gates theme loading on the SkinNexus version ("requires": {"skin_nexus": ">=0.1"}).
type Requires struct {
	SkinNexus string `json:"skin_nexus,omitempty"`
}

// Theme is a loaded pack: manifest + resolved absolute base dir.
type Theme struct {
	Manifest Manifest
	BaseDir  string

	// Token view of the pack's stylesheets, computed on first use. A Registry
	// rescan replaces the *Theme, so a pack edit is picked up with the rescan.
	tokensOnce sync.Once
	tokensCSS  string

	// Concrete-value token view for consumers that cannot evaluate CSS (mail).
	tokensMapOnce sync.Once
	tokensMap     map[string]string
}

var (
	idRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	fileRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
)

// ErrBadID / ErrNotFound signal pack validation failures.
var (
	ErrBadID    = errors.New("theme: invalid id")
	ErrNotFound = errors.New("theme: not found")
)

// Registry is the in-memory catalog of installed packs.
type Registry struct {
	dir     string
	hostVer string

	mu     sync.RWMutex
	themes map[string]*Theme
}

// NewRegistry scans dir for packs compatible with the host version
// (requires.skin_nexus, ">=x.y" form). A missing directory is not an error
// (no themes yet).
func NewRegistry(dir, hostVersion string) (*Registry, error) {
	r := &Registry{dir: dir, hostVer: hostVersion, themes: map[string]*Theme{}}
	if err := r.Rescan(); err != nil {
		return nil, err
	}
	return r, nil
}

// Dir returns the themes root directory.
func (r *Registry) Dir() string { return r.dir }

// Rescan re-reads the themes directory (called at boot; new/removed packs need
// a service restart). Every skip is logged so a broken pack is diagnosable.
func (r *Registry) Rescan() error {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		if os.IsNotExist(err) {
			r.mu.Lock()
			r.themes = map[string]*Theme{}
			r.mu.Unlock()
			return nil
		}
		return err
	}
	found := map[string]*Theme{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if !idRe.MatchString(e.Name()) {
			slog.Info("theme: skip: id must match", "name", e.Name(), "id_re", idRe)
			continue // unknown ids are skipped, never trusted
		}
		base := filepath.Join(r.dir, e.Name())
		raw, err := os.ReadFile(filepath.Join(base, "theme.json"))
		if err != nil {
			slog.Info("theme: skip: no theme.json", "name", e.Name(), "error", err)
			continue // packs without a manifest are ignored
		}
		var m Manifest
		if err := json.Unmarshal(raw, &m); err != nil {
			slog.Info("theme: skip: bad theme.json", "name", e.Name(), "error", err)
			continue
		}
		// manifest id must match the directory name (no aliasing surprises)
		if m.ID != e.Name() {
			slog.Info("theme: skip: theme.json id != directory name", "name", e.Name(), "id", m.ID)
			continue
		}
		// required fields (theme-spec §2): name, version, inject
		if m.Name == "" || m.Version == "" || (len(m.Inject.CSS) == 0 && len(m.Inject.JS) == 0) {
			slog.Info("theme: skip: name/version/inject are required", "name", e.Name())
			continue
		}
		if !satisfiesRequires(m.Requires.SkinNexus, r.hostVer) {
			slog.Info("theme: skip: requires.skin_nexus not met by host", "name", e.Name(), "skin_nexus", m.Requires.SkinNexus, "host_ver", r.hostVer)
			continue
		}
		found[m.ID] = &Theme{Manifest: m, BaseDir: base}
	}
	r.mu.Lock()
	r.themes = found
	r.mu.Unlock()
	return nil
}

// satisfiesRequires checks the only constraint form the spec defines:
// ">=x.y" (optional patch). Empty constraint passes; anything that cannot be
// parsed — the constraint, or the host version it is checked against — fails
// closed, so a typo never silently widens compatibility. Comparison itself
// lives in internal/version: this gate and the plugin/market gates must
// order the same string the same way, or a pack accepted here is rejected
// there.
func satisfiesRequires(constraint, hostVer string) bool {
	if constraint == "" {
		return true
	}
	c := strings.TrimPrefix(constraint, ">=")
	if c == constraint { // unsupported operator
		return false
	}
	host, err := version.Parse(hostVer)
	if err != nil {
		return false // a host we cannot order proves nothing
	}
	want, err := version.Parse(c)
	if err != nil {
		return false // a typo must never widen the gate
	}
	return host.Compare(want) >= 0
}

// List returns manifests sorted by id.
func (r *Registry) List() []Manifest {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Manifest, 0, len(r.themes))
	for _, t := range r.themes {
		out = append(out, t.Manifest)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Get returns one pack or ErrNotFound.
func (r *Registry) Get(id string) (*Theme, error) {
	if !idRe.MatchString(id) {
		return nil, ErrBadID
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.themes[id]
	if !ok {
		return nil, ErrNotFound
	}
	return t, nil
}

// ResolveAsset validates a pack-relative asset request and returns the
// absolute path. Anything outside the pack dir is rejected.
func (r *Registry) ResolveAsset(id, name string) (string, error) {
	t, err := r.Get(id)
	if err != nil {
		return "", err
	}
	if !fileRe.MatchString(name) {
		return "", ErrBadID
	}
	full := filepath.Join(t.BaseDir, name)
	// defense in depth: resolved path must stay inside the pack directory
	if !strings.HasPrefix(full, t.BaseDir+string(os.PathSeparator)) {
		return "", ErrBadID
	}
	if st, err := os.Stat(full); err != nil || st.IsDir() {
		return "", ErrNotFound
	}
	return full, nil
}

// HeadHTML renders the active theme's inject list as HTML tags, served at
// /theme-assets/<id>/<file> (cached for 86400s; ?v=<theme version> lets a
// version bump bust the cache).
func (t *Theme) HeadHTML() string {
	v := "?v=" + url.QueryEscape(t.Manifest.Version)
	var sb strings.Builder
	for _, f := range t.Manifest.Inject.CSS {
		if !fileRe.MatchString(f) {
			continue
		}
		sb.WriteString(`<link rel="stylesheet" href="/theme-assets/` + t.Manifest.ID + `/` + f + v + `">` + "\n")
	}
	for _, f := range t.Manifest.Inject.JS {
		if !fileRe.MatchString(f) {
			continue
		}
		// data-cfasync="false": Rocket Loader (Cloudflare) would otherwise
		// defer this script past the first paint — the theme's mode script
		// must set the light/dark class before anything is drawn.
		sb.WriteString(`<script src="/theme-assets/` + t.Manifest.ID + `/` + f + v + `" data-cfasync="false"></script>` + "\n")
	}
	return sb.String()
}
