// Package thememkt is the theme market service: a signed registry catalog,
// one-click installs from the trusted market, admin-trusted local uploads,
// and hot reload of the theme registry (no restart needed after install or
// uninstall).
//
// Trust model (2026-11-01 拍板, mirrors the plugin market but with an
// INDEPENDENT keyring so one leaked key never compromises both surfaces):
//   - market artifacts must pass registry signature, pinned-key bundle
//     signature, size + sha256 and theme.json validation before they land;
//   - local uploads run on the admin trust surface (theme JS already equals
//     custom JS in power), so an unsigned pack installs only after the
//     operator acknowledges the risk (acceptUnverified) — same two-step
//     inspect/commit flow as plugin uploads.
package thememkt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"skin-nexus/server/internal/marketfmt"
	"skin-nexus/server/internal/outbound"
	"skin-nexus/server/internal/signedpkg"
	"skin-nexus/server/internal/theme"
)

const (
	cacheTTL     = 10 * time.Minute
	defaultUpMax = 16 << 20 // theme packs are CSS/JS/fonts — 16 MiB is generous
	manifestMax  = 256 << 10
)

var (
	ErrMarketOff   = errors.New("theme market is not configured")
	ErrNotFound    = errors.New("theme package not found in market")
	ErrUnverified  = errors.New("未签名主题包需显式确认风险（acceptUnverified）")
	ErrDigestStale = errors.New("上传内容与刚才检查的包不一致，请重试")
	ErrActiveTheme = errors.New("该主题正在使用中，先切换到其它主题再卸载")
	ErrBadThemePkg = errors.New("主题包校验失败")
)

type Config struct {
	ThemesDir   string
	MarketURL   string
	TrustKeys   signedpkg.Keyring
	HostVersion string
	UploadMax   int64
	// IsActive reports whether id is the currently selected active_theme.
	IsActive func(id string) bool
}

type Service struct {
	cfg Config
	reg *theme.Registry

	mu      sync.Mutex
	cache   *marketfmt.Registry
	fetched time.Time
}

func New(cfg Config, reg *theme.Registry) *Service {
	if cfg.UploadMax <= 0 {
		cfg.UploadMax = defaultUpMax
	}
	return &Service{cfg: cfg, reg: reg}
}

func (s *Service) MarketEnabled() bool {
	return s.cfg.MarketURL != "" && len(s.cfg.TrustKeys) > 0
}

// Entry decorates a registry package with the local install state.
type Entry struct {
	marketfmt.Package
	Installed string `json:"installed,omitempty"`
	CanUpdate bool   `json:"canUpdate,omitempty"`
}

func (s *Service) Catalog(ctx context.Context, force bool) ([]Entry, error) {
	if !s.MarketEnabled() {
		return nil, ErrMarketOff
	}
	reg, err := s.fetchRegistry(ctx, force)
	if err != nil {
		return nil, err
	}
	installed := map[string]string{}
	for _, m := range s.reg.List() {
		installed[m.ID] = m.Version
	}
	out := make([]Entry, 0, len(reg.Packages))
	for _, p := range reg.Packages {
		e := Entry{Package: p, Installed: installed[p.Name]}
		if e.Installed != "" {
			if cmp, err := marketfmt.CompareVersions(p.Version, e.Installed); err == nil {
				e.CanUpdate = cmp > 0
			} else {
				e.CanUpdate = e.Installed != p.Version
			}
		}
		out = append(out, e)
	}
	return out, nil
}

