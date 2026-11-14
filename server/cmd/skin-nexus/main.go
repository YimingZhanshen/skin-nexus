// Command skin-nexus is the SkinNexus server.
//
// Routes:
//   - /api/yggdrasil/*        Yggdrasil API (legacy-compatible surface)
//   - /api/v1/*               SPA backend (webapi)
//   - /api/public/v1/*        public read-only API (S8)
//   - /textures/{hash}        texture file serving (image/png, immutable)
//   - /skin/{player}.png etc. legacy-api compatibility (S8)
//   - /*                      SPA static assets (embedded later; falls back to web/dist)
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	"skin-nexus/server/internal/accesslog"
	"skin-nexus/server/internal/account"
	"skin-nexus/server/internal/adminapi"
	"skin-nexus/server/internal/appearance"
	"skin-nexus/server/internal/auth"
	"skin-nexus/server/internal/avatargen"
	"skin-nexus/server/internal/config"
	"skin-nexus/server/internal/jobs"
	"skin-nexus/server/internal/logx"
	"skin-nexus/server/internal/metrics"
	"skin-nexus/server/internal/migrate"
	"skin-nexus/server/internal/oidc"
	"skin-nexus/server/internal/plugin"
	"skin-nexus/server/internal/publicapi"
	"skin-nexus/server/internal/ratelimit"
	"skin-nexus/server/internal/seo"
	"skin-nexus/server/internal/signedpkg"
	"skin-nexus/server/internal/siteurl"
	"skin-nexus/server/internal/store"
	"skin-nexus/server/internal/texture"
	"skin-nexus/server/internal/theme"
	"skin-nexus/server/internal/thememkt"
	"skin-nexus/server/internal/union"
	"skin-nexus/server/internal/webapi"
	"skin-nexus/server/internal/webi18n"
	"skin-nexus/server/internal/ygg"
)

var (
	version     = "0.6.0"
	commit      = ""
	buildTime   = ""
	buildBranch = ""
)

