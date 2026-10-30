// Package adminapi serves the operator surface: user list, texture list,
// weak-hash dashboard, signing key fingerprint, audit log.
//
// Admin gating (blessing parity, 2026-10-24): EITHER a web session whose
// account carries the legacy admin permission (users.permission > 0) — the
// admin logs in normally and the panel works without ceremony — OR the
// shared admin token (X-Admin-Token header, SKIN_ADMIN_TOKEN env) as the
// machine/API fallback (empty env = token path locked).
package adminapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"strconv"

	"skin-nexus/server/internal/jobs"
	"skin-nexus/server/internal/plugin"
	"skin-nexus/server/internal/store"
	"skin-nexus/server/internal/ygg"
)

type Backend struct {
	Store          *store.Mysql
	Ygg            *ygg.Service
	AdminTok       string
	union          UnionDeps       // nil until MountUnion (module disabled = no routes)
	sharedClientID string          // guard: shared launcher client cannot be deleted
	appearance     *AppearanceDeps // nil until MountAppearance
	plugins        *PluginsDeps    // nil until MountPlugins (no plugin routes)
	// Jobs is the durable plugin job queue (nil = jobs API disabled).
	Jobs *jobs.Store
	// SettingsDefaults carries env-derived fallbacks for the settings panel
	// (injected by main from config).
	SettingsDefaults map[string]string
	// OnUserCredentialChange fires on admin password reset (main wires OIDC
	// grant revocation — same semantics as the self-service change).
	OnUserCredentialChange func(uid int64)
	// OnUserDeleted fires after a successful user deletion with the (uuid,
	// name) pairs of the account's players, read BEFORE the transactional
	// delete (players/uuid_map rows are gone afterwards). main wires the
	// best-effort union hub announcement (webapi create-sync precedent).
	OnUserDeleted func(uid int64, players [][2]string)
	// OnUserLifecycle mirrors admin-driven deletions into the plugin host's
	// lifecycle event stream (nil = no plugins).
	OnUserLifecycle func(ctx context.Context, e plugin.UserLifecycleEvent)
	// OnPlayerDeleted fires after an admin player deletion with the
	// (uuid, name) captured before the delete — main wires the same
	// best-effort union hub announcement as OnUserDeleted (B3).
	OnPlayerDeleted OnPlayerDeletedHook
	// OnSettingChanged fires after a successful settings PUT with the key
	// and stored value — main wires the union hot-apply (union_enabled /
	// union_api_root / union_member_key take effect without a restart,
	// 2026-10-24 拍板「热切换」).
	OnSettingChanged func(key, value string)
	// Version is the build version injected by main (status page display).
	Version string
	// StorageBytes sums the texture files on disk (main injects a walk of
	// the textures dir — the textures table has no size column and the
	// schema is frozen; nil = storage card shows "—").
	StorageBytes func() (int64, error)
}

func New(st *store.Mysql, yggSvc *ygg.Service) *Backend {
	return &Backend{Store: st, Ygg: yggSvc, AdminTok: os.Getenv("SKIN_ADMIN_TOKEN")}
}

// jsonDecode is a one-shot JSON body decoder shared by admin handlers.
func jsonDecode(r *http.Request, v interface{}) error {
	return json.NewDecoder(r.Body).Decode(v)
}