func (s *Service) fetchRegistry(ctx context.Context, force bool) (*marketfmt.Registry, error) {
	s.mu.Lock()
	if !force && s.cache != nil && time.Since(s.fetched) < cacheTTL {
		r := s.cache
		s.mu.Unlock()
		return r, nil
	}
	s.mu.Unlock()

	u, err := url.Parse(s.cfg.MarketURL)
	if err != nil {
		return nil, err
	}
	client := outbound.New([]string{u.Hostname()}, 5*time.Second, 1<<20)
	raw, err := client.Get(ctx, s.cfg.MarketURL)
	if err != nil {
		if cached := s.readDiskCache(); cached != nil {
			s.mu.Lock()
			s.cache, s.fetched = cached, time.Now()
			s.mu.Unlock()
			return cached, nil
		}
		return nil, err
	}
	reg, err := marketfmt.VerifyRegistry(raw, s.cfg.TrustKeys, time.Now())
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.cache, s.fetched = reg, time.Now()
	s.mu.Unlock()
	s.writeDiskCache(raw)
	return reg, nil
}

// cachePath lives under themesDir/.market-cache — the dot-prefixed name never
// matches the theme id whitelist, so Rescan skips it as an unknown directory.
func (s *Service) cachePath() string {
	return filepath.Join(s.cfg.ThemesDir, ".market-cache", "registry.json")
}

func (s *Service) writeDiskCache(raw []byte) {
	if err := os.MkdirAll(filepath.Dir(s.cachePath()), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(s.cachePath(), raw, 0o644)
}

func (s *Service) readDiskCache() *marketfmt.Registry {
	raw, err := os.ReadFile(s.cachePath())
	if err != nil {
		return nil
	}
	reg, err := marketfmt.VerifyRegistry(raw, s.cfg.TrustKeys, time.Now())
	if err != nil {
		return nil
	}
	return reg
}

// Result reports one accepted install.
type Result struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Source  string `json:"source"`
	Trust   string `json:"trust"`
}

// Install downloads the named package from the signed registry and installs
// it (registry signature already covered the dist hash at fetch time; the
// bundle signature re-proves authorship over the bytes we are about to run).
func (s *Service) Install(ctx context.Context, name string) (*Result, error) {
	if !s.MarketEnabled() {
		return nil, ErrMarketOff
	}
	reg, err := s.fetchRegistry(ctx, false)
	if err != nil {
		return nil, err
	}
	var pkg *marketfmt.Package
	for i := range reg.Packages {
		if reg.Packages[i].Name == name {
			pkg = &reg.Packages[i]
			break
		}
	}
	if pkg == nil {
		return nil, ErrNotFound
	}
	u, err := url.Parse(pkg.Dist.URL)
	if err != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("market package has invalid dist URL")
	}
	client := outbound.New([]string{u.Hostname()}, 15*time.Second, 32<<20)
	raw, err := client.Get(ctx, pkg.Dist.URL)
	if err != nil {
		return nil, err
	}
	if pkg.Dist.Size > 0 && int64(len(raw)) != pkg.Dist.Size {
		return nil, errors.New("market artifact size mismatch")
	}
	sum := sha256.Sum256(raw)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), pkg.Dist.SHA256) {
		return nil, errors.New("market artifact sha256 mismatch")
	}
	// Bundle signature: the pinned key must have signed exactly this pack.
	dir, err := s.stageZip(raw)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	zipPath := filepath.Join(dir, "pack.zip")
	meta, err := signedpkg.VerifyBundle(zipPath, s.cfg.TrustKeys)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadThemePkg, err)
	}
	if meta.Name != pkg.Name || meta.Version != pkg.Version {
		return nil, fmt.Errorf("%w: bundle signs %s@%s, registry lists %s@%s",
			ErrBadThemePkg, meta.Name, meta.Version, pkg.Name, pkg.Version)
	}
	man, err := manifestFromZip(zipPath, pkg.Name, s.cfg.HostVersion)
	if err != nil {
		return nil, err
	}
	if err := s.installStaged(zipPath, man.ID); err != nil {
		return nil, err
	}
	return &Result{ID: man.ID, Version: man.Version, Source: "market", Trust: "verified"}, nil
}

