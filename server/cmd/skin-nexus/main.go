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
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"skin-nexus/server/internal/accesslog"
	"skin-nexus/server/internal/account"
	"skin-nexus/server/internal/adminapi"
	"skin-nexus/server/internal/appearance"
	"skin-nexus/server/internal/auth"
	"skin-nexus/server/internal/config"
	"skin-nexus/server/internal/jobs"
	"skin-nexus/server/internal/migrate"
	"skin-nexus/server/internal/oidc"
	"skin-nexus/server/internal/plugin"
	"skin-nexus/server/internal/publicapi"
	"skin-nexus/server/internal/ratelimit"
	"skin-nexus/server/internal/seo"
	"skin-nexus/server/internal/signedpkg"
	"skin-nexus/server/internal/store"
	"skin-nexus/server/internal/texture"
	"skin-nexus/server/internal/theme"
	"skin-nexus/server/internal/union"
	"skin-nexus/server/internal/webapi"
	"skin-nexus/server/internal/ygg"
)

var version = "0.4.0"

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

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// Legacy verifier: built from optional legacy .env (SKIN_LEGACY_ENV).
	var legacy *auth.LegacyVerifier
	if envPath := os.Getenv("SKIN_LEGACY_ENV"); envPath != "" {
		lenv, err := migrate.ParseLegacyEnv(envPath)
		if err != nil {
			log.Fatalf("legacy env: %v", err)
		}
		legacy = auth.NewLegacyVerifier(lenv.MethodSalts())
		log.Printf("legacy verifier: %d methods loaded", len(lenv.PWDMethods))
	}

	st, err := store.Open(cfg.DatabaseDSN, cfg.BaseURL, legacy)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	// Phase 3 / plan option A (2026-10-23): tracked migrations. Runs on every
	// SKIN_AUTO_MIGRATE boot — new files reach EXISTING databases (the
	// empty-DB-only path left 0006 unapplied on the live test instance);
	// schema_migrations markers skip completed files, duplicate-object
	// collisions from pre-tracking databases are tolerated, all else fatal.
	if cfg.AutoMigrate {
		if err := migrate.ApplySQLDirTracked(st.DB(), cfg.MigrationsDir); err != nil {
			log.Fatalf("schema: %v", err)
		}
	}
	if n, err := st.BackfillPublicDedupe(context.Background()); err != nil {
		log.Printf("dedupe: backfill public hashes: %v", err)
	} else if n > 0 {
		log.Printf("dedupe: backfilled %d public texture hashes", n)
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
		log.Fatalf("textures: %v", err)
	}

	// Phase 3: SEO + appearance (theme packs, custom CSS/JS).
	seoEng := seo.NewEngine(st, seo.Defaults{SiteName: cfg.SiteName, BaseURL: cfg.BaseURL})
	themeReg, err := theme.NewRegistry(cfg.ThemesDir, version)
	if err != nil {
		log.Fatalf("themes: %v", err)
	}
	if n := len(themeReg.List()); n > 0 {
		log.Printf("themes: %d packs loaded from %s", n, cfg.ThemesDir)
	}
	appearEng := appearance.NewEngine(st, themeReg)

	accessLog := accesslog.New(st.InsertAccessLog)

	// Plugin host: scans <data>/plugins; invalid directories are
	// recorded as errored entries and never abort boot. Hooks are fail-open
	// so a crashing plugin cannot alter the frozen Yggdrasil wire.
	var pluginSecrets *plugin.SecretBox
	if box, err := plugin.NewSecretBox(os.Getenv("SKIN_PLUGIN_SECRET_KEY"), filepath.Join(filepath.Dir(cfg.PluginsDir), "plugin-secrets.key")); err != nil {
		log.Printf("plugins: secret box disabled: %v", err)
	} else {
		pluginSecrets = box
	}
	var trustKeys signedpkg.Keyring
	if cfg.PluginTrustKeys != "" {
		if keys, err := signedpkg.ParseKeys(cfg.PluginTrustKeys); err != nil {
			log.Printf("plugins: trust keys invalid: %v", err)
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
		Jobs:            jobs.New(st.DB()),
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
				Kind: "plugin", Outcome: outcome, UID: uid, Who: name, IP: ip,
				Detail: fmt.Sprintf("%s %s tier=%s status=%d %dms", method, path, tier, status, ms),
			})
		},
		Audit: func(ctx context.Context, action string, detail map[string]any) {
			st.InsertAudit(ctx, plugin.ActorFromContext(ctx), action, detail)
		},
	})
	log.Printf("plugins: market=%q trustKeys=%d stateDir=%s uploadMax=%d", cfg.PluginMarketURL, len(trustKeys), cfg.PluginStateDir, cfg.PluginUploadMax)
	if err := pluginHost.Load(cfg.PluginsDir); err != nil {
		log.Printf("plugins: load %s: %v", cfg.PluginsDir, err)
	} else if n := len(pluginHost.List()); n > 0 {
		log.Printf("plugins: %d scanned from %s", n, cfg.PluginsDir)
	}

	signer, err := ygg.NewLocalSigner(cfg.SignKeyPath)
	if err != nil {
		log.Fatalf("signer: %v", err)
	}
	log.Printf("signing key fingerprint: %s", signer.KeyFingerprint())

	// Phase 2: MUA Union member module. Options-first config (2026-10-24 拍板):
	// the stack is ALWAYS assembled so `union_enabled` hot-toggles from the
	// admin panel without a restart (blessing parity: 插件启停即改即生效).
	// When off: every union route 404s and texture signing reverts to the
	// local key live (zero-surface semantics preserved).
	ustack := newUnionStack(context.Background(), st, cfg, version, signer)
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
			log.Fatalf("oidc key: %v", err)
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
			log.Fatalf("oidc: %v", err)
		}
		oidcProvider.SessionResolver = st.SessionUser
		oidcProvider.Audit = func(event string, detail map[string]any) {
			log.Printf("oidc audit: %s %v", event, detail)
			st.InsertAudit(context.Background(), 0, event, detail)
		}
		oidc.SetClientSecretVerifier(func(hash, secret string) bool { return auth.VerifyPHC(secret, hash) })
		if err := oidcProvider.SeedSharedClient(context.Background()); err != nil {
			log.Fatalf("oidc seed client: %v", err)
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
		log.Printf("oidc: enabled (issuer %s, shared client %s)", cfg.OIDCIssuer, cfg.SharedClientID)
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
			if n, err := st.PurgeAccessLogs(context.Background(), 90*24*time.Hour); err == nil && n > 0 {
				log.Printf("access_logs: purged %d rows older than 90d", n)
			}
		}
	}()
	backend := webapi.New(st, ts, yggSvc)
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
		Store: st, Textures: ts, MaxChars: 3, BaseURL: cfg.BaseURL, OIDCEnabled: cfg.OIDCEnabled,
		CheckGuard: pluginHost.CheckGuard, EmitEvent: pluginHost.EmitEvent,
		OnTextureUploaded: pluginHost.EmitTextureUploaded,
		Audit: func(ctx context.Context, uid int64, action string, detail map[string]any) {
			st.InsertAudit(ctx, uid, action, detail)
		},
		UnionSync: backend.SyncProfileCreate,
	}
	pluginHost.SetAccountService(pluginAccount{svc: backend.Account, signer: pluginHost.PluginSigner})
	yggSvc.JoinGuard = pluginHost.CheckGuard
	backend.Access = accessLog
	backend.RequireInvite = cfg.RequireInvite
	// A1 找回密码: reset-link base + mail subject + env fallbacks for the
	// panel's SMTP keys (option wins per request, see smtpConfig).
	backend.BaseURL = cfg.BaseURL
	backend.SiteName = cfg.SiteName
	backend.SMTPDefaults = map[string]string{
		"smtp_host": os.Getenv("SKIN_SMTP_HOST"),
		"smtp_port": envOr("SKIN_SMTP_PORT", "465"),
		"smtp_user": os.Getenv("SKIN_SMTP_USER"),
		"smtp_pass": os.Getenv("SKIN_SMTP_PASS"),
		"smtp_from": os.Getenv("SKIN_SMTP_FROM"),
	}
	backend.OnPasswordChange = func(uid int64) {
		if oidcProvider != nil {
			if err := oidcProvider.RevokeUserAllTokens(context.Background(), uid); err != nil {
				log.Printf("password change: OIDC revoke uid=%d: %v", uid, err)
			}
		}
	}
	// Options panel → request-time join gate (no restart needed).
	yggSvc.RequireVerificationResolver = func(ctx context.Context) bool {
		if v, ok, err := st.GetOption(ctx, "require_verification"); err == nil && ok {
			return v == "1" || v == "true"
		}
		return cfg.RequireVerification
	}
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
		backend.MountUnion(unionOut) // registers /api/v1/union/* (zero surface when disabled)
	}
	pub := publicapi.New(st, ts)
	pub.TextureURL = pluginHost.TextureURL
	admin := adminapi.New(st, yggSvc)
	admin.Jobs = jobs.New(st.DB())
	admin.OnUserLifecycle = func(ctx context.Context, e plugin.UserLifecycleEvent) {
		pluginHost.EmitUserLifecycle(ctx, e)
		if e.Kind == "deleted" {
			pluginHost.EmitEvent(ctx, "user.deleted", plugin.EventPayload{UserID: e.UserID})
		}
	}
	admin.Version = version
	// Union hot settings (2026-10-24 拍板「热切换」): the three hot keys apply
	// on save without a restart; the rest of the union group is marked
	// restart-only in the settings panel.
	admin.OnSettingChanged = func(key, value string) {
		switch key {
		case "union_enabled":
			ustack.setEnabled(context.Background(), value == "1")
			log.Printf("union: hot toggle -> %s", value)
		case "union_api_root":
			if err := ustack.setAPIRoot(value); err != nil {
				log.Printf("union: hot api_root rejected: %v", err)
			}
		case "union_member_key":
			ustack.setMemberKey(value)
		case "cdn_enabled", "cdn_headers":
			applyCDN()
			log.Printf("cdn: hot apply %s=%s", key, value)
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
		"union_oauth2_sig_private_key": "",
		"union_oauth2_sig_public_key":  "",
		// A1 找回密码: SMTP env fallbacks shown as panel placeholders
		"smtp_host": os.Getenv("SKIN_SMTP_HOST"),
		"smtp_port": envOr("SKIN_SMTP_PORT", "465"),
		"smtp_user": os.Getenv("SKIN_SMTP_USER"),
		"smtp_pass": os.Getenv("SKIN_SMTP_PASS"),
		"smtp_from": os.Getenv("SKIN_SMTP_FROM"),
	}
	// Admin user management shares the credential-change revoke semantics.
	admin.OnUserCredentialChange = func(uid int64) {
		if oidcProvider != nil {
			if err := oidcProvider.RevokeUserAllTokens(context.Background(), uid); err != nil {
				log.Printf("admin credential change: OIDC revoke uid=%d: %v", uid, err)
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
					log.Printf("union sync delete failed uid=%d uuid=%s name=%s: %v", uid, uuid, name, err)
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
				log.Printf("union sync delete failed (admin player) uuid=%s name=%s: %v", uuid, name, err)
			}
		}()
	}
	admin.MountAppearance(adminapi.AppearanceDeps{SEO: seoEng, Appear: appearEng, Themes: themeReg})
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
	root.Handle("/api/yggdrasil/", yggAPI.Handler())
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
	root.HandleFunc("GET /avatar/player/{name}", av.handleAvatarPlayer)
	root.HandleFunc("GET /avatar/user/{uid}", av.handleAvatarUser)
	root.HandleFunc("GET /avatar/hash/{hash}", av.handleAvatarHash)
	root.HandleFunc("GET /avatar/{tid}", av.handleAvatarTID)
	root.HandleFunc("GET /preview/hash/{hash}", av.handlePreviewHash)
	root.HandleFunc("GET /preview/{tid}", av.handlePreviewTID)
	// Phase 3 public surface: health probe, SEO outputs, theme assets.
	root.Handle("GET /healthz", healthzHandler(st))
	root.Handle("GET /sitemap.xml", sitemapHandler(seoEng, st, cfg.BaseURL))
	root.Handle("GET /robots.txt", robotsHandler(seoEng))
	root.Handle("GET /llms.txt", llmsHandler(seoEng))
	root.Handle("GET /theme-assets/{id}/{file}", themeAssetHandler(themeReg))
	root.Handle("/plugin-assets/", pluginHost.AssetHandler())
	// Public server-rendered texture detail page (2026-10-25 拍板: 皮肤详情
	// 是搜索引擎的内容页). Registered on the root mux so it beats the SPA
	// catch-all below; private textures render only for their owner (noindex).
	root.HandleFunc("GET /skinlib/{tid}", texturePageHandler(st, seoEng, appearEng, cfg.SiteName, cfg.BaseURL, pluginHost.TextureURL, pluginHost.SlotHTMLContext))
	// Public landing page data (counts + featured library cards + login state).
	landingFn := func(r *http.Request) landingData {
		ctx := r.Context()
		d := landingData{SiteName: cfg.SiteName}
		// 已登录访客的 hero CTA 换成进站动作（2026-10-27 逻辑修复）
		if c, err := r.Cookie("skin_session"); err == nil {
			if _, err := st.SessionUser(ctx, c.Value); err == nil {
				d.LoggedIn = true
			}
		}
		if n, err := st.CountUsers(ctx); err == nil {
			d.Users = n
		}
		if n, err := st.CountPlayers(ctx); err == nil {
			d.Players = n
		}
		if n, err := st.CountTextures(ctx); err == nil {
			d.Textures = n
		}
		if rows, _, err := st.SkinlibList(ctx, store.SkinlibOpts{Sort: "likes", Page: 1, PerPage: 8}); err == nil {
			for _, r := range rows {
				if !r.Public {
					continue
				}
				// Same split as the skinlib UI: skins preview rendered, capes
				// flat texture (a cape through the body renderer 404s).
				u := "/preview/" + strconv.FormatInt(r.TID, 10)
				if r.Type == "cape" {
					u = "/textures/" + r.Hash
				}
				d.Featured = append(d.Featured, landingTexture{TID: r.TID, Name: r.Name, URL: u})
			}
		}
		return d
	}
	spa := spaHandler("web/dist", seoEng, appearEng, cfg.SiteName, landingFn, pluginHost.HasUIPath)
	if oidcProvider != nil {
		spa = oidcProvider.Wrap(spa)
	}
	// Plugin routes (read-only, GET/HEAD) resolve per request from the live
	// registry so enable/disable takes effect without touching the mux.
	root.Handle("/api/plugins/", pluginHost.Handler())
	root.Handle("/", spa)

	addr := cfg.Listen
	log.Printf("skin-nexus %s listening on %s (base %s)", version, addr, cfg.BaseURL)

	srv := newHTTPServer(addr, pub.WrapRoot(root))
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe() }()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	case sig := <-stop:
		// Docker/systemd stop sends SIGTERM: drain in-flight requests instead
		// of dropping uploads and texture downloads mid-response. 8s stays
		// inside Docker's default 10s stop timeout before SIGKILL.
		log.Printf("received %s, shutting down", sig)
		_ = pluginHost.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("graceful shutdown: %v", err)
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