// buildVersionInfo: ldflags values win; local go builds fill the commit from
// the VCS stamp when present. Branch is only available from CI build args.
func buildVersionInfo() (string, string, string) {
	c, t, b := commit, buildTime, buildBranch
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				if c == "" {
					c = s.Value
				}
			case "vcs.time":
				if t == "" {
					t = s.Value
				}
			}
		}
	}
	if c == "" {
		c = "unknown"
	}
	if t == "" {
		t = "unknown"
	}
	if b == "" {
		b = "unknown"
	}
	return c, t, b
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println("skin-nexus", version)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		if err := migrate.RunCLI(os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "migrate:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "redetect-models" {
		if err := runRedetectModels(os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "redetect-models:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "fsck" {
		if err := runFSCK(os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "fsck:", err)
			os.Exit(1)
		}
		return
	}

	cfg, err := config.Load()
	if err != nil {
		logx.Fatal("config", "error", err)
	}
	// A typo'd SKIN_* variable is otherwise invisible: the site starts, uses the
	// default, and the operator believes they configured it.
	config.WarnUnknownEnv()
	// Install the env-derived base first so nothing ever builds a URL against an
	// empty address; the option override below replaces it before the listener
	// opens. Both writes happen on the boot goroutine.
	siteurl.Set(cfg.BaseURL)
	startedAt := time.Now()

	// Legacy verifier: built from optional legacy .env (SKIN_LEGACY_ENV).
	var legacy *auth.LegacyVerifier
	if envPath := os.Getenv("SKIN_LEGACY_ENV"); envPath != "" {
		lenv, err := migrate.ParseLegacyEnv(envPath)
		if err != nil {
			logx.Fatal("legacy env", "error", err)
		}
		legacy = auth.NewLegacyVerifier(lenv.MethodSalts())
		slog.Info("legacy verifier: methods loaded", "count", len(lenv.PWDMethods))
	}

	// The engine comes from the DSN (a `sqlite:` or `postgres://` prefix); a bare
	// MySQL DSN stays MySQL, which is what every existing deployment runs on.
	st, err := store.Open("", cfg.DatabaseDSN, legacy)
	if err != nil {
		logx.Fatal("store", "error", err)
	}
	defer st.Close()

	// Phase 3 / plan option A (2026-10-23): tracked migrations. Runs on every
	// SKIN_AUTO_MIGRATE boot — new files reach EXISTING databases (the
	// empty-DB-only path left 0006 unapplied on the live test instance);
	// schema_migrations markers skip completed files, duplicate-object
	// collisions from pre-tracking databases are tolerated, all else fatal.
	if cfg.AutoMigrate {
		if err := migrate.ApplySQLDirTracked(st.DB(), cfg.MigrationsDir, st.Engine()); err != nil {
			logx.Fatal("schema", "error", err)
		}
		// Databases built before the DDL declared AUTOINCREMENT still hand a freed
		// rowid to the next insert, and reports and tombstones cite those ids.
		if err := migrate.EnsureNoRowIDReuse(st.DB(), st.Engine()); err != nil {
			logx.Fatal("rowid", "error", err)
		}
	}
	// Session and launcher tokens are stored as digests. Runs on every boot, not
	// only when SKIN_AUTO_MIGRATE is on: it rewrites rows, not schema. The rewrite
	// and its marker share one transaction, so a failure here leaves the database
	// exactly as it was (still plaintext, still working) and the next boot retries
	// — which is why this logs rather than killing the process.
	if n, err := st.EnsureTokenDigests(context.Background()); err != nil {
		slog.Warn("auth: WARNING token digests not applied, session and launcher tokens remain in plaintext", "error", err)
	} else if n > 0 {
		slog.Info("auth: rewrote stored token(s) as digests (this happens once)", "count", n)
	}
	if n, err := st.BackfillPublicDedupe(context.Background()); err != nil {
		slog.Info("dedupe: backfill public hashes", "error", err)
	} else if n > 0 {
		slog.Info("dedupe: backfilled public texture hashes", "count", n)
	}
	if uid, changed, err := st.EnsureSuperAdmin(context.Background()); err != nil {
		slog.Info("auth: ensure super admin", "error", err)
	} else if changed {
		slog.Info("auth: promoted bootstrap admin to super admin", "uid", uid)
	}

	// Setup wizard writes site_name/skin_domains to options; they override the
	// env defaults here (explicit env SKIN_SKIN_DOMAINS/SKIN_SITE_NAME wins).
	if os.Getenv("SKIN_SITE_NAME") == "" {
		if v, ok, err := st.GetOption(context.Background(), "site_name"); err == nil && ok && v != "" {
			cfg.SiteName = v
			cfg.ServerName = v
		}
	}
	// review P1 (2026-10-23): server_name/base_url options were write-only —
	// the settings panel / setup wizard wrote them and nothing read them.
	if os.Getenv("SKIN_SERVER_NAME") == "" {
		if v, ok, err := st.GetOption(context.Background(), "server_name"); err == nil && ok && v != "" {
			cfg.ServerName = v
		}
	}
	// Site URL (拍板 2026-10-24 dev-plan §9): option `site_url`（blessing
	// 原名，迁移导入值直接生效）wins over env —— env 只是首启默认。legacy
	// `base_url` option（B4 时代写入）作为过渡回退，两键皆空才用 env。
	cfg.BaseURL = resolveBaseURL(func(key string) (string, bool) {
		v, ok, err := st.GetOption(context.Background(), key)
		return v, err == nil && ok
	}, cfg.BaseURL)
	// The one authoritative write. Everything that hands out an absolute URL —
	// the store's texture paths, the account service's views, the signed
	// Yggdrasil profile — reads it from internal/siteurl at build time, so
	// changing site_url in the panel and restarting moves all of them together.
	siteurl.Set(cfg.BaseURL)

	// CDN client-IP policy: option-first, hot-applied from the settings panel.
	// Headers are only trusted when the direct peer is loopback/RFC1918.
	applyCDN := func() {
		enabled := false
		if v, ok, err := st.GetOption(context.Background(), "cdn_enabled"); err == nil && ok {
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "1", "true", "on", "yes":
				enabled = true
			}
		}
		headers := ygg.DefaultCDNHeaders()
		if v, ok, err := st.GetOption(context.Background(), "cdn_headers"); err == nil && ok && strings.TrimSpace(v) != "" {
			headers = strings.Split(v, ",")
		}
		ygg.ConfigureClientIP(enabled, headers)
	}
	applyCDN()
	if os.Getenv("SKIN_SKIN_DOMAINS") == "" {
		if v, ok, err := st.GetOption(context.Background(), "skin_domains"); err == nil && ok && v != "" {
			for _, d := range strings.Split(v, ",") {
				if d = strings.TrimSpace(d); d != "" {
					cfg.SkinDomains = append(cfg.SkinDomains, d)
				}
			}
		}
	}

	ts, err := texture.NewStore(cfg.TexturesDir)
	if err != nil {
		logx.Fatal("textures", "error", err)
	}

	// Phase 3: SEO + appearance (theme packs, custom CSS/JS).
	seoEng := seo.NewEngine(st, seo.Defaults{SiteName: cfg.SiteName, BaseURL: cfg.BaseURL})
	themeReg, err := theme.NewRegistry(cfg.ThemesDir, version)
	if err != nil {
		logx.Fatal("themes", "error", err)
	}
	if n := len(themeReg.List()); n > 0 {
		slog.Info("themes: packs loaded from", "n", n, "themes_dir", cfg.ThemesDir)
	}
	appearEng := appearance.NewEngine(st, themeReg)
	// Mail palette: closures over the appearance engine, same shape as the
	// IsActive closure below. Any failure returns nil so a letter falls back to
	// the constant palette — reading a theme must never block a send.
	mailPalette := func() map[string]string {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		id := appearEng.Settings(ctx).ActiveTheme
		if id == "" {
			return nil
		}
		th, terr := themeReg.Get(id)
		if terr != nil {
			return nil
		}
		return th.TokenMap()
	}
	mailThemeID := func() string {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return appearEng.Settings(ctx).ActiveTheme
	}
	// Theme market: independent keyring (SKIN_THEME_TRUST_KEYS) — uploads run
	// on the admin trust surface even with the market disabled (2026-11-01).
	var themeTrustKeys signedpkg.Keyring
	if cfg.ThemeTrustKeys != "" {
		if keys, err := signedpkg.ParseKeys(cfg.ThemeTrustKeys); err != nil {
			slog.Info("theme market: bad SKIN_THEME_TRUST_KEYS () — market installs disabled", "error", err)
		} else {
			themeTrustKeys = keys
		}
	}
	themeMarketSvc := thememkt.New(thememkt.Config{
		ThemesDir:   cfg.ThemesDir,
		MarketURL:   cfg.ThemeMarketURL,
		TrustKeys:   themeTrustKeys,
		HostVersion: version,
		IsActive: func(id string) bool {
			return appearEng.Settings(context.Background()).ActiveTheme == id
		},
	}, themeReg)
	slog.Info("theme market", "market", cfg.ThemeMarketURL, "trustKeys", len(themeTrustKeys), "themesDir", cfg.ThemesDir)

	accessLog := accesslog.New(st.InsertAccessLog)
	// Diagnostic mode (2026-11-05 拍板): the same switch the panel API writes.
	// While `access_detail_until` is in the future, each logged request also
	// gets an access_log_details row (headers/body excerpt/credential, see
	// internal/accesslog/detail.go). The option is re-read per logged request
	// and the clock is Go-side, so a super admin closing the window takes effect
	// immediately and without a restart. Unwired = permanently closed.
	detailDiag := &accesslog.Diagnostic{Option: st.GetOption, Now: time.Now}
	accessLog.SetDiagnostic(detailDiag)

	// Plugin host: scans <data>/plugins; invalid directories are
	// recorded as errored entries and never abort boot. Hooks are fail-open
	// so a crashing plugin cannot alter the frozen Yggdrasil wire.
	var pluginSecrets *plugin.SecretBox
	if box, err := plugin.NewSecretBox(os.Getenv("SKIN_PLUGIN_SECRET_KEY"), filepath.Join(filepath.Dir(cfg.PluginsDir), "plugin-secrets.key")); err != nil {
		slog.Info("plugins: secret box disabled", "error", err)
	} else {
		pluginSecrets = box
	}
	var trustKeys signedpkg.Keyring
	if cfg.PluginTrustKeys != "" {
		if keys, err := signedpkg.ParseKeys(cfg.PluginTrustKeys); err != nil {
			slog.Warn("plugins: trust keys invalid", "error", err)
		} else {
			trustKeys = keys
		}
	}
	pluginHost := plugin.NewHost(plugin.RuntimeConfig{
		Dir:             cfg.PluginsDir,
		StateDir:        cfg.PluginStateDir,
		Keyring:         trustKeys,
		MarketURL:       cfg.PluginMarketURL,
		UploadMax:       cfg.PluginUploadMax,
		Jobs:            jobs.New(st.DBX()),
		HostVersion:     version,
		State:           st,
		Store:           pluginStore{st: st},
		SecretBox:       pluginSecrets,
		SessionResolver: pluginSessionResolver{st: st},
		SessionCookie:   "skin_session",
		ClientIP:        ygg.ClientIP,
		AccessLog: func(ctx context.Context, name, method, path, ip string, status, ms int64, tier string, uid int64) {
			outcome := accesslog.OutcomeOK
			switch {
			case status >= 500:
				outcome = accesslog.OutcomeError
			case status == 403 || status == 429:
				outcome = accesslog.OutcomeDenied
			case status >= 400:
				outcome = accesslog.OutcomeFail
			}
			accessLog.Log(ctx, accesslog.Entry{
				Kind: accesslog.Plugin, Outcome: outcome, UID: uid, Who: name, IP: ip,
				Detail: fmt.Sprintf("%s %s tier=%s status=%d %dms", method, path, tier, status, ms),
			})
		},
		Audit: func(ctx context.Context, action string, detail map[string]any) {
			st.InsertAudit(ctx, plugin.ActorFromContext(ctx), action, detail)
		},
	})
	slog.Info("plugins", "market", cfg.PluginMarketURL, "trustKeys", len(trustKeys), "stateDir", cfg.PluginStateDir, "uploadMax", cfg.PluginUploadMax)
	if err := pluginHost.Load(cfg.PluginsDir); err != nil {
		slog.Info("plugins: load", "plugins_dir", cfg.PluginsDir, "error", err)
	} else if n := len(pluginHost.List()); n > 0 {
		slog.Info("plugins: scanned from", "n", n, "plugins_dir", cfg.PluginsDir)
	}

	signer, err := ygg.NewLocalSigner(cfg.SignKeyPath)
	if err != nil {
		logx.Fatal("signer", "error", err)
	}
	slog.Info("signing key fingerprint", "value", signer.KeyFingerprint())

	// Phase 2: MUA Union member module. Options-first config (2026-10-24 拍板):
	// the stack is ALWAYS assembled so `union_enabled` hot-toggles from the
	// admin panel without a restart (blessing parity: 插件启停即改即生效).
	// When off: every union route 404s and texture signing reverts to the
	// local key live (zero-surface semantics preserved).
	unionCtx, unionCancel := context.WithCancel(context.Background())
	defer unionCancel()
	ustack := newUnionStack(unionCtx, st, cfg, version, signer)
	unionAPI, unionGrant := ustack.api, ustack.grant
	unionOut, unionSigner := ustack.out, ustack.signer
	var activeSigner ygg.Signer = signer
	if ustack.signer != nil {
		activeSigner = ustack.signer // hub key w/ local fallback; hot-off reverts live
	}

	yggSvc := ygg.NewService(st, activeSigner)
	yggSvc.TextureURL = pluginHost.TextureURL
	yggSvc.ProfileFilter = pluginHost.ProfileFilter
	yggSvc.AuthPhase = pluginHost.EmitAuthPhase
	yggSvc.UserLifecycle = pluginHost.EmitUserLifecycle
	pluginHost.SetBearerResolver(pluginBearerResolver{ygg: yggSvc, st: st})
	oidcDiscoveryURL := ""
	var oidcProvider *oidc.Provider
	if cfg.OIDCEnabled {
		okey, err := oidc.LoadOrCreateKey(cfg.OIDCSignKey)
		if err != nil {
			logx.Fatal("oidc key", "error", err)
		}
		oidcProvider, err = oidc.New(st, oidc.ProfileSource{
			Players: func(ctx context.Context, uid int64) ([]oidc.PlayerBrief, error) {
				ps, err := st.PlayersByUID(ctx, uid)
				if err != nil {
					return nil, err
				}
				out := make([]oidc.PlayerBrief, 0, len(ps))
				for _, p := range ps {
					out = append(out, oidc.PlayerBrief{ID: p.ID, UUID: p.UUID, Name: p.Name})
				}
				return out, nil
			},
			User: st.UserBrief,
		}, oidc.Config{
			Issuer:         cfg.OIDCIssuer,
			Key:            okey,
			SharedClientID: cfg.SharedClientID,
			LoginURL:       cfg.BaseURL + "/login",
		})
		if err != nil {
			logx.Fatal("oidc", "error", err)
		}
		oidcProvider.SessionResolver = st.SessionUser
		// Device-code knobs are panel settings, read live per request — a
		// save takes effect on the next device_authorization without restart.
		oidcProvider.DeviceKnobs = func() (time.Duration, int) {
			return st.OIDCDeviceKnobs(context.Background())
		}
		oidcProvider.Audit = func(event string, detail map[string]any) {
			slog.Info("oidc audit", "event", event, "detail", detail)
			st.InsertAudit(context.Background(), 0, event, detail)
		}
		oidc.SetClientSecretVerifier(func(hash, secret string) bool { return auth.VerifyPHC(secret, hash) })
		if err := oidcProvider.SeedSharedClient(context.Background()); err != nil {
			logx.Fatal("oidc seed client", "error", err)
		}
		// Dual-state token resolution: OIDC JWTs resolve through the jti
		// ledger; opaque password-flow tokens fall back to the store lookup.
		// Only ErrNotJOSE falls back — a JWT-shaped token that fails
		// verification must not degrade to another authority (2026-10-23).
		jwtResolve := oidc.NewYggTokenResolver(st, cfg.OIDCIssuer, &okey.PublicKey)
		yggSvc.SetTokenResolver(func(ctx context.Context, access string) (ygg.Token, error) {
			t, err := jwtResolve(ctx, access)
			if errors.Is(err, oidc.ErrNotJOSE) {
				return st.TokenByAccess(ctx, access)
			}
			return t, err
		})
		oidcDiscoveryURL = cfg.BaseURL + "/api/yggdrasil/.well-known/openid-configuration"
		slog.Info("oidc: enabled (issuer , shared client )", "oidc_issuer", cfg.OIDCIssuer, "shared_client_id", cfg.SharedClientID)
	}
	yggSvc.RequireVerification = cfg.RequireVerification
	yggAPI := ygg.NewAPI(yggSvc, ygg.MetadataConfig{
		ServerName:             cfg.ServerName,
		Version:                version,
		Homepage:               cfg.BaseURL,
		Register:               cfg.BaseURL + "/auth/register",
		SkinDomains:            cfg.SkinDomains,
		OpenIDConfigurationURL: oidcDiscoveryURL,
		ExtraSkinDomains: func() []string {
			if v, ok, err := st.GetOption(context.Background(), "union_skin_domains"); err == nil && ok && v != "" {
				return strings.Split(v, ",")
			}
			return nil
		},
	})
	// A2 Mojang 官方兜底（热开关 ygg_mojang_fallback，默认开）：profile/
	// hasJoined 本地未命中时向官方 sessionserver 串行查询——守卫传输层
	// （仅公网拨号/不跟随重定向/4s 硬超时/1MiB 上限，见 ygg/fallback.go）。
	mojangFallbackDefault := envOr("SKIN_MOJANG_FALLBACK", "1") == "1"
	yggAPI.SetFallback(ygg.NewFallback(func(ctx context.Context) bool {
		if v, ok, err := st.GetOption(ctx, "ygg_mojang_fallback"); err == nil && ok {
			return v == "1" || v == "true"
		}
		return mojangFallbackDefault
	}))
	// 运维观测面（2026-10-26）：网页/启动器登录、Yggdrasil 服务器流量、
	// 联盟出入站通信统一落 access_logs，管理面板可筛选分页查看。全程
	// 旁路：nil 安全、写失败吞掉，任何情况下不改变协议响应。保留 90 天，
	// 每 6h 清一次。
	yggAPI.SetAccess(accessLog)
	if unionOut != nil {
		unionOut.SetAccess(accessLog)
	}
	go func() {
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			bg := context.Background()
			if n, err := st.PurgeAccessLogs(bg, 90*24*time.Hour); err == nil && n > 0 {
				slog.Info("access_logs: purged rows older than 90d", "count", n)
			}
			// Two clocks on the detail table, on this same tick (no second
			// goroutine). Retention follows the panel option, read now; the
			// credential deadline is fixed at 24h and does not (see
			// store.SweepAccessDetailCredentials for why those must not merge).
			if window := detailDiag.RetentionWindow(bg); window > 0 {
				if n, err := st.PurgeAccessDetails(bg, window); err != nil {
					slog.Warn("access_log_details: purge failed", "error", err)
				} else if n > 0 {
					slog.Info("access_log_details: purged rows older than", "n", n, "window", window)
				}
			}
			if n, err := st.SweepAccessDetailCredentials(bg); err != nil {
				slog.Warn("access_log_details: credential sweep failed", "error", err)
			} else if n > 0 {
				slog.Info("access_log_details: cleared credentials on expired row(s) ( after capture)", "n", n, "credential_ttl", accesslog.CredentialTTL)
			}
			// Join sessions expire in 120 seconds and nothing had ever deleted
			// them: the table only ever grew, while hasJoined scanned it.
			if n, err := st.PurgeJoinSessions(bg); err != nil {
				slog.Warn("ygg_join_sessions: purge failed", "error", err)
			} else if n > 0 {
				slog.Info("ygg_join_sessions: purged expired row(s)", "count", n)
			}
		}
	}()
	backend := webapi.New(st, ts, yggSvc)
	// The SPA gates its authorized-apps/device pages on this flag; without it
	// those nav entries lead to a 404 whenever OIDC is off.
	backend.OIDCEnabled = cfg.OIDCEnabled
	// The wizard is the one endpoint that answers before anyone exists. Guard it
	// while it is still open, and say the token out loud in the log so the
	// operator — the only person reading that log — can complete the install.
	if need, nerr := st.NeedsSetup(context.Background()); nerr == nil && need {
		if tok, generated := installToken(cfg.SetupToken); tok != "" {
			backend.SetupToken = tok
			if generated {
				slog.Info("setup: the site is not initialized yet — use install token in the wizard (or set SKIN_SETUP_TOKEN to keep it across restarts)", "token", tok)
			}
		} else {
			// No operator token and no random one (a host where crypto/rand fails).
			// Say so loudly instead of quietly leaving the door open.
			slog.Warn("setup: WARNING the install wizard is unguarded because no setup token could be produced; set SKIN_SETUP_TOKEN before exposing this site")
		}
	}
	backend.Appearance = appearEng
	backend.ThemePalette = mailPalette
	backend.ThemeID = mailThemeID
	backend.TextureURL = pluginHost.TextureURL
	backend.ProfileFilter = pluginHost.ProfileFilter
	backend.OnTextureUploaded = pluginHost.EmitTextureUploaded
	backend.OnUserLifecycle = pluginHost.EmitUserLifecycle
	backend.CheckGuard = pluginHost.CheckGuard
	backend.OnEvent = pluginHost.EmitEvent
	backend.PluginUI = pluginHost.UI
	backend.PluginHostInfo = pluginHost.HostInfo
	backend.PluginFormData = pluginHost.ValidateFormData
	backend.Account = &account.Service{
		Store: st, Textures: ts, MaxChars: 3, OIDCEnabled: cfg.OIDCEnabled,
		CheckGuard: pluginHost.CheckGuard, EmitEvent: pluginHost.EmitEvent,
		OnTextureUploaded: pluginHost.EmitTextureUploaded,
		Audit: func(ctx context.Context, uid int64, action string, detail map[string]any) {
			st.InsertAudit(ctx, uid, action, detail)
		},
		UnionCreate: backend.SyncProfileCreate,
		UnionRename: backend.SyncProfileRename,
		UnionRetire: backend.SyncProfileDelete,
	}
	pluginHost.SetAccountService(pluginAccount{svc: backend.Account, signer: pluginHost.PluginSigner})
	yggSvc.JoinGuard = pluginHost.CheckGuard
	backend.Access = accessLog
	backend.RequireInvite = cfg.RequireInvite
	// A1 找回密码: reset-link base + mail subject + env fallbacks for the
	// panel's SMTP keys (option wins per request, see smtpConfig).
	backend.BaseURL = cfg.BaseURL
	backend.SiteName = cfg.SiteName
	backend.SMTPDefaults = cfg.SMTP.Map()
	backend.OnPasswordChange = func(uid int64) {
		if oidcProvider != nil {
			if err := oidcProvider.RevokeUserAllTokens(context.Background(), uid); err != nil {
				slog.Info("password change: OIDC revoke", "uid", uid, "error", err)
			}
		}
	}
	// Options panel → request-time join gate (no restart needed).
	verifyGate := func(ctx context.Context) bool {
		if v, ok, err := st.GetOption(ctx, "require_verification"); err == nil && ok {
			return v == "1" || v == "true"
		}
		return cfg.RequireVerification
	}
	yggSvc.RequireVerificationResolver = verifyGate
	backend.VerificationEnabled = verifyGate
	// 邮箱验证宽限期 (2026-11-02): options-panel `email_verify_grace_days`,
	// 0 (or unreadable) = enforce immediately.
	yggSvc.VerifyGraceResolver = func(ctx context.Context) time.Duration {
		if v, ok, err := st.GetOption(ctx, "email_verify_grace_days"); err == nil && ok {
			if n, e := strconv.Atoi(strings.TrimSpace(v)); e == nil && n > 0 {
				return time.Duration(n) * 24 * time.Hour
			}
		}
		return 0
	}
	// The web gate decides with the same two numbers (verify_gate_web.go).
	// Wiring a second reader here would be how "graceful in the launcher,
	// blocked in the panel" gets written into a release by accident.
	backend.VerifyGrace = yggSvc.VerifyGraceResolver
	// B5.1 dual-TTL windows (blessing ygg_token_expire_1/2): live options
	// override the defaults at request time (soft is fixed into expires_at
	// at issue; hard is re-read here per refresh).
	yggSvc.SetTokenWindowsResolver(func(ctx context.Context) ygg.TokenWindows {
		w := ygg.DefaultTokenWindows
		if v, ok, err := st.GetOption(ctx, "ygg_token_expire_1"); err == nil && ok {
			if n, e := strconv.Atoi(v); e == nil && n > 0 {
				w.Soft = time.Duration(n) * time.Second
			}
		}
		if v, ok, err := st.GetOption(ctx, "ygg_token_expire_2"); err == nil && ok {
			if n, e := strconv.Atoi(v); e == nil && n > 0 {
				w.Hard = time.Duration(n) * time.Second
			}
		}
		return w
	})
	// B5.2: ygg_rate_limit (same-username gap, ms) / ygg_tokens_limit (LRU
	// cap, 0=off) / ygg_search_profile_max (bulk lookup cap).
	yggSvc.SetAuthGapResolver(func(ctx context.Context) time.Duration {
		if v, ok, err := st.GetOption(ctx, "ygg_rate_limit"); err == nil && ok {
			if n, e := strconv.Atoi(v); e == nil && n >= 0 {
				return time.Duration(n) * time.Millisecond
			}
		}
		return time.Second // callbacks.php:96 default 1000ms
	})
	yggSvc.SetTokenLimitResolver(func(ctx context.Context) int {
		if v, ok, err := st.GetOption(ctx, "ygg_tokens_limit"); err == nil && ok {
			if n, e := strconv.Atoi(v); e == nil && n >= 0 {
				return n
			}
		}
		return 10 // callbacks.php:95 default
	})
	yggSvc.SetSearchMaxResolver(func(ctx context.Context) int {
		if v, ok, err := st.GetOption(ctx, "ygg_search_profile_max"); err == nil && ok {
			if n, e := strconv.Atoi(v); e == nil && n > 0 {
				return n
			}
		}
		return 5 // callbacks.php:98 default
	})
	// Rate limiting (P1 ygg_rate_limit): settings-panel values override env
	// at boot (limiter thresholds are boot-constructed; restart to apply).
	if v, ok, err := st.GetOption(context.Background(), "rate_limit_auth"); err == nil && ok {
		if n, e := strconv.Atoi(v); e == nil {
			cfg.RateLimitAuth = n
		}
	}
	if v, ok, err := st.GetOption(context.Background(), "login_fail_limit"); err == nil && ok {
		if n, e := strconv.Atoi(v); e == nil {
			cfg.LoginFailLimit = n
		}
	}
	rateLimiter := ratelimit.New(cfg.RateLimitAuth, 10*time.Minute, cfg.LoginFailLimit, 15*time.Minute)
	yggSvc.Limiter = rateLimiter
	backend.Limiter = rateLimiter
	if unionOut != nil {
		backend.MountUnion(unionOut, ustack.names) // registers /api/v1/union/* (zero surface when disabled)
	}
	pub := publicapi.New(st, ts)
	pub.TextureURL = pluginHost.TextureURL
	admin := adminapi.New(st, yggSvc)
	admin.Account = backend.Account // deletion runs through the same guards/events as the user path
	admin.Jobs = jobs.New(st.DBX())
	// The panel asks the *same* question the user centre asks, about the same
	// account. Handing over webapi's method is deliberate: a second
	// implementation of "still pending?" is how the panel ends up answering
	// yes while the profile page answers no.
	admin.VerifyGate = backend.VerifyPending
	// Plugin session-tier routes act as the signed-in player too (the account
	// bridge is tier-agnostic), so they consume the same verdict; public and
	// bearer plugin routes stay open.
	pluginHost.SetVerifyGate(backend.VerifyPending)
	admin.OnUserLifecycle = func(ctx context.Context, e plugin.UserLifecycleEvent) {
		pluginHost.EmitUserLifecycle(ctx, e)
		if e.Kind == "deleted" {
			pluginHost.EmitEvent(ctx, "user.deleted", plugin.EventPayload{UserID: e.UserID})
		}
	}
	buildCommit, buildStamp, buildBranchName := buildVersionInfo()
	admin.Version = version
	admin.BuildCommit = buildCommit
	admin.BuildTime = buildStamp
	admin.BuildBranch = buildBranchName
	admin.Environment = cfg.Environment
	admin.BaseURL = cfg.BaseURL
	admin.Listen = cfg.Listen
	admin.DataDir = cfg.DataDir
	admin.MigrationsDir = cfg.MigrationsDir
	admin.SiteName = cfg.SiteName
	admin.StartedAt = startedAt
	admin.OIDCEnabled = cfg.OIDCEnabled
	// Union hot settings (2026-10-24 拍板「热切换」): the three hot keys apply
	// on save without a restart; the rest of the union group is marked
	// restart-only in the settings panel.
	admin.OnSettingChanged = func(key, value string) {
		switch key {
		case "union_enabled":
			ustack.setEnabled(context.Background(), value == "1")
			slog.Info("union: hot toggle", "value", value)
		case "union_api_root":
			if err := ustack.setAPIRoot(value); err != nil {
				slog.Warn("union: hot api_root rejected", "error", err)
			}
		case "union_member_key":
			ustack.setMemberKey(value)
		case "cdn_enabled", "cdn_headers":
			applyCDN()
			slog.Info("cdn: hot apply", "key", key, "value", value)
		}
	}
	// Dashboard storage card: sum of texture files on disk (the textures
	// table has no size column; schema is frozen — walk the dir instead).
	// Small file counts (hundreds); no cache needed, failures degrade to "—".
	admin.StorageBytes = func() (int64, error) {
		var total int64
		err := filepath.WalkDir(cfg.TexturesDir, func(_ string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
			return nil
		})
		return total, err
	}
	// Settings panel: env-derived fallbacks; option values win per request.
	// B4 keys use blessing原名 defaults (config/options.php) so the panel
	// shows what an un-set option actually behaves as.
	admin.SettingsDefaults = map[string]string{
		"site_name":              cfg.SiteName,
		"server_name":            cfg.ServerName,
		"site_url":               cfg.BaseURL,
		"skin_domains":           strings.Join(cfg.SkinDomains, ","),
		"invite_required":        boolStr(cfg.RequireInvite),
		"require_verification":   boolStr(cfg.RequireVerification),
		"rate_limit_auth":        strconv.Itoa(cfg.RateLimitAuth),
		"oidc_device_lifetime":   strconv.Itoa(int(store.DefaultDeviceCodeLifetime / time.Second)),
		"oidc_device_poll_secs":  strconv.Itoa(store.DefaultDeviceCodePoll),
		"login_fail_limit":       strconv.Itoa(cfg.LoginFailLimit),
		"cdn_enabled":            "0",
		"cdn_headers":            strings.Join(ygg.DefaultCDNHeaders(), ","),
		"announcement":           "",
		"regs_per_ip":            "3",
		"max_upload_file_size":   "1024",
		"player_name_rule":       "official",
		"player_name_length_min": "3",
		"player_name_length_max": "16",
		"ygg_token_expire_1":     "259200",
		"ygg_token_expire_2":     "604800",
		"ygg_tokens_limit":       "10",
		"ygg_search_profile_max": "5",
		"ygg_rate_limit":         "1000",
		"ygg_mojang_fallback":    boolStr(mojangFallbackDefault),
		// Union group (2026-10-24): option-first, env fallback, blessing 原名
		"union_enabled":                boolStr(cfg.UnionEnabled),
		"union_api_root":               cfg.UnionAPIRoot,
		"union_member_key":             cfg.UnionMemberKey,
		"union_enable_update":          boolStr(cfg.UnionAllowUpdateBkKey),
		"union_enable_oauth2":          "1",
		"union_queryemail_mode":        cfg.UnionQueryEmailMode,
		"union_resolve_names":          boolStr(cfg.UnionResolveNames),
		"union_oauth2_sig_private_key": "",
		"union_oauth2_sig_public_key":  "",
		// A1 找回密码: SMTP env fallbacks shown as panel placeholders. Same map as
		// the mailer's, minus the password — see config.SMTPConfig.PanelMap.
		"smtp_host": cfg.SMTP.Host,
		"smtp_port": cfg.SMTP.Port,
		"smtp_user": cfg.SMTP.User,
		"smtp_pass": "",
		"smtp_from": cfg.SMTP.From,
	}
	// Admin user management shares the credential-change revoke semantics.
	admin.OnUserCredentialChange = func(uid int64) {
		if oidcProvider != nil {
			if err := oidcProvider.RevokeUserAllTokens(context.Background(), uid); err != nil {
				slog.Info("admin credential change: OIDC revoke", "uid", uid, "error", err)
			}
		}
	}
	// Union profile lifecycle on user deletion (2026-10-23 hardening):
	// announce SyncProfileDelete per player so the hub (and cross-site
	// clients) retire the profiles — best-effort async, 5s bound per
	// announcement, never blocks or fails the admin response. Failures are
	// audited with a FRESH context (the sync ctx is the thing that just
	// expired) and logged — same shape as webapi's union_sync_create_failed.
	admin.OnUserDeleted = func(uid int64, players [][2]string) {
		if unionOut == nil || !unionOut.Enabled() {
			return
		}
		go func() {
			for _, p := range players {
				uuid, name := p[0], p[1]
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := unionOut.SyncProfileDelete(ctx, uuid)
				cancel()
				if err != nil {
					st.InsertAudit(context.Background(), 0, "union_sync_delete_failed",
						map[string]interface{}{"uid": uid, "uuid": uuid, "name": name, "error": err.Error()})
					slog.Warn("union sync delete failed", "uid", uid, "uuid", uuid, "name", name, "error", err)
				}
			}
		}()
	}
	admin.SetSharedClientID(cfg.SharedClientID) // guards shared launcher client deletion
	// Admin player deletion (B3): same best-effort union announcement as
	// user deletion — retire the profile on the hub so cross-site clients
	// stop seeing it.
	admin.OnPlayerDeleted = func(uuid, name string) {
		if unionOut == nil || !unionOut.Enabled() {
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := unionOut.SyncProfileDelete(ctx, uuid)
			cancel()
			if err != nil {
				st.InsertAudit(context.Background(), 0, "union_sync_delete_failed",
					map[string]interface{}{"uuid": uuid, "name": name, "error": err.Error()})
				slog.Warn("union sync delete failed (admin player)", "uuid", uuid, "name", name, "error", err)
			}
		}()
	}
	// Admin player rename: this path writes through the store, so it does not
	// pass the account service where the other rename announcement lives — the
	// hub needs to hear about it either way (the reference listens to the core
	// event, which fires for both actors).
	admin.OnPlayerRenamed = func(uuid, name string) {
		backend.SyncProfileRename(context.Background(), uuid, name)
	}
	admin.ThemePalette = mailPalette
	admin.ThemeID = mailThemeID
	admin.MountAppearance(adminapi.AppearanceDeps{SEO: seoEng, Appear: appearEng, Themes: themeReg, ThemeMarket: themeMarketSvc})
	admin.MountPlugins(adminapi.PluginsDeps{Host: pluginHost})
	if unionSigner != nil {
		admin.MountUnion(adminapi.UnionDeps{
			Enabled: ustack.Enabled, // hot gate: 404 whole subtree when off
			Status: func() map[string]any {
				return map[string]any{
					"enabled":             ustack.Enabled(),
					"mode":                unionSigner.Mode(),
					"key_fingerprint":     unionSigner.KeyFingerprint(),
					"private_key_version": unionSigner.PrivateKeyVersion(),
					"server_list_version": unionAPI.ServerListVersion(),
					"rotation_suspended":  unionSigner.RotationSuspended(),
					"queryemail_mode":     ustack.cfg.QueryEmailMode,
					"hub_origin":          ustack.cfg.HubOrigin,
					"allow_update_bk_key": ustack.cfg.AllowUpdateBackendKey,
				}
			},
			ResumeRotation: unionSigner.ResumeRotation,
			// B5.3 ops buttons (wire-frozen union package; orchestration only)
			ForceRotation: unionSigner.ForceRotation,
			RefreshServerList: func(ctx context.Context) (any, error) {
				list, err := unionOut.ServerList(ctx)
				if err != nil {
					return nil, err
				}
				unionAPI.SetServerListVersion(list.Version) // keep status card fresh
				return list, nil
			},
			SyncRoster: func(ctx context.Context) (int, error) {
				roster, err := st.AllProfileUUIDs(ctx)
				if err != nil {
					return 0, err
				}
				if err := unionOut.Sync(ctx, roster); err != nil {
					return 0, err
				}
				return len(roster), nil
			},
			Diagnose: unionOut.Diagnose,
			// B5.4 blacklist proxy (hub relay; audit lives in adminapi)
			BlacklistQuery: func(ctx context.Context, q url.Values) (int, json.RawMessage, error) {
				res, err := unionOut.BlacklistQuery(ctx, q)
				if err != nil {
					return 0, nil, err
				}
				return res.StatusCode, res.Body, nil
			},
			BlacklistAdd: func(ctx context.Context, body json.RawMessage) (int, json.RawMessage, error) {
				res, err := unionOut.BlacklistCreate(ctx, body)
				if err != nil {
					return 0, nil, err
				}
				return res.StatusCode, res.Body, nil
			},
			BlacklistRelease: func(ctx context.Context, id string) (int, json.RawMessage, error) {
				res, err := unionOut.BlacklistInvalidate(ctx, id)
				if err != nil {
					return 0, nil, err
				}
				return res.StatusCode, res.Body, nil
			},
			BlacklistDelete: func(ctx context.Context, id string) (int, json.RawMessage, error) {
				res, err := unionOut.BlacklistDelete(ctx, id)
				if err != nil {
					return 0, nil, err
				}
				return res.StatusCode, res.Body, nil
			},
		})
	}

	root := http.NewServeMux()
	mountCompatSurfaces(root, yggAPI.Handler())
	if oidcProvider != nil {
		root.Handle("GET /api/yggdrasil/.well-known/openid-configuration", oidcProvider.DiscoveryHandler())
		// /api/v1/oidc/* must be claimed at the root mux: the webapi
		// "/api/v1/" subtree below would otherwise shadow the provider's
		// device/token API (live: /device page HTTP 404, 2026-10-23).
		root.Handle("/api/v1/oidc/", oidcProvider.DeviceAPIHandler())
	}
	if unionAPI != nil {
		// 1 MiB body cap (MUA review P2): /api/union/* verified its signature
		// over io.ReadAll — an unbounded body was a memory DoS surface.
		// ustack.gate = hot union_enabled check (2026-10-24 拍板): routes stay
		// mounted, switched off = whole subtree 404.
		root.Handle("/api/union/", ustack.gate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
			}
			unionAPI.Handler().ServeHTTP(w, r)
		})))
		root.Handle("GET /api/union/member/oauth2/grant", ustack.gate(unionGrantHandler(unionGrant, unionOut, st)))
	}
	root.Handle("/api/v1/", backend.Handler())
	root.Handle("/api/admin/v1/", admin.Handler())
	root.Handle("/api/public/", pub.Handler())
	root.Handle("/usm/", pub.Handler())
	root.Handle("/skin/", pub.Handler())
	root.Handle("/cape/", pub.Handler())
	// CustomSkinAPI R1 (GET /{player}.json) is served by pub.WrapRoot below:
	// ServeMux cannot register "{player}.json" next to the "/" SPA catch-all.
	root.Handle("GET /textures/{hash}", textureHandler(ts))
	// Blessing TextureController static surface (routes/static.php
	// parity): avatars + previews. Registered at the root mux so they
	// beat the SPA catch-all below.
	av := &avatarDeps{st: st, ts: ts, redirect: pluginHost.AvatarURL}
	// Deployment self-check inputs (U16): only the boot knows where the bundle
	// is, what SKIN_BASE_URL said before any option overrode it, and whether a
	// signing keyring loaded. The preview probe reuses the live renderer.
	admin.Diagnostics = adminapi.DiagnosticsDeps{
		WebDist: spaDistDir,
		EnvBase: os.Getenv("SKIN_BASE_URL"),
		Preview: av.smokePreview,
		// The mail-DNS check asks public resolvers, not the system one: a cloud
		// VPC's internal DNS can serve a stale RRset while receivers already see
		// the new record (measured on Aliyun 100.100.2.x).
		LookupTXT: adminapi.PublicTXTLookup,
		// One closure, four readers: the launcher, the register form, the panel
		// gate and the self-check row that tells the operator how many accounts
		// this switch is about to stop.
		VerifyGate: verifyGate,
		KeyringStatus: func() (int, string) {
			return len(trustKeys), cfg.PluginMarketURL
		},
	}
	root.HandleFunc("GET /avatar/player/{name}", av.handleAvatarPlayer)
	root.HandleFunc("GET /avatar/user/{uid}", av.handleAvatarUser)
	root.HandleFunc("GET /avatar/hash/{hash}", av.handleAvatarHash)
	root.HandleFunc("GET /avatar/{tid}", av.handleAvatarTID)
	root.HandleFunc("GET /preview/hash/{hash}", av.handlePreviewHash)
	root.HandleFunc("GET /preview/{tid}", av.handlePreviewTID)
	root.HandleFunc("GET /texture-render/{hash}", av.handleViewerTexture)
	// Phase 3 public surface: health probe, SEO outputs, theme assets.
	root.Handle("GET /healthz", healthzHandler(st))
	// The scrape surface is registered unconditionally and gated in the
	// handler: /metrics publishes route cardinality and error counts to
	// whoever can reach the port, so the switch defaults to off (SKIN_METRICS)
	// and an unparseable value never opens it.
	root.Handle("GET /metrics", metricsHandler(metricsEnabled()))
	root.Handle("GET /sitemap.xml", sitemapHandler(seoEng, st, cfg.BaseURL))
	root.Handle("GET /robots.txt", robotsHandler(seoEng))
	root.Handle("GET /llms.txt", llmsHandler(seoEng))
	root.Handle("GET /theme-assets/{id}/{file}", themeAssetHandler(themeReg))
	// A plugin UI page paints with its own inline defaults, so the theme has to
	// travel as CSS in <head> rather than only through the postMessage bridge —
	// otherwise every plugin page shows the stock palette for a moment before the
	// bridge applies the site's theme. Same source the pages get it from.
	pluginThemeHead := func(ctx context.Context) string {
		id := appearEng.Settings(ctx).ActiveTheme
		if id == "" {
			return ""
		}
		t, err := themeReg.Get(id)
		if err != nil {
			return ""
		}
		// TokenStyle, not HeadHTML: a plugin document must inherit the
		// palette, and a linked theme stylesheet would also apply the theme's
		// bare element rules (body background wash, h1/h2 font, input size) to
		// the plugin's own markup.
		return t.TokenStyle()
	}
	root.Handle("/plugin-assets/", pluginHost.AssetHandler(pluginThemeHead))
	// Public server-rendered texture detail page (2026-10-25 拍板: 皮肤详情
	// 是搜索引擎的内容页). Registered on the root mux so it beats the SPA
	// catch-all below; private textures render only for their owner (noindex).
	root.HandleFunc("GET /skinlib/{tid}", texturePageHandler(st, seoEng, appearEng, cfg.SiteName, cfg.BaseURL, pluginHost.TextureURL, pluginHost.SlotHTMLContext))
	// Public landing page data (counts + featured library cards + login state).
	landingFn := func(r *http.Request) landingData {
		ctx := r.Context()
		d := landingData{Lang: webi18n.ResolveLang(r), SiteName: cfg.SiteName, SiteURL: cfg.BaseURL}
		// 已登录访客的 hero CTA 换成进站动作（2026-10-27 逻辑修复）
		if c, err := r.Cookie("skin_session"); err == nil {
			if id, err := st.SessionUser(ctx, c.Value); err == nil {
				d.LoggedIn = true
				// Same source as the SPA sidebar badge; a count failure hides the badge.
				if n, err := st.UnreadNotificationCount(ctx, id); err == nil {
					d.Unread = n
				}
			}
		}
		stats, _ := landingCache.get(ctx, func(ctx context.Context) (landingStats, error) {
			var s landingStats
			var err error
			if s.Users, err = st.CountUsers(ctx); err != nil {
				return s, err
			}
			if s.Players, err = st.CountPlayers(ctx); err != nil {
				return s, err
			}
			if s.Textures, err = st.CountTextures(ctx); err != nil {
				return s, err
			}
			rows, _, err := st.SkinlibList(ctx, store.SkinlibOpts{Sort: "likes", Page: 1, PerPage: 8})
			if err != nil {
				return s, err
			}
			for _, r := range rows {
				if !r.Public {
					continue
				}
				// Same split as the skinlib UI: skins preview rendered, capes
				// flat texture (a cape through the body renderer 404s).
				u := "/preview/" + strconv.FormatInt(r.TID, 10) + "?v=" + avatargen.RenderVersion
				if r.Type == "cape" {
					u = "/textures/" + r.Hash
				}
				s.Featured = append(s.Featured, landingTexture{TID: r.TID, Name: r.Name, URL: u})
			}
			return s, nil
		})
		d.Users, d.Players, d.Textures = stats.Users, stats.Players, stats.Textures
		d.Featured = stats.Featured
		return d
	}
	// A signed-in viewer boots the interactive app, so the shell must not also
	// ship them the crawler's public list (that swap is the double render).
	loggedInFn := func(r *http.Request) bool {
		_, ok := sessionUserID(st, r)
		return ok
	}
	spa := spaHandler(spaDistDir, seoEng, appearEng, cfg.SiteName, landingFn, loggedInFn, st, pluginHost.HasUIPath)
	if oidcProvider != nil {
		spa = oidcProvider.Wrap(spa)
	}
	// Plugin routes (read-only, GET/HEAD) resolve per request from the live
	// registry so enable/disable takes effect without touching the mux.
	root.Handle("/api/plugins/", pluginHost.Handler())
	root.Handle("/", spa)

	addr := cfg.Listen
	// The analytics origins an operator declares end up inside a response
	// header, so a value that is not one CSP source fails the start rather than
	// either splitting the header or silently widening nothing — same rule as
	// SKIN_TRUSTED_PROXIES below. This one runs before the "listening" line,
	// because a boot that announces a port and then exits is the worst kind of
	// log to read at 3am.
	cspScript, err := parseCSPSources("SKIN_CSP_SCRIPT_EXTRA_SOURCES", cfg.CSPScriptExtraSources)
	if err != nil {
		logx.Fatal("startup", "error", err)
	}
	cspConnect, err := parseCSPSources("SKIN_CSP_CONNECT_EXTRA_SOURCES", cfg.CSPConnectExtraSources)
	if err != nil {
		logx.Fatal("startup", "error", err)
	}
	if len(cspScript) > 0 || len(cspConnect) > 0 {
		slog.Info("csp: extra sources allowed", "script_src", strings.Join(cspScript, " "), "connect_src", strings.Join(cspConnect, " "))
	}
	slog.Info("skin-nexus listening", "version", version, "addr", addr, "base_url", cfg.BaseURL)

	if len(cfg.TrustedProxies) > 0 {
		// A typo here would silently widen the trust that every rate limit and
		// audit row depends on, so refuse to start rather than fall back.
		if err := ygg.ConfigureTrustedProxies(cfg.TrustedProxies); err != nil {
			logx.Fatal("config", "error", err)
		}
		slog.Info("client IP: X-Forwarded-For accepted only from", "value", strings.Join(cfg.TrustedProxies, ", "))
	}
	// securityHeaders must be the outermost wrapper: pub.WrapRoot answers the
	// CustomSkinAPI `GET /{player}.json` routes itself, so anything it wraps
	// never sees a header applied underneath it. accesslog.Observe sits just
	// inside it: it notes the status code and a bounded body prefix for the
	// diagnostic detail rows and forwards every byte (see internal/accesslog/
	// observe.go — it persists nothing by itself).
	srv := newHTTPServer(addr, securityHeaders(accesslog.Observe(pub.WrapRoot(gzipHandler(root))), cspExtras{script: cspScript, connect: cspConnect}))
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe() }()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logx.Fatal("err")
		}
	case sig := <-stop:
		// Docker/systemd stop sends SIGTERM: drain in-flight requests instead
		// of dropping uploads and texture downloads mid-response. 8s stays
		// inside Docker's default 10s stop timeout before SIGKILL.
		slog.Info("received , shutting down", "sig", sig)
		unionCancel()
		_ = pluginHost.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			slog.Info("graceful shutdown", "error", err)
		}
	}
}