// Inspection is the upload pre-flight report (step 1 of the two-step flow).
type Inspection struct {
	Status    string          `json:"status"` // verified | unknown-key | unsigned | invalid
	KeyID     string          `json:"keyId,omitempty"`
	Name      string          `json:"name,omitempty"`
	Version   string          `json:"version,omitempty"`
	Author    string          `json:"author,omitempty"`
	ZipSHA256 string          `json:"zipSha"`
	Theme     *theme.Manifest `json:"theme,omitempty"`
	Error     string          `json:"error,omitempty"`
}

func (s *Service) Inspect(raw []byte) *Inspection {
	insp := &Inspection{}
	sum := sha256.Sum256(raw)
	insp.ZipSHA256 = hex.EncodeToString(sum[:])
	dir, err := s.stageZip(raw)
	if err != nil {
		insp.Status, insp.Error = "invalid", err.Error()
		return insp
	}
	defer os.RemoveAll(dir)
	zipPath := filepath.Join(dir, "pack.zip")
	meta, metaErr := signedpkg.ReadBundleMeta(zipPath)
	if metaErr != nil {
		if strings.Contains(metaErr.Error(), "bundle.json missing") {
			metaErr = nil // no bundle.json → unsigned local pack
		} else {
			insp.Status, insp.Error = "invalid", metaErr.Error()
			return insp
		}
	}
	man, manErr := manifestFromZip(zipPath, "", s.cfg.HostVersion)
	if manErr != nil {
		insp.Status, insp.Error = "invalid", manErr.Error()
		return insp
	}
	insp.Theme, insp.Name, insp.Version, insp.Author = man, man.ID, man.Version, man.Author
	if metaErr == nil && meta.Signature != "" {
		insp.KeyID = meta.KeyID
		if _, err := signedpkg.VerifyBundle(zipPath, s.cfg.TrustKeys); err != nil {
			if strings.Contains(err.Error(), "unknown signing key") {
				insp.Status = "unknown-key"
			} else {
				insp.Status, insp.Error = "invalid", err.Error()
			}
		} else {
			insp.Status = "verified"
			if meta.Name != man.ID || meta.Version != man.Version {
				insp.Status, insp.Error = "invalid",
					fmt.Errorf("bundle signs %s@%s but theme.json says %s@%s", meta.Name, meta.Version, man.ID, man.Version).Error()
			}
		}
	} else {
		insp.Status = "unsigned"
	}
	return insp
}

// UploadInstall is step 2 of the local-upload flow. Unsigned packs need an
// explicit acceptUnverified; expectDigest pins the bytes the operator just
// inspected (TOCTOU guard, same contract as plugin uploads).
func (s *Service) UploadInstall(raw []byte, acceptUnverified bool, expectDigest string) (*Result, error) {
	insp := s.Inspect(raw)
	switch insp.Status {
	case "invalid", "unknown-key":
		return nil, fmt.Errorf("%w: %s", ErrBadThemePkg, insp.Error)
	case "unsigned":
		if !acceptUnverified {
			return nil, ErrUnverified
		}
	}
	if expectDigest != "" && !strings.EqualFold(expectDigest, insp.ZipSHA256) {
		return nil, ErrDigestStale
	}
	dir, err := s.stageZip(raw)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	zipPath := filepath.Join(dir, "pack.zip")
	man, err := manifestFromZip(zipPath, "", s.cfg.HostVersion)
	if err != nil {
		return nil, err
	}
	if err := s.installStaged(zipPath, man.ID); err != nil {
		return nil, err
	}
	trust := insp.Status
	if trust == "unsigned" {
		trust = "local"
	}
	return &Result{ID: man.ID, Version: man.Version, Source: "upload", Trust: trust}, nil
}

// Uninstall removes a pack directory and rescans. The active theme is
// protected (it is what every visitor is currently served).
func (s *Service) Uninstall(id string) error {
	if _, err := s.reg.Get(id); err != nil {
		return err
	}
	if s.cfg.IsActive != nil && s.cfg.IsActive(id) {
		return ErrActiveTheme
	}
	if err := os.RemoveAll(filepath.Join(s.cfg.ThemesDir, id)); err != nil {
		return err
	}
	return s.reg.Rescan()
}

