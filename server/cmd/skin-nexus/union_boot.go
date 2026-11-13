package main

// union_boot.go — MUA Union 成员站模块装配（options 优先配置）。
//
// 2026-10-24 拍板：union 配置对齐 blessing yggdrasil-connect 存储面——
// option 表优先、env 兜底（callbacks.php:91-150 全走 option()）；新增
// `union_enabled` option（老站用插件启停表达、无 option 行）作热开关：路由
// 常驻、每请求门禁，关闭即 404、签名即回本地密钥。OAuth2 用户信息签名密钥对
// 首启自动生成入库（callbacks.php:147-150 同语义，RSA-4096）；hub 验签公钥
// 每次现取带缓存（UnionHostVerify.php:67 同语义），pinned 文件仅作现取失败
// 兜底。迁移导入（union_member_key / union_oauth2_sig_* 随 options 同构直
// 迁）因此零手工搬移。

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"skin-nexus/server/internal/config"
	"skin-nexus/server/internal/outbound"
	"skin-nexus/server/internal/store"
	"skin-nexus/server/internal/union"
	"skin-nexus/server/internal/ygg"
)

// unionDefaultAPIRoot is the blessed default hub (blessing
// yggdrasil-connect callbacks.php:93 default('union_api_root')).
const unionDefaultAPIRoot = "https://skin.mualliance.ltd/api/union"

// unionStack owns the live union objects. on mirrors the union_enabled
// option; every route gates on it and the hot settings keys land through
// setEnabled/setAPIRoot/setMemberKey without a restart (2026-10-24 拍板
// 「热切换」). A failed build leaves the zero value: Enabled()==false and
// every route 404s.
type unionStack struct {
	on     atomic.Bool
	out    *union.Outbound
	signer *union.UnionSigner
	api    *union.API
	grant  *union.Grantor
	names  *union.MemberNames // peer site names, read through guarded egress
	cfg    union.Config       // resolved config, admin status card reporting
}

// Enabled reports the live module state (nil-safe).
func (u *unionStack) Enabled() bool { return u != nil && u.on.Load() }

// gate 404s every union route while the module is switched off — the
// hot-toggle face of the old "plugin disabled = zero surface" behavior.
func (u *unionStack) gate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !u.Enabled() {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// setEnabled is the union_enabled hot toggle (admin settings PUT).
func (u *unionStack) setEnabled(ctx context.Context, on bool) {
	if u == nil || u.signer == nil {
		return
	}
	u.on.Store(on)
	u.out.SetEnabled(on)
	u.signer.SetHubMode(on) // trust statement: off = shared hub key unused
	if on {
		go u.signer.StartRotation(ctx, 12*time.Minute) // no-op when already running
	}
}

// setAPIRoot / setMemberKey apply the other hot settings keys.
func (u *unionStack) setAPIRoot(root string) error {
	if u == nil || u.out == nil {
		return errors.New("union: not built")
	}
	return u.out.SetAPIRoot(root)
}

func (u *unionStack) setMemberKey(key string) {
	if u == nil {
		return
	}
	if u.out != nil {
		u.out.SetMemberKey(key)
	}
	if u.grant != nil {
		u.grant.SetMemberKey(key)
	}
}

// optOrEnv resolves one union setting: non-empty option first (blessing
// parity — the options table is the config surface), env fallback second.
func optOrEnv(ctx context.Context, st *store.Mysql, key, envVal string) string {
	if v, ok, err := st.GetOption(ctx, key); err == nil && ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return envVal
}

// optOrEnvBool resolves a boolean setting stored "1"/"0" (blessing stores
// bool options as "1"/"0"; the settings panel normalizes the same way).
func optOrEnvBool(ctx context.Context, st *store.Mysql, key string, envVal bool) bool {
	if v, ok, err := st.GetOption(ctx, key); err == nil && ok && strings.TrimSpace(v) != "" {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "on", "yes":
			return true
		case "0", "false", "off", "no":
			return false
		}
	}
	return envVal
}