// unionGrantHandler is the login-state glue for the Union OAuth2 grant
// (analysis/yggdrasil-connect.md §4.5): fetch the hub encryption key, build
// the signed+encrypted userInfo token, 302 to the hub continuation URL. The
// continuation base follows the live API root (hot union_api_root).
func unionGrantHandler(grant *union.Grantor, out *union.Outbound, st *store.Mysql) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("skin_session")
		if err != nil {
			http.Error(w, "login required", http.StatusUnauthorized)
			return
		}
		uid, err := st.SessionUser(r.Context(), c.Value)
		if err != nil {
			http.Error(w, "login required", http.StatusUnauthorized)
			return
		}
		u, err := st.UserBrief(r.Context(), uid)
		if err != nil {
			http.Error(w, "user lookup failed", http.StatusInternalServerError)
			return
		}
		hubKey, err := out.OAuth2BackendKey(r.Context())
		if err != nil {
			http.Error(w, "hub key unavailable", http.StatusBadGateway)
			return
		}
		token, err := grant.GrantToken(uid, u.Nickname, u.Email, hubKey.PublicKey)
		if err != nil {
			http.Error(w, "grant failed", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, grant.GrantRedirectURL(token, out.APIRoot()+"/oauth2/continue", r.URL.Query()), http.StatusFound)
	})
}