func (b *Backend) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/admin/v1/overview", b.auth(b.handleOverview))
	mux.HandleFunc("GET /api/admin/v1/status", b.auth(b.handleStatus))
	mux.HandleFunc("GET /api/admin/v1/users", b.auth(b.handleUsers))
	mux.HandleFunc("GET /api/admin/v1/textures", b.auth(b.handleTextures))
	mux.HandleFunc("GET /api/admin/v1/audit", b.auth(b.handleAudit))
	// Access/communication logs (2026-10-26 运维观测面): web + launcher
	// logins, ygg server traffic, union traffic — filterable/paged.
	mux.HandleFunc("GET /api/admin/v1/access-logs", b.auth(b.handleAccessLogs))
	// Invitation codes (1:1 live blessing plugin admin surface).
	mux.HandleFunc("GET /api/admin/v1/invitation-codes", b.auth(b.handleInvitationList))
	mux.HandleFunc("POST /api/admin/v1/invitation-codes", b.auth(b.handleInvitationGenerate))
	mux.HandleFunc("PUT /api/admin/v1/invitation-codes", b.auth(b.handleInvitationEdit))
	mux.HandleFunc("GET /api/admin/v1/settings", b.auth(b.handleSettingsGet))
	mux.HandleFunc("PUT /api/admin/v1/settings", b.auth(b.handleSettingsPut))
	mux.HandleFunc("POST /api/admin/v1/users/{id}/password", b.auth(b.handleUserSetPassword))
	mux.HandleFunc("PUT /api/admin/v1/users/{id}/email", b.auth(b.handleUserSetEmail))
	mux.HandleFunc("PUT /api/admin/v1/users/{id}/nickname", b.auth(b.handleUserSetNickname))
	mux.HandleFunc("PUT /api/admin/v1/users/{id}/verification", b.auth(b.handleUserSetVerification))
	mux.HandleFunc("PUT /api/admin/v1/users/{id}/permission", b.auth(b.handleUserSetPermission))
	mux.HandleFunc("DELETE /api/admin/v1/users/{id}", b.auth(b.handleUserDelete))
	// Player management (B3, blessing 玩家管理 parity).
	mux.HandleFunc("GET /api/admin/v1/players", b.auth(b.handleAdminPlayers))
	mux.HandleFunc("PUT /api/admin/v1/players/{pid}/name", b.auth(b.handleAdminPlayerRename))
	mux.HandleFunc("PUT /api/admin/v1/players/{pid}/owner", b.auth(b.handleAdminPlayerOwner))
	mux.HandleFunc("PUT /api/admin/v1/players/{pid}/textures", b.auth(b.handleAdminPlayerTextures))
	mux.HandleFunc("DELETE /api/admin/v1/players/{pid}", b.auth(b.handleAdminPlayerDelete))
	if b.union.Status != nil {
		mux.HandleFunc("GET /api/admin/v1/union/status", b.auth(b.unionGate(b.handleUnionStatus)))
		mux.HandleFunc("POST /api/admin/v1/union/rotation/resume", b.auth(b.unionGate(b.handleUnionRotationResume)))
		// B5.3 ops buttons + B5.4 blacklist proxy (each dep nil-checks by
		// route registration: they all ride the same MountUnion wiring).
		if b.union.ForceRotation != nil {
			mux.HandleFunc("POST /api/admin/v1/union/ops/key-update", b.auth(b.unionGate(b.handleUnionKeyUpdate)))
			mux.HandleFunc("POST /api/admin/v1/union/ops/serverlist", b.auth(b.unionGate(b.handleUnionServerList)))
			mux.HandleFunc("POST /api/admin/v1/union/ops/roster-sync", b.auth(b.unionGate(b.handleUnionRosterSync)))
			mux.HandleFunc("POST /api/admin/v1/union/ops/diagnose", b.auth(b.unionGate(b.handleUnionDiagnose)))
			mux.HandleFunc("GET /api/admin/v1/union/blacklist", b.auth(b.unionGate(b.handleUnionBlacklistQuery)))
			mux.HandleFunc("POST /api/admin/v1/union/blacklist", b.auth(b.unionGate(b.handleUnionBlacklistAdd)))
			mux.HandleFunc("PUT /api/admin/v1/union/blacklist/{id}/invalidate", b.auth(b.unionGate(b.handleUnionBlacklistRelease)))
			mux.HandleFunc("DELETE /api/admin/v1/union/blacklist/{id}", b.auth(b.unionGate(b.handleUnionBlacklistDelete)))
		}
	}
	// OIDC client management (works regardless of module flag; the shared
	// launcher client deletion is guarded by sharedClientID).
	mux.HandleFunc("GET /api/admin/v1/oidc/clients", b.auth(b.handleOIDCClientsList))
	mux.HandleFunc("POST /api/admin/v1/oidc/clients", b.auth(b.handleOIDCClientsCreate))
	mux.HandleFunc("DELETE /api/admin/v1/oidc/clients/{id}", b.auth(b.handleOIDCClientsDelete))
	// SEO / appearance / themes (phase 3).
	if b.appearance != nil {
		mux.HandleFunc("GET /api/admin/v1/seo", b.auth(b.handleSEOGet))
		mux.HandleFunc("POST /api/admin/v1/seo", b.auth(b.handleSEOSave))
		mux.HandleFunc("POST /api/admin/v1/seo/oneclick", b.auth(b.handleSEOOneClick))
		mux.HandleFunc("GET /api/admin/v1/appearance", b.auth(b.handleAppearanceGet))
		mux.HandleFunc("POST /api/admin/v1/appearance", b.auth(b.handleAppearanceSave))
		mux.HandleFunc("GET /api/admin/v1/themes", b.auth(b.handleThemesList))
	}
	// Plugin host (v0.3.0): list for any admin, mutations super-admin only.
	if b.plugins != nil {
		mux.HandleFunc("GET /api/admin/v1/plugins", b.auth(b.handlePluginsList))
		mux.HandleFunc("GET /api/admin/v1/plugins/{name}/config", b.auth(b.handlePluginConfigGet))
		mux.HandleFunc("POST /api/admin/v1/plugins/guards/bypass", b.auth(b.handlePluginGuardBypass))
		mux.HandleFunc("POST /api/admin/v1/plugins/{name}/rollback", b.auth(b.handlePluginRollback))
		mux.HandleFunc("DELETE /api/admin/v1/plugins/{name}", b.auth(b.handlePluginUninstall))
		mux.HandleFunc("GET /api/admin/v1/market/catalog", b.auth(b.handleMarketCatalog))
		mux.HandleFunc("POST /api/admin/v1/market/plan", b.auth(b.handleMarketPlan))
		mux.HandleFunc("POST /api/admin/v1/market/install", b.auth(b.handleMarketInstall))
		mux.HandleFunc("POST /api/admin/v1/market/upload", b.auth(b.handleMarketUpload))
		mux.HandleFunc("PUT /api/admin/v1/plugins/{name}/config", b.auth(b.handlePluginConfigSave))
		mux.HandleFunc("POST /api/admin/v1/plugins/{name}/{action}", b.auth(b.handlePluginAction))
	}
	if b.Jobs != nil {
		mux.HandleFunc("GET /api/admin/v1/jobs", b.auth(b.handleJobsList))
		mux.HandleFunc("GET /api/admin/v1/jobs/{id}", b.auth(b.handleJobGet))
		mux.HandleFunc("POST /api/admin/v1/jobs/{id}/cancel", b.auth(b.handleJobCancel))
		mux.HandleFunc("POST /api/admin/v1/jobs/{id}/resume", b.auth(b.handleJobResume))
	}
	// 1 MiB body cap (review B P2) — admin JSON endpoints included.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.URL.Path != "/api/admin/v1/market/upload" {
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		}
		mux.ServeHTTP(w, r)
	})
}