// ensureSigKeypair resolves the OAuth2 userinfo signing pair: stored options
// first (migrated values land verbatim), SKIN_UNION_OAUTH2_SIG_KEY file second
// (the 2026-10-24 manual bridge — its content is seeded INTO the options so
// the bind mount becomes removable), RSA-4096 auto-generation last (callbacks
// php:147-150 first-boot semantics). Values never reach the log.
func ensureSigKeypair(ctx context.Context, st *store.Mysql, keyFilePath string) (string, string) {
	priv, _, _ := st.GetOption(ctx, "union_oauth2_sig_private_key")
	pub, _, _ := st.GetOption(ctx, "union_oauth2_sig_public_key")
	if strings.TrimSpace(priv) != "" && strings.TrimSpace(pub) != "" {
		return priv, pub
	}
	if keyFilePath != "" {
		if raw, err := os.ReadFile(keyFilePath); err == nil && strings.Contains(string(raw), "BEGIN") {
			filePEM := string(raw)
			filePub := derivePublicKeyPEM(filePEM)
			if filePub != "" {
				// seed options from the extracted file, then serve from
				// options — the bind mount becomes removable.
				_ = st.SetOption(ctx, "union_oauth2_sig_private_key", filePEM)
				_ = st.SetOption(ctx, "union_oauth2_sig_public_key", filePub)
				slog.Info("union: OAuth2 sig keypair seeded into options from key file")
				return filePEM, filePub
			}
		}
	}
	k, err := rsa.GenerateKey(rand.Reader, 4096) // ygg_generate_rsa_keys: 4096 bits
	if err != nil {
		slog.Warn("union: sig keypair generation failed", "error", err)
		return "", ""
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return "", ""
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&k.PublicKey)
	if err != nil {
		return "", ""
	}
	privPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER}))
	pubPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))
	_ = st.SetOption(ctx, "union_oauth2_sig_private_key", privPEM)
	_ = st.SetOption(ctx, "union_oauth2_sig_public_key", pubPEM)
	slog.Info("union: OAuth2 sig keypair auto-generated into options (RSA-4096)")
	return privPEM, pubPEM
}

// derivePublicKeyPEM extracts the SubjectPublicKeyInfo PEM from a private
// key PEM (PKCS#8 or PKCS#1).
func derivePublicKeyPEM(privPEM string) string {
	block, _ := pem.Decode([]byte(privPEM))
	if block == nil {
		return ""
	}
	var priv *rsa.PrivateKey
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rk, ok := k.(*rsa.PrivateKey); ok {
			priv = rk
		}
	} else if rk, err2 := x509.ParsePKCS1PrivateKey(block.Bytes); err2 == nil {
		priv = rk
	}
	if priv == nil {
		return ""
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return ""
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))
}