// textureHandler serves stored textures byte-exact with strict hash validation.
func zeroTime() time.Time { return time.Time{} }

// isHTTPBaseURL gates site_url/base_url option values at boot: only
// http(s) absolute origins survive (settings panel enforces the same rule;
// this catches direct DB writes before they poison texture URLs/issuer).
func isHTTPBaseURL(v string) bool {
	return strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://")
}

// resolveBaseURL applies the 拍板 priority: option site_url → legacy option
// base_url → env fallback (the passed envDef). Non-http(s) option values
// are rejected (kept envDef) — garbage from a direct DB write must not
// burn into texture URLs / OIDC issuer / sitemap.
// envOr reads an env var with a fallback default (SMTP port etc.).
func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func resolveBaseURL(getOpt func(key string) (string, bool), envDef string) string {
	if v, ok := getOpt("site_url"); ok && isHTTPBaseURL(v) {
		return strings.TrimRight(v, "/")
	}
	if v, ok := getOpt("base_url"); ok && isHTTPBaseURL(v) {
		return strings.TrimRight(v, "/")
	}
	return envDef
}

func textureHandler(ts *texture.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f, err := ts.Open(r.PathValue("hash"))
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		defer f.Close()
		// Content-addressed: immutable cache is safe (hash = filename).
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		http.ServeContent(w, r, "", zeroTime(), f)
	}
}

