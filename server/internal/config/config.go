// Package config loads SkinNexus runtime configuration from environment
// variables (12-factor, .env-friendly for container / hosting-panel setups).
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Listen      string // e.g. ":8080"
	BaseURL     string // e.g. https://skin.example.com (used for texture URLs / ALI)
	Environment string // SKIN_ENV — free-form deployment label (production/demo/...)
	DatabaseDSN string // user:pass@tcp(host:3306)/skin-nexus?parseTime=true&charset=utf8mb4

	// SMTP is the password-recovery mailer's env fallback. Read once, here: the
	// reset mailer and the settings panel's placeholders used to build this map
	// separately from the same five variables, so they could drift apart.
	SMTP SMTPConfig

	TexturesDir string

	// Yggdrasil metadata
	ServerName  string
	SkinDomains []string // comma-separated

	SignKeyPath string // RSA private key PEM (generated on first boot)

	SiteName string

	// SetupToken guards the install wizard during the window when the site has no
	// accounts yet. Empty at boot does not mean "no token": main generates one for
	// that boot and prints it to the log.
	SetupToken string // SKIN_SETUP_TOKEN

	// TrustedProxies narrows which peers may speak X-Forwarded-For. Empty keeps
	// the historical default (loopback or RFC1918), so an existing deployment is
	// unaffected until it opts in.
	TrustedProxies []string // SKIN_TRUSTED_PROXIES

	// Phase 2: yggc/OIDC passwordless login (internal OIDC provider).
	OIDCEnabled    bool
	OIDCIssuer     string // e.g. https://skin.example.com
	OIDCSignKey    string // OIDC token signing key PEM — MUST differ from SignKeyPath
	SharedClientID string // discovery shared_client_id (launcher public client)

	// Join gate on email_verified (fork's require_verification setting).
	// Default off until the verification mail flow ships (P1).
	RequireVerification bool // SKIN_REQUIRE_VERIFICATION
	// RateLimitAuth = auth requests per IP per 10-minute sliding window
	// (0 disables the window layer). LoginFailLimit = consecutive failures
	// per identifier before a 15-minute lockout (0 disables). Both tunable
	// from the admin settings panel (boot-read with env fallback).
	// Default 120 (chain-trace P1 2026-10-23): 20 shared/NAT exits tripped
	// within minutes — register + web login + ygg auth + refresh share one
	// bucket per IP, so shared exits got collective 429s.
	RateLimitAuth  int // SKIN_RATE_LIMIT_AUTH (default 120)
	LoginFailLimit int // SKIN_LOGIN_FAIL_LIMIT (default 5)

	// Registration requires a valid invitation code (live plugin's mandatory
	// semantics). Default off = optional code, validated and burned when given.
	RequireInvite bool // SKIN_INVITE_REQUIRED

	// Phase 2: MUA Union member module (off by default; zero surface when off).
	UnionEnabled           bool
	UnionAPIRoot           string
	UnionMemberKey         string
	UnionHostPublicKeyPath string
	UnionAllowUpdateBkKey  bool
	UnionQueryEmailMode    string // "full" | "off"
	UnionSigPrivateKey     string // Union OAuth2 sig private key PEM path
	UnionHubOrigin         string

	// Phase 3 (docker-first): unified data root + schema bootstrap.
	DataDir         string // SKIN_DATA_DIR — textures/keys/themes live under it
	ThemesDir       string // SKIN_THEMES_DIR — theme packs (default <DataDir>/themes)
	PluginsDir      string // SKIN_PLUGINS_DIR — plugin packs (default <DataDir>/plugins)
	PluginStateDir  string // SKIN_PLUGIN_STATE_DIR — default <PluginsDir>/.state
	PluginMarketURL string // SKIN_PLUGIN_MARKET_URL — default official registry (explicit empty = market off)
	PluginTrustKeys string // SKIN_PLUGIN_TRUST_KEYS — id:base64pub,... (default the official keyring)
	PluginUploadMax int64  // SKIN_PLUGIN_UPLOAD_MAX — bytes (default 64 MiB)
	// Theme market shares the distribution repo but an INDEPENDENT keyring —
	// one leaked key must never compromise both surfaces (2026-11-01 拍板).
	ThemeMarketURL string // SKIN_THEME_MARKET_URL — default official theme registry (explicit empty = off)
	ThemeTrustKeys string // SKIN_THEME_TRUST_KEYS — id:base64pub,... (default official; empty = market installs off, uploads stay available)
	AutoMigrate    bool   // SKIN_AUTO_MIGRATE — apply migrations/ on empty DB at boot
	MigrationsDir  string // SKIN_MIGRATIONS_DIR — where *.sql live (default ./migrations)
}