// newUnionStack assembles the module from options-first config. It runs
// unconditionally (hot toggle needs the objects alive) and never Fatals:
// broken config degrades to an inert stack (Enabled()==false → 404s).
func newUnionStack(ctx context.Context, st *store.Mysql, cfg *config.Config, version string, fallback ygg.Signer) *unionStack {
	u := &unionStack{}

	// --- options-first config resolution (blessing option key names) ---
	apiRoot := optOrEnv(ctx, st, "union_api_root", cfg.UnionAPIRoot)
	if apiRoot == "" {
		apiRoot = unionDefaultAPIRoot
	}
	memberKey := optOrEnv(ctx, st, "union_member_key", cfg.UnionMemberKey)
	allowUpdate := optOrEnvBool(ctx, st, "union_enable_update", cfg.UnionAllowUpdateBkKey)
	enableOAuth2 := optOrEnvBool(ctx, st, "union_enable_oauth2", true)
	queryEmail := optOrEnv(ctx, st, "union_queryemail_mode", cfg.UnionQueryEmailMode)
	if queryEmail == "" {
		queryEmail = union.QueryEmailModeFull
	}
	sigPriv, sigPub := ensureSigKeypair(ctx, st, cfg.UnionSigPrivateKey)
	enabled := optOrEnvBool(ctx, st, "union_enabled", cfg.UnionEnabled)

	// Pinned hub key stays an optional fallback only (2026-10-24 拍板「现取」:
	// UnionHostVerify.php:67 semantics — fetch per request, cache briefly).
	var pinnedHubKey string
	if cfg.UnionHostPublicKeyPath != "" {
		if raw, err := os.ReadFile(cfg.UnionHostPublicKeyPath); err == nil {
			pinnedHubKey = string(raw)
		}
	}

	audit := func(event string, detail map[string]any) {
		slog.Info("union audit", "event", event, "detail", detail)
		st.InsertAudit(context.Background(), 0, "union."+event, detail)
	}

	ucfg := union.Config{
		Enabled:               true, // objects live; the hot flag gates every request
		ApiRoot:               apiRoot,
		MemberKey:             memberKey,
		HostPublicKeyPEM:      pinnedHubKey,
		AllowUpdateBackendKey: allowUpdate,
		QueryEmailMode:        queryEmail,
		HubOrigin:             cfg.UnionHubOrigin,
		SigPrivateKeyPEM:      sigPriv,
		SigPublicKeyPEM:       sigPub,
		YggdrasilApiVersion:   version,
		UnionOAuth2:           enableOAuth2,
		// The two optional enabledFeatures are read per hello, the way the
		// reference reads them per hello (option() in UnionController.php:19-20):
		// the hub picks this list up from outside, so a snapshot taken at boot
		// would keep advertising a gate the owner switched off hours ago.
		EmailVerification: func(ctx context.Context) bool {
			return optOrEnvBool(ctx, st, "require_verification", cfg.RequireVerification)
		},
		InvitationCodesForUnion: func(ctx context.Context) bool {
			return optOrEnvBool(ctx, st, "invite_required", cfg.RequireInvite)
		},
	}
	if err := ucfg.Validate(); err != nil {
		slog.Warn("union: config invalid, module inert", "error", err)
		return u
	}

	out, err := union.NewOutbound(ucfg, nil)
	if err != nil {
		slog.Warn("union: outbound build failed, module inert", "error", err)
		return u
	}
	usigner := union.NewUnionSigner(ucfg, out, fallback, audit)
	grant, err := union.NewGrantor(ucfg)
	if err != nil {
		slog.Warn("union: grantor build failed, module inert", "error", err)
		return u
	}

	// T3.3: member data hooks — profile sync list, UUID remap, queryemail.
	hooks := union.Hooks{
		OnServerList: func(list any, version string) {
			_ = st.SetOption(context.Background(), "union_server_list_version", version)
			// Legacy skinDomains formula: member hosts feed the texture
			// whitelist (options-cached like union_server_list).
			if hosts, ok := unionSkinDomains(list); ok {
				_ = st.SetOption(context.Background(), "union_skin_domains", strings.Join(hosts, ","))
			}
		},
		OnMemberKeyRotated: func(newKey string) {
			if err := st.SetOption(context.Background(), "union_member_key", newKey); err != nil {
				audit("member_key_persist_failed", map[string]any{"error": err.Error()})
				return
			}
			audit("member_key_rotated", nil)
		},
		OnRemapUUID: func(remap map[string]string) error {
			applied, skipped, err := st.RemapUUIDs(context.Background(), remap)
			if err != nil {
				return err
			}
			// true counts (MUA review P1: the old audit logged len(batch)
			// regardless of rows actually moved)
			st.InsertAudit(context.Background(), 0, "union_remapuuid_detail",
				map[string]interface{}{"applied": applied, "skipped": skipped})
			return nil
		},
		ProfileListFunc: func() (map[string]string, error) {
			return st.ProfileNameByUUID(context.Background())
		},
		QueryEmailFunc: func(name string) (string, bool, error) {
			return st.EmailByPlayerName(context.Background(), name)
		},
	}
	unionAPI, err := union.NewAPI(ucfg, out, usigner, grant, hooks, audit)
	if err != nil {
		slog.Warn("union: api build failed, module inert", "error", err)
		return u
	}

	// hub verify key: live fetch with cache (UnionHostVerify.php:67), pinned
	// PEM as fetch-failure fallback (wire unchanged — key source only).
	unionAPI.Verifier().SetHostKeySource(func() (string, error) {
		cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return out.HostPublicKey(cctx)
	})

	// The nonce replay cache is process-local; without a janitor it only shrinks
	// lazily on writes, so a quiet member site would hold expired nonces forever.
	// Sweep on a timer until the boot context is cancelled at shutdown.
	unionAPI.Verifier().StartJanitor(ctx.Done(), 30*time.Second)

	// Signing-key restart continuity (2026-10-23 hardening): every landed hub
	// key is persisted to union_kv (the 0004 escape hatch built for exactly
	// this) and the self-description version follows live rotations too.
	usigner.OnKeyPersist = func(pemStr, version string) error {
		if err := st.SetUnionKV(context.Background(), "union_private_key", pemStr); err != nil {
			return err
		}
		if err := st.SetUnionKV(context.Background(), "union_private_key_version", version); err != nil {
			return err
		}
		unionAPI.SetPrivateKeyVersion(version) // keep self-description in step
		return nil
	}
	// Restore the persisted hub key BEFORE serving: without it every restart
	// signed with the LOCAL fallback key until the first 10-15min rotation
	// landed and cross-site clients rejected our profiles in that window.
	if pemStr, ok, err := st.GetUnionKV(context.Background(), "union_private_key"); err == nil && ok && pemStr != "" {
		kvVersion, _, _ := st.GetUnionKV(context.Background(), "union_private_key_version")
		if err := usigner.RestoreKey(pemStr, kvVersion); err != nil {
			audit("signer_key_restore_failed", map[string]any{"error": err.Error()})
		} else {
			slog.Info("union: hub signing key restored (version )", "kv_version", kvVersion)
		}
	}
	// Self-description versions survive restarts (inbound.go caches start
	// empty): key version from the signer state, server list version from the
	// union_server_list_version option written by OnServerList above.
	if v := usigner.PrivateKeyVersion(); v != "" {
		unionAPI.SetPrivateKeyVersion(v)
	}
	if v, ok, err := st.GetOption(context.Background(), "union_server_list_version"); err == nil && ok && v != "" {
		unionAPI.SetServerListVersion(v)
	}

	u.out, u.signer, u.api, u.grant = out, usigner, unionAPI, grant
	u.cfg = ucfg
	// Peer site names. The hub stores none (measured: 53/53 entries arrive with
	// name empty), so each name is read from the peer's own discovery document —
	// the same source the legacy plugin reached for, from the visitor's browser
	// (servers.twig:22-33), which our own CSP forbids here. Guarded egress only,
	// always in the background, and the operator can switch it off from the panel
	// (union_resolve_names), which also stops serving names already learned.
	u.names = union.NewMemberNames(outbound.NewScoped(outbound.Policy{
		AllowUserHosts: true,
		UserHostPaths:  []string{"/api/yggdrasil"},
		MaxBytes:       128 << 10,
		Timeout:        4 * time.Second,
		UserAgent:      "SkinNexus/" + version + " (union member)",
	}), func() bool {
		return optOrEnvBool(context.Background(), st, "union_resolve_names", cfg.UnionResolveNames)
	}, union.MemberNamesOptions{})
	u.setEnabled(ctx, enabled)
	slog.Info("union: stack ready (, hub , queryemail )", "enabled", enabled, "api_root", apiRoot, "query_email", queryEmail)
	return u
}