// spaDistDir is the bundle the SPA is served from. One constant, because the
// static handler and the deployment self-check must look at the same directory
// — a check pointing at a path nothing serves would report green forever.
const spaDistDir = "web/dist"

// spaHandler serves web/dist if present, else a placeholder page. Every HTML
// serve passes through the SEO + appearance injection layers (phase 3).
// Only concrete asset files are served raw: os.Stat on "" resolves to the dist
// directory itself, so "exists on disk" must not mean "static file" — root and
// /index.html need the injected index.html too (2026-10-25 fix: "/" was served
// raw and skipped SEO/theme/custom injection entirely).
// The exact root path serves the public server-rendered landing page instead
// of the SPA shell (2026-10-25 拍板: exposure for first-time visitors/crawlers).
// hashedAsset reports Vite's content-hashed chunk names (main-DDTQwvLG.js).
func hashedAsset(name string) bool {
	ext := path.Ext(name)
	if ext != ".js" && ext != ".css" {
		return false
	}
	base := strings.TrimSuffix(name, ext)
	dash := strings.LastIndex(base, "-")
	return dash > 0 && len(base)-dash-1 >= 6
}

// staticPathIn resolves a request path inside the SPA directory, or "" when the
// path escapes it. dir is configurable and usually relative ("web/dist"), so it
// has to go through the same cleaning filepath.Join applies: on Windows a raw
// "web/dist/" prefix never matches the backslash-joined result, which made every
// asset 404 for a site hosted on Windows.
func staticPathIn(dir, urlPath string) string {
	full := filepath.Join(dir, urlPath)
	base := filepath.Clean(dir) + string(filepath.Separator)
	if !strings.HasPrefix(full, base) {
		return ""
	}
	return full
}

