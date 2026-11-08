package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"skin-nexus/server/internal/marketfmt"
	"skin-nexus/server/internal/outbound"
)

const marketCacheTTL = 10 * time.Minute

type MarketDist = marketfmt.Dist

type MarketPackage = marketfmt.Package

type MarketRegistry = marketfmt.Registry

type MarketEntry struct {
	MarketPackage
	Installed string       `json:"installed,omitempty"`
	CanUpdate bool         `json:"canUpdate,omitempty"`
	Plan      *InstallPlan `json:"plan,omitempty"`
}

func (m *Manager) fetchRegistry(ctx context.Context, force bool) (*MarketRegistry, error) {
	if m.cfg.MarketURL == "" {
		return nil, errors.New("plugin market is not configured")
	}
	if m.cfg.Keyring == nil {
		return nil, errors.New("plugin trust keys are not configured")
	}
	m.marketMu.Lock()
	if !force && m.marketCache != nil && time.Since(m.marketFetched) < marketCacheTTL {
		r := m.marketCache
		m.marketMu.Unlock()
		return r, nil
	}
	m.marketMu.Unlock()

	u, err := url.Parse(m.cfg.MarketURL)
	if err != nil {
		return nil, err
	}
	client := outbound.New([]string{u.Hostname()}, 5*time.Second, 1<<20)
	raw, err := client.Get(ctx, m.cfg.MarketURL)
	if err != nil {
		if cached := m.readMarketCache(); cached != nil {
			m.marketMu.Lock()
			m.marketCache, m.marketFetched = cached, time.Now()
			m.marketMu.Unlock()
			return cached, nil
		}
		return nil, err
	}
	reg, err := marketfmt.VerifyRegistry(raw, m.cfg.Keyring, time.Now())
	if err != nil {
		return nil, err
	}
	m.marketMu.Lock()
	m.marketCache, m.marketFetched = reg, time.Now()
	m.marketMu.Unlock()
	m.writeMarketCache(raw)
	return reg, nil
}

func (m *Manager) marketCachePath() string {
	return filepath.Join(m.stateDir(), "cache", "registry.json")
}

func (m *Manager) writeMarketCache(raw []byte) {
	path := m.marketCachePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, raw, 0o644)
}

func (m *Manager) readMarketCache() *MarketRegistry {
	raw, err := os.ReadFile(m.marketCachePath())
	if err != nil {
		return nil
	}
	reg, err := marketfmt.VerifyRegistry(raw, m.cfg.Keyring, time.Now())
	if err != nil {
		return nil
	}
	return reg
}

func (m *Manager) MarketCatalog(ctx context.Context, force bool) ([]MarketEntry, error) {
	reg, err := m.fetchRegistry(ctx, force)
	if err != nil {
		return nil, err
	}
	m.mu.RLock()
	versions := map[string]string{}
	for name, inst := range m.instances {
		versions[name] = inst.info.Version
	}
	m.mu.RUnlock()
	out := make([]MarketEntry, 0, len(reg.Packages))
	for _, p := range reg.Packages {
		e := MarketEntry{MarketPackage: p, Installed: versions[p.Name]}
		if e.Installed != "" {
			if cmp, err := marketfmt.CompareVersions(p.Version, e.Installed); err == nil {
				e.CanUpdate = cmp > 0
			} else {
				e.CanUpdate = e.Installed != p.Version
			}
		}
		e.Plan, _ = m.PlanInstall(PlanInput{
			Name: p.Name, Version: p.Version, Permissions: p.Capabilities,
			Requires: p.Requires, Conflicts: p.Conflicts,
		})
		out = append(out, e)
	}
	return out, nil
}

// MarketInstall downloads a signed artifact from the trusted registry, checks
// transport SHA-256, then hands it to the bundle installer for signature,
// staging and atomic swap.
func (m *Manager) MarketInstall(ctx context.Context, name string, consent bool) (*InstallResult, error) {
	reg, err := m.fetchRegistry(ctx, false)
	if err != nil {
		return nil, err
	}
	var pkg *MarketPackage
	for i := range reg.Packages {
		if reg.Packages[i].Name == name {
			pkg = &reg.Packages[i]
			break
		}
	}
	if pkg == nil {
		return nil, ErrPluginNotFound
	}
	if pkg.MinServer != "" && m.cfg.HostVersion != "" {
		ok, err := checkMinServer(pkg.MinServer, m.cfg.HostVersion)
		if err != nil || !ok {
			return &InstallResult{Plan: &InstallPlan{Name: pkg.Name, Version: pkg.Version, CanInstall: false, Missing: []string{"host " + m.cfg.HostVersion + " does not satisfy " + pkg.MinServer}}, State: "blocked", Source: "market"}, ErrInstallBlocked
		}
	}
	plan, err := m.PlanInstall(PlanInput{
		Name: pkg.Name, Version: pkg.Version, Permissions: pkg.Capabilities,
		Requires: pkg.Requires, Conflicts: pkg.Conflicts,
	})
	if err != nil {
		return nil, err
	}
	if !plan.CanInstall {
		return &InstallResult{Plan: plan, State: "blocked", Source: "market"}, ErrInstallBlocked
	}
	if len(plan.AddedPermissions) > 0 && !consent {
		return &InstallResult{Plan: plan, State: "consent", Source: "market"}, ErrConsentRequired
	}
	u, err := url.Parse(pkg.Dist.URL)
	if err != nil || u.Hostname() == "" {
		return nil, errors.New("market package has invalid dist URL")
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
	return m.InstallBytes(ctx, raw, InstallOptions{Consent: consent, Source: "market"})
}