// resolveDataPath picks an explicit env value when set, else joins the data
// root with rel — so a single SKIN_DATA_DIR volume holds everything while
// legacy per-path env vars keep working (explicit always wins).
func resolveDataPath(explicitEnv string, dataDir, rel string) string {
	if v := os.Getenv(explicitEnv); v != "" {
		return v
	}
	return filepath.Join(dataDir, rel)
}

// OfficialPluginMarketURL is the signed registry published by the upstream
// plugin repository. Downstream deployments override SKIN_PLUGIN_MARKET_URL
// (including setting it empty to disable the market).
const OfficialPluginMarketURL = "https://yimingzhanshen.github.io/skin-nexus-plugins/v1/registry.json"

// pluginMarketURL distinguishes "unset" (use the official registry) from an
// explicitly empty value (market disabled).
func pluginMarketURL() string {
	if v, ok := os.LookupEnv("SKIN_PLUGIN_MARKET_URL"); ok {
		return v
	}
	return OfficialPluginMarketURL
}

// OfficialThemeMarketURL is the theme registry published beside the plugin
// one (same distribution repo, themes/v1 path). SKIN_THEME_MARKET_URL
// overrides, including setting it empty to disable market browsing.
const OfficialThemeMarketURL = "https://yimingzhanshen.github.io/skin-nexus-plugins/themes/v1/registry.json"

func themeMarketURL() string {
	if v, ok := os.LookupEnv("SKIN_THEME_MARKET_URL"); ok {
		return v
	}
	return OfficialThemeMarketURL
}

// Official keyrings, compiled in beside the registry URLs so both markets work
// out of the box (a bare `docker run` or `go run` needs no extra env). Trusting
// these keys only means "artifacts signed by upstream are installable"; an
// explicitly empty SKIN_*_TRUST_KEYS still switches the surface off, and forks
// point both vars at their own registry and key.
const (
	OfficialPluginTrustKeys = "market-2026-10:ID2EWgcfs1E4UbGCCX9WjD0aCgTof3oeuHV03IpdGNY="
	OfficialThemeTrustKeys  = "theme-market-2026-11:EXxNehbsxNG9gdYUztMl/EU/lZ09+fPftPvmjeqPr/I="
)

func pluginTrustKeys() string {
	if v, ok := os.LookupEnv("SKIN_PLUGIN_TRUST_KEYS"); ok {
		return v
	}
	return OfficialPluginTrustKeys
}

func themeTrustKeys() string {
	if v, ok := os.LookupEnv("SKIN_THEME_TRUST_KEYS"); ok {
		return v
	}
	return OfficialThemeTrustKeys
}