// isKnownSPAPath whitelists the SPA shell routes; anything else is a true 404.
func isKnownSPAPath(p string, uiPath func(string) bool) bool {
	if strings.HasPrefix(p, "ext/") {
		return uiPath != nil && uiPath(p)
	}
	// The admin panel owns real URLs (/admin, /admin/<tab>, /admin/plugin/<id>):
	// operators bookmark and share panel views, so the shell must load for them
	// instead of 404ing before the SPA can resolve the tab. Boundary-checked so
	// /administrator stays a true 404. The shell is noindex like every SPA path.
	if p == "admin" || strings.HasPrefix(p, "admin/") {
		return true
	}
	switch p {
	case "index.html", "login", "register", "forgot-password", "reset-password", "email-verify", "device",
		"setup", "skinlib", "help", "user/skinlib", "user/closet", "user/profile",
		"user/players", "user/dashboard":
		return true
	}
	return strings.HasPrefix(p, "user/")
}

func spaHandler(dir string, seoEng *seo.Engine, appearEng *appearance.Engine, siteName string, landing func(*http.Request) landingData, loggedIn func(*http.Request) bool, skinlib skinlibLister, uiPath func(string) bool) http.Handler {
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		fs := http.FileServer(http.Dir(dir))
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			persistLang(w, r)
			p := strings.TrimPrefix(r.URL.Path, "/")
			if p == "" && landing != nil {
				html := renderLanding(r, seoEng, appearEng, landing(r))
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = w.Write([]byte(html))
				return
			}
			inject := !strings.HasPrefix(p, "api/")
			if inject {
				// Read-only probe, but an escaping path would still reveal which
				// files exist outside dir, so contain it before statting. Join
				// already cleans ".." segments; the prefix check catches the rest.
				// dir itself has to be cleaned too: with a relative config like
				// "web/dist" on Windows, Join produces "web\dist\…" while the
				// raw prefix is "web/dist/", so no asset ever matched and the
				// whole SPA 404'd on a Windows-hosted site.
				if full := staticPathIn(dir, p); full != "" {
					if fi, err := os.Stat(full); err == nil && !fi.IsDir() && p != "index.html" {
						inject = false // real asset file
					}
				}
			}
			// Unknown SPA paths get a real 404 (2026-10-27 逻辑修复：软 404 —
			// 任何未知 URL 都曾 200 返回登录卡，爬虫把它们当内容页收进索引).
			// Known shell routes: auth flows, device verification, the public
			// skinlib list and the /user/* launcher-deep-link family.
			if inject && !isKnownSPAPath(p, uiPath) {
				writeTexNotFound(r.Context(), w, siteName, appearEng, webi18n.ResolveLang(r), "notfound.titlePage", "notfound.page")
				return
			}
			if inject {
				raw, err := os.ReadFile(dir + "/index.html")
				if err != nil {
					http.Error(w, "index missing", http.StatusInternalServerError)
					return
				}
				html := string(raw)
				meta := pageMetaFor(p)
				meta.Copy = seoCopyFor(webi18n.ResolveLang(r))
				// The library list is the one SPA route that is public content a
				// crawler is asked to index, so its first page ships in the HTML:
				// Bing says critical content must not live behind client-side
				// rendering, and AI crawlers do not run JavaScript at all.
				if strings.TrimRight(p, "/") == "skinlib" {
					// The list is for crawlers and cold anonymous loads. A signed-in
					// viewer boots the interactive app, so the shell must not also
					// ship them the crawler list first — that swap is the double
					// render (index.html's "swap is the first paint" only holds when
					// JS beats the first paint; under latency it does not).
					signedIn := loggedIn != nil && loggedIn(r)
					if !signedIn {
						if block, pm, ok := prerenderSkinlib(r.Context(), r, skinlib, siteName); ok {
							html = injectAppContent(html, block)
							meta = pm
						}
					}
				}
				html = seoEng.RenderHeadWith(r.Context(), html, meta)
				html = appearEng.Render(r.Context(), html)
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Header().Set("Cache-Control", "no-cache")
				_, _ = w.Write([]byte(html))
				return
			}
			// Vite emits content-hashed chunks; those are safe to cache for a
			// year. Fixed-name files (viewer.js) must revalidate.
			if strings.HasPrefix(p, "assets/") {
				if hashedAsset(path.Base(p)) {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "no-cache")
				}
			} else if p == "favicon.svg" || p == "favicon.ico" || p == "manifest.webmanifest" {
				w.Header().Set("Cache-Control", "public, max-age=86400")
			}
			fs.ServeHTTP(w, r)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<h1>SkinNexus</h1><p>web/dist not built yet. See docs/.</p>"))
	})
}