// spaHandler serves web/dist if present, else a placeholder page. Every HTML
// serve passes through the SEO + appearance injection layers (phase 3).
// Only concrete asset files are served raw: os.Stat on "" resolves to the dist
// directory itself, so "exists on disk" must not mean "static file" — root and
// /index.html need the injected index.html too (2026-10-25 fix: "/" was served
// raw and skipped SEO/theme/custom injection entirely).
// The exact root path serves the public server-rendered landing page instead
// of the SPA shell (2026-10-25 拍板: exposure for first-time visitors/crawlers).
// isKnownSPAPath whitelists the SPA shell routes; anything else is a true 404.
func isKnownSPAPath(p string, uiPath func(string) bool) bool {
	if strings.HasPrefix(p, "ext/") {
		return uiPath != nil && uiPath(p)
	}
	switch p {
	case "index.html", "login", "register", "forgot-password", "reset-password", "device",
		"setup", "skinlib", "user/skinlib", "user/closet", "user/profile",
		"user/players", "user/dashboard":
		return true
	}
	return strings.HasPrefix(p, "user/")
}

func spaHandler(dir string, seoEng *seo.Engine, appearEng *appearance.Engine, siteName string, landing func(*http.Request) landingData, uiPath func(string) bool) http.Handler {
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		fs := http.FileServer(http.Dir(dir))
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := strings.TrimPrefix(r.URL.Path, "/")
			if p == "" && landing != nil {
				html := renderLanding(r.Context(), seoEng, appearEng, landing(r))
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = w.Write([]byte(html))
				return
			}
			inject := !strings.HasPrefix(p, "api/")
			if inject {
				if fi, err := os.Stat(dir + "/" + p); err == nil && !fi.IsDir() && p != "index.html" {
					inject = false // real asset file
				}
			}
			// Unknown SPA paths get a real 404 (2026-10-27 逻辑修复：软 404 —
			// 任何未知 URL 都曾 200 返回登录卡，爬虫把它们当内容页收进索引).
			// Known shell routes: auth flows, device verification, the public
			// skinlib list and the /user/* launcher-deep-link family.
			if inject && !isKnownSPAPath(p, uiPath) {
				writeTexNotFound(r.Context(), w, siteName, appearEng, "页面不存在。")
				return
			}
			if inject {
				raw, err := os.ReadFile(dir + "/index.html")
				if err != nil {
					http.Error(w, "index missing", http.StatusInternalServerError)
					return
				}
				html := string(raw)
				html = seoEng.RenderHeadWith(r.Context(), html, pageMetaFor(p))
				html = appearEng.Render(r.Context(), html)
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = w.Write([]byte(html))
				return
			}
			fs.ServeHTTP(w, r)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<h1>SkinNexus</h1><p>web/dist not built yet. See docs/.</p>"))
	})
}

// healthzHandler is the container health probe (DB round-trip).
func healthzHandler(st *store.Mysql) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := st.Health(r.Context()); err != nil {
			http.Error(w, "unhealthy", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
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
		// bounded: at most 5000 public texture pages
		if rows, err := st.PublicTextureIDs(r.Context(), 5000); err == nil {
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
		w.Header().Set("Cache-Control", "public, max-age=86400")
		http.ServeFile(w, r, full)
	})
}