// sessionCookieName is the site session cookie webapi issues (same name the
// oidc package reads — see oidc.sessionCookie).
const sessionCookieName = "skin_session"

// actorUIDKey carries the acting admin uid into handlers so audit rows are
// attributed to the real account on session-authenticated calls; the token
// path keeps uid 0 (system/API actor).
type actorUIDKey struct{}

// actorUID returns the session-authenticated admin's uid (0 = token/API path).
func actorUID(r *http.Request) int64 {
	uid, _ := r.Context().Value(actorUIDKey{}).(int64)
	return uid
}

// sessionAdmin resolves the caller's web session to an admin account
// (users.permission > 0 — banned/normal sessions do not qualify). Package
// var per the oidc.sessionCookie precedent so the middleware is testable
// without a DB; the default body is store-backed (queries live in store:
// SessionUser + UserPermission).
var sessionAdmin = func(r *http.Request, st *store.Mysql) (int64, bool) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return 0, false
	}
	uid, err := st.SessionUser(r.Context(), c.Value)
	if err != nil || uid <= 0 {
		return 0, false
	}
	perm, err := st.UserPermission(r.Context(), uid)
	if err != nil || perm <= 0 {
		return 0, false
	}
	return uid, true
}

// auth enforces admin access: EITHER the shared admin token (constant-time
// compare; empty token = token path locked, unchanged behavior) OR a web
// session of an admin account (blessing parity). A non-admin session gets
// the same 403 as a wrong token.
func (b *Backend) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("X-Admin-Token")
		if b.AdminTok != "" && subtle.ConstantTimeCompare([]byte(got), []byte(b.AdminTok)) == 1 {
			next(w, r) // machine/API fallback: audit actor stays uid 0
			return
		}
		if uid, ok := sessionAdmin(r, b.Store); ok {
			next(w, r.WithContext(context.WithValue(r.Context(), actorUIDKey{}, uid)))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"Forbidden","message":"admin token invalid"}`))
	}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// handleOverview: counters powering the dashboard header.
func (b *Backend) handleOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	weak, _ := b.Store.WeakHashCount(ctx)
	users, _ := b.Store.CountUsers(ctx)
	players, _ := b.Store.CountPlayers(ctx)
	textures, _ := b.Store.CountTextures(ctx)
	// Storage usage (B1): textures dir walk injected by main; a failure
	// degrades to null (UI shows "—") and never blocks the counters.
	var storageBytes interface{}
	if b.StorageBytes != nil {
		if n, err := b.StorageBytes(); err == nil {
			storageBytes = n
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"users": users, "players": players, "textures": textures,
		"weakHashUsers":      weak, // 「弱哈希余量」看板核心指标
		"signKeyFingerprint": b.Ygg.SignerFingerprint(),
		"storageBytes":       storageBytes,
	})
}

// handleStatus: read-only runtime facts for the 运行状态 page — build
// version, Go version, MySQL server version, applied-migration watermark,
// and which optional modules are mounted (union / appearance / oidc are
// wiring-time facts, so no extra deps needed beyond the existing fields).
func (b *Backend) handleStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	mysqlVer, err := b.Store.MySQLVersion(ctx)
	if err != nil {
		mysqlVer = "?" // degraded display; the page still renders
	}
	migVer, err := b.Store.MigrationVersion(ctx)
	if err != nil {
		migVer = "?"
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"version":      b.Version,
		"goVersion":    runtime.Version(),
		"mysqlVersion": mysqlVer,
		"migration":    migVer,
		"modules": map[string]bool{
			"union":      b.union.Status != nil,
			"appearance": b.appearance != nil,
		},
		"signKeyFingerprint": b.Ygg.SignerFingerprint(),
	})
}

func (b *Backend) handleUsers(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	rows, total, err := b.Store.AdminUsers(r.Context(), r.URL.Query().Get("q"), page, 50)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"users": rows, "total": total, "page": page})
}

func (b *Backend) handleTextures(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	rows, total, err := b.Store.AdminTextures(r.Context(), page, 50)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"textures": rows, "total": total, "page": page})
}

func (b *Backend) handleAudit(w http.ResponseWriter, r *http.Request) {
	rows, err := b.Store.RecentAudit(r.Context(), 100)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"audit": rows})
}

// handleAccessLogs pages the observability table (access_logs): web +
// launcher logins, Yggdrasil server traffic, Union traffic. Filters: kind
// (exact, "" = all) and q (substring over who/ip/detail).
func (b *Backend) handleAccessLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	rows, total, err := b.Store.ListAccessLogs(r.Context(), q.Get("kind"), q.Get("q"), page, 50)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"logs": rows, "total": total, "page": page})
}