// healthChecker is what the probe needs from the store; naming it keeps the
// answer's shape testable without standing up a database.
type healthChecker interface {
	Health(ctx context.Context) error
}

// healthzHandler is the container health probe (DB round-trip). It also
// answers the two questions a human asks it during an incident — which build
// is this, and how long has it been up — so neither needs a shell.
func healthzHandler(st healthChecker) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := st.Health(r.Context()); err != nil {
			http.Error(w, "unhealthy", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ok":true,"version":%q,"uptime_seconds":%d}`,
			version, int64(metrics.Default.Uptime().Seconds()))
	})
}

// metricsEnabled reads SKIN_METRICS. Unset or unparseable means off: the
// endpoint publishes request cardinality and error counts, so it is a switch
// the operator throws, never a default.
func metricsEnabled() bool {
	v := strings.TrimSpace(os.Getenv("SKIN_METRICS"))
	if v == "" {
		return false
	}
	on, err := strconv.ParseBool(v)
	return err == nil && on
}

// metricsHandler renders the registry in Prometheus text exposition format,
// or refuses with 404 when the switch is off — a route that answers 404 looks
// exactly like a route that does not exist, which is the point.
func metricsHandler(enabled bool) http.Handler {
	if !enabled {
		return http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		if err := metrics.Default.WriteText(w); err != nil {
			http.Error(w, "metrics unavailable", http.StatusInternalServerError)
		}
	})
}