// unionSkinDomains reads the hub's server list — an opaque any, because that
// is what the union package hands OnServerList — and returns the texture
// hosts the member stations contribute to this station's whitelist. Whatever
// comes out is read back by ygg.skinDomains as ExtraSkinDomains, so every
// entry is a host every launcher here will trust for textures: only a real
// http(s) station root may contribute. url.Parse reports a host for
// "ftp://internal:11211/" just as happily as for a station, and the hub is a
// third party, so the scheme is checked rather than assumed.
//
// The spelling the hub sent is kept, not folded here. The whitelist emitter
// publishes the lowercase spelling alongside the original (ygg.domainSpellings),
// so folding at this end would drop the one form a mixed-case member texture
// URL matches today.
//
// ok is false only when the list could not be decoded at all — the caller
// then leaves the previously stored value alone rather than clearing a
// working whitelist over a malformed payload. An empty but decodable list
// returns an empty slice on purpose: every member gone must clear the option.
func unionSkinDomains(list any) (hosts []string, ok bool) {
	raw, err := json.Marshal(list)
	if err != nil {
		return nil, false
	}
	var entries []struct {
		BSRoot string `json:"bs_root"`
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, false
	}
	hosts = make([]string, 0, len(entries))
	for _, e := range entries {
		u, err := url.Parse(strings.TrimSpace(e.BSRoot))
		if err != nil || u.Host == "" {
			continue
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			continue
		}
		hosts = append(hosts, u.Host)
	}
	return hosts, true
}