// --- shared plumbing -------------------------------------------------------

// stageZip writes the bytes to a temp file and sanity-checks it opens.
func (s *Service) stageZip(raw []byte) (string, error) {
	if int64(len(raw)) > s.cfg.UploadMax {
		return "", fmt.Errorf("theme package exceeds %d bytes", s.cfg.UploadMax)
	}
	dir, err := os.MkdirTemp("", "skintheme-")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.zip"), raw, 0o600); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

// manifestFromZip reads + validates theme.json. When wantID is set the pack
// must carry exactly that id (market installs); uploads take the id from the
// manifest itself.
func manifestFromZip(zipPath, wantID, hostVer string) (*theme.Manifest, error) {
	raw, err := signedpkg.ReadEntry(zipPath, "theme.json", manifestMax)
	if err != nil {
		return nil, fmt.Errorf("%w: theme.json missing (%v)", ErrBadThemePkg, err)
	}
	var m theme.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%w: theme.json: %v", ErrBadThemePkg, err)
	}
	if wantID != "" && m.ID != wantID {
		return nil, fmt.Errorf("%w: theme.json id %q != registry name %q", ErrBadThemePkg, m.ID, wantID)
	}
	if err := theme.Validate(m, m.ID, hostVer); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadThemePkg, err)
	}
	return &m, nil
}

// installStaged extracts the validated zip into themesDir/<id> atomically:
// unpack to a sibling staging dir, move any previous version aside, rename
// the new one in, Rescan; any failure restores the previous version.
func (s *Service) installStaged(zipPath, id string) error {
	staging, err := os.MkdirTemp(s.cfg.ThemesDir, ".install-"+id+"-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	if err := signedpkg.ExtractZip(zipPath, staging); err != nil {
		return fmt.Errorf("%w: %v", ErrBadThemePkg, err)
	}
	root, err := findPackRoot(staging)
	if err != nil {
		return err
	}
	final := filepath.Join(s.cfg.ThemesDir, id)
	backup := final + ".replaced-" + time.Now().UTC().Format("20060102150405.000")
	hadOld := false
	if _, err := os.Stat(final); err == nil {
		hadOld = true
		if err := os.Rename(final, backup); err != nil {
			return err
		}
	}
	if err := os.Rename(root, final); err != nil {
		if hadOld {
			_ = os.Rename(backup, final)
			_ = s.reg.Rescan()
		}
		return err
	}
	if err := s.reg.Rescan(); err != nil {
		_ = os.RemoveAll(final)
		if hadOld {
			_ = os.Rename(backup, final)
		}
		_ = s.reg.Rescan()
		return err
	}
	if _, err := s.reg.Get(id); err != nil {
		// extracted but the registry rejected it (e.g. host version moved):
		// roll back so the operator sees the old state, not a dead pack
		_ = os.RemoveAll(final)
		if hadOld {
			_ = os.Rename(backup, final)
		}
		_ = s.reg.Rescan()
		return fmt.Errorf("%w: 解包后未通过主题注册表校验", ErrBadThemePkg)
	}
	if hadOld {
		_ = os.RemoveAll(backup)
	}
	return nil
}

// findPackRoot accepts a flat pack (theme.json at zip root) or one single
// top-level directory containing theme.json (hand-zipped folders).
func findPackRoot(dir string) (string, error) {
	if _, err := os.Stat(filepath.Join(dir, "theme.json")); err == nil {
		return dir, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".install-") {
			dirs = append(dirs, e.Name())
		}
	}
	if len(dirs) == 1 {
		root := filepath.Join(dir, dirs[0])
		if _, err := os.Stat(filepath.Join(root, "theme.json")); err == nil {
			return root, nil
		}
	}
	return "", fmt.Errorf("%w: theme.json 必须在压缩包根（或单层顶层目录）", ErrBadThemePkg)
}