// sitemapHandler serves /sitemap.xml (static routes + bounded public
// texture detail pages). The texture detail page replaced player profile
// pages as the crawlable content surface (2026-10-25): profile pages are
// SPA shells with no anonymous content, and only /skinlib/{tid} renders
// real server-side HTML.
func sitemapHandler(seoEng *seo.Engine, st *store.Mysql, baseURL string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s := seoEng.Settings(r.Context())
		if !s.SitemapOn {
			http.NotFound(w, r)
			return
		}
		entries := []seo.SitemapEntry{}
		// bounded: at most store.SitemapIDLimit public texture pages — the same
		// constant fsck reports against, or the two numbers drift apart and
		// `omitted=` stops describing what a crawler is actually told.
		if rows, err := st.PublicTextureIDs(r.Context(), store.SitemapIDLimit); err == nil {
			for _, row := range rows {
				e := seo.SitemapEntry{Path: "/skinlib/" + strconv.FormatInt(row.TID, 10)}
				if row.UploadAt.Valid {
					e.LastMod = row.UploadAt.Time
				}
				entries = append(entries, e)
			}
		}
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		_, _ = w.Write([]byte(seo.SitemapXML(s.Canonical, baseURL, entries)))
	})
}

// robotsHandler serves /robots.txt from the SEO settings.
func robotsHandler(seoEng *seo.Engine) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s := seoEng.Settings(r.Context())
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(s.Robots))
	})
}

// llmsHandler serves /llms.txt for AI answer engines.
func llmsHandler(seoEng *seo.Engine) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_, _ = w.Write([]byte(seoEng.LLMSTxt(r.Context())))
	})
}

// themeAssetHandler serves theme pack assets with strict path validation.
func themeAssetHandler(reg *theme.Registry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		file := r.PathValue("file")
		full, err := reg.ResolveAsset(id, file)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		// A theme pack is third-party content on the site's own origin. If someone
		// navigates straight to an HTML file inside it, that document would run with
		// the site's cookies; the sandbox header puts it in an opaque origin instead.
		sandboxDocument(w, mime.TypeByExtension(strings.ToLower(filepath.Ext(full))))
		w.Header().Set("Cache-Control", "public, max-age=86400")
		http.ServeFile(w, r, full)
	})
}