func Load() (*Config, error) {
	dataDir := getenv("SKIN_DATA_DIR", "./data")
	c := &Config{
		Listen:         getenv("SKIN_LISTEN", ":8080"),
		BaseURL:        strings.TrimRight(getenv("SKIN_BASE_URL", "http://localhost:8080"), "/"),
		Environment:    getenv("SKIN_ENV", "production"),
		DatabaseDSN:    os.Getenv("SKIN_DATABASE_DSN"),
		SMTP:           loadSMTP(),
		TexturesDir:    resolveDataPath("SKIN_TEXTURES_DIR", dataDir, "textures"),
		ServerName:     getenv("SKIN_SERVER_NAME", "SkinNexus"),
		SkinDomains:    splitCSV(getenv("SKIN_SKIN_DOMAINS", "")),
		SignKeyPath:    resolveDataPath("SKIN_SIGN_KEY", dataDir, "sign-key.pem"),
		SiteName:       getenv("SKIN_SITE_NAME", "SkinNexus"),
		SetupToken:     os.Getenv("SKIN_SETUP_TOKEN"),
		TrustedProxies: splitCSV(getenv("SKIN_TRUSTED_PROXIES", "")),

		OIDCEnabled:         getenv("SKIN_OIDC_ENABLED", "false") == "true",
		RequireVerification: getenv("SKIN_REQUIRE_VERIFICATION", "false") == "true",
		RequireInvite:       getenv("SKIN_INVITE_REQUIRED", "false") == "true",
		RateLimitAuth:       atoiDefault(getenv("SKIN_RATE_LIMIT_AUTH", "120"), 120),
		LoginFailLimit:      atoiDefault(getenv("SKIN_LOGIN_FAIL_LIMIT", "5"), 5),
		OIDCIssuer:          strings.TrimRight(getenv("SKIN_OIDC_ISSUER", strings.TrimRight(getenv("SKIN_BASE_URL", "http://localhost:8080"), "/")), "/"),
		OIDCSignKey:         resolveDataPath("SKIN_OIDC_SIGN_KEY", dataDir, "oidc-key.pem"),
		SharedClientID:      getenv("SKIN_OIDC_SHARED_CLIENT_ID", "2"),

		UnionEnabled:           getenv("SKIN_UNION_ENABLED", "false") == "true",
		UnionAPIRoot:           getenv("SKIN_UNION_API_ROOT", "https://skin.mualliance.ltd/api/union"),
		UnionMemberKey:         os.Getenv("SKIN_UNION_MEMBER_KEY"),
		UnionHostPublicKeyPath: resolveDataPath("SKIN_UNION_HOST_PUBKEY", dataDir, "union-host-pubkey.pem"),
		UnionAllowUpdateBkKey:  getenv("SKIN_UNION_ALLOW_UPDATEBACKENDKEY", "false") == "true",
		UnionQueryEmailMode:    getenv("SKIN_UNION_QUERYEMAIL_MODE", "full"),
		UnionSigPrivateKey:     resolveDataPath("SKIN_UNION_OAUTH2_SIG_KEY", dataDir, "union-oauth2-sig.pem"),
		UnionHubOrigin:         getenv("SKIN_UNION_HUB_ORIGIN", "https://skin.mualliance.ltd"),

		DataDir:         dataDir,
		ThemesDir:       getenv("SKIN_THEMES_DIR", filepath.Join(dataDir, "themes")),
		PluginsDir:      resolveDataPath("SKIN_PLUGINS_DIR", dataDir, "plugins"),
		PluginStateDir:  getenv("SKIN_PLUGIN_STATE_DIR", ""),
		PluginMarketURL: pluginMarketURL(),
		PluginTrustKeys: pluginTrustKeys(),
		PluginUploadMax: int64(atoiDefault(getenv("SKIN_PLUGIN_UPLOAD_MAX", "67108864"), 67108864)),
		ThemeMarketURL:  themeMarketURL(),
		ThemeTrustKeys:  themeTrustKeys(),
		AutoMigrate:     getenv("SKIN_AUTO_MIGRATE", "false") == "true",
		MigrationsDir:   getenv("SKIN_MIGRATIONS_DIR", "./migrations"),
	}
	if c.PluginStateDir == "" {
		c.PluginStateDir = filepath.Join(c.PluginsDir, ".state")
	}
	if c.PluginUploadMax <= 0 {
		c.PluginUploadMax = 64 << 20
	}
	if c.DatabaseDSN == "" {
		return nil, fmt.Errorf("config: SKIN_DATABASE_DSN is required")
	}
	return c, nil
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func atoiDefault(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}

func splitCSV(v string) []string {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Port extracts the numeric port from Listen for logging.
func (c *Config) Port() int {
	i := strings.LastIndex(c.Listen, ":")
	if i < 0 {
		return 8080
	}
	p, err := strconv.Atoi(c.Listen[i+1:])
	if err != nil {
		return 8080
	}
	return p
}

// SMTPConfig is the five mail variables grouped as one value.
type SMTPConfig struct {
	Host string
	Port string
	User string
	Pass string
	From string
}

func loadSMTP() SMTPConfig {
	return SMTPConfig{
		Host: os.Getenv("SKIN_SMTP_HOST"),
		Port: getenv("SKIN_SMTP_PORT", "465"),
		User: os.Getenv("SKIN_SMTP_USER"),
		Pass: os.Getenv("SKIN_SMTP_PASS"),
		From: os.Getenv("SKIN_SMTP_FROM"),
	}
}

// Map keys the five variables by their options-panel names, so the panel's
// option keys and the env fallbacks cannot drift into two vocabularies.
func (s SMTPConfig) Map() map[string]string {
	return map[string]string{
		"smtp_host": s.Host,
		"smtp_port": s.Port,
		"smtp_user": s.User,
		"smtp_pass": s.Pass,
		"smtp_from": s.From,
	}
}

// PanelMap is Map() with the password removed. The settings endpoint serializes
// this map, and a credential is not a placeholder — it is a secret.
func (s SMTPConfig) PanelMap() map[string]string {
	m := s.Map()
	m["smtp_pass"] = ""
	return m
}
