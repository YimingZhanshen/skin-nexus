// Package webapi serves the SkinNexus SPA backend: registration, login
// (web session cookie), profile, texture upload, player management.
//
// All responses are JSON except texture file serving. Errors use
// {"error": "...", "message": "..."} — distinct from the Yggdrasil legacy
// shape (which stays frozen in the ygg package).
package webapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"skin-nexus/server/internal/accesslog"
	"skin-nexus/server/internal/account"
	"skin-nexus/server/internal/announce"
	"skin-nexus/server/internal/appearance"
	"skin-nexus/server/internal/auth"
	"skin-nexus/server/internal/plugin"
	"skin-nexus/server/internal/ratelimit"
	"skin-nexus/server/internal/store"
	"skin-nexus/server/internal/texture"
	"skin-nexus/server/internal/union"
	"skin-nexus/server/internal/ygg"
)

var (
	nickRe = regexp.MustCompile(`^[A-Za-z0-9_一-龥]{2,24}$`)
	mailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
)

// Backend bundles dependencies for the web API.
type Backend struct {
	Store    *store.Mysql
	Textures *texture.Store
	// RequireInvite forces invitationCode at registration
	// (SKIN_INVITE_REQUIRED). Default off = optional code, validated and
	// burned when supplied (legacy live semantics: required + "邀请码不能为空").
	RequireInvite bool
	// OnPasswordChange fires after a successful password change (main wires
	// OIDC grant revocation — legacy RevokeUserTokens semantics).
	OnPasswordChange func(uid int64)
	// Limiter protects login/register from guessing (nil = off; main wires
	// settings-panel thresholds; IP window + identifier failure lockout).
	Limiter  *ratelimit.Limiter
	Ygg      *ygg.Service
	MaxChars int // per-user player limit

	// A1 找回密码 (2026-10-26): mail transport + link base. SMTPDefaults
	// carries env-derived fallbacks for the panel keys (option wins per
	// request — smtpConfig reads both); empty smtp_host anywhere = feature
	// off. BaseURL builds the emailed /reset-password link; SiteName the
	// mail subject.
	SMTPDefaults map[string]string
	BaseURL      string
	SiteName     string
	// Appearance supplies the public footer config (nil = default credit).
	Appearance *appearance.Engine

	// Access is the observability sidecar (nil = off): web login/register
	// and inbound Union traffic lines for the admin panel. Best-effort —
	// a failed log write never changes an HTTP response.
	Access *accesslog.Logger

	// Plugin host hooks (v0.3.0, nil = no plugins, all fail-open).
	TextureURL        func(ctx context.Context, hash, url, typ string) string
	ProfileFilter     func(ctx context.Context, p *plugin.UserProfileFilter)
	OnTextureUploaded func(ctx context.Context, e plugin.TextureUploadedEvent)
	OnUserLifecycle   func(ctx context.Context, e plugin.UserLifecycleEvent)
	// Account is the shared character/texture service (nil = legacy paths off).
	Account *account.Service
	// v2 generic events/guards (nil = no plugins).
	CheckGuard func(ctx context.Context, kind string, p plugin.EventPayload) error
	OnEvent    func(ctx context.Context, kind string, p plugin.EventPayload)
	PluginUI   func() []plugin.UI
	// PluginHostInfo feeds capability/slot discovery to the SPA (nil = none).
	PluginHostInfo func() plugin.HostInfo
	// PluginFormData validates host-rendered plugin form fields (nil = none).
	PluginFormData func(formID string, data map[string]any) (map[string]any, error)

	// union is the MUA Union hub client (nil or disabled -> no /api/v1/union/*
	// routes exist at all: zero surface for non-alliance deployments).
	union    *union.Outbound
	unionSrv *unionServerCache
}

func (b *Backend) checkGuard(ctx context.Context, kind string, p plugin.EventPayload) error {
	if b.CheckGuard == nil {
		return nil
	}
	return b.CheckGuard(ctx, kind, p)
}

func (b *Backend) emitEvent(ctx context.Context, kind string, p plugin.EventPayload) {
	if b.OnEvent != nil {
		b.OnEvent(ctx, kind, p)
	}
}

// pluginFormData decodes {"<plugin>":{...}} and validates it against enabled
// plugins that declare formID. An empty payload still returns defaults/errors
// so required plugin fields hold for API clients too.
func (b *Backend) pluginFormData(formID string, raw map[string]json.RawMessage) (map[string]any, error) {
	if b.PluginFormData == nil {
		return nil, nil
	}
	data := make(map[string]any, len(raw))
	for name, v := range raw {
		var val any
		if err := json.Unmarshal(v, &val); err != nil {
			return nil, &plugin.FormFieldError{Plugin: name, Message: "invalid JSON"}
		}
		data[name] = val
	}
	return b.PluginFormData(formID, data)
}

func (b *Backend) pluginFormDataJSON(formID, rawJSON string) (map[string]any, error) {
	if b.PluginFormData == nil {
		return nil, nil
	}
	if len(rawJSON) > plugin.MaxPluginFormBytes {
		return nil, &plugin.FormFieldError{Message: "pluginData is too large"}
	}
	raw := map[string]json.RawMessage{}
	if strings.TrimSpace(rawJSON) != "" {
		if err := json.Unmarshal([]byte(rawJSON), &raw); err != nil {
			return nil, &plugin.FormFieldError{Message: "pluginData must be a JSON object"}
		}
	}
	return b.pluginFormData(formID, raw)
}

func writePluginFormError(w http.ResponseWriter, err error) {
	var fe *plugin.FormFieldError
	if errors.As(err, &fe) {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": fe.Error(), "plugin": fe.Plugin, "field": fe.Field,
		})
		return
	}
	writeErr(w, http.StatusBadRequest, err.Error())
}

// writeAccountErr maps the shared service's stable codes to the legacy web
// status codes so existing clients see the same behavior.
func writeAccountErr(w http.ResponseWriter, err *account.Error) {
	if err.Code == account.CodeDuplicatePub {
		body := map[string]any{"error": err.Code, "message": err.Message}
		for k, v := range err.Details {
			body[k] = v
		}
		writeJSON(w, http.StatusConflict, body)
		return
	}
	status := http.StatusInternalServerError
	switch err.Code {
	case account.CodeNotFound, account.CodeLibraryNotFnd:
		status = http.StatusNotFound
	case account.CodeLimit:
		status = http.StatusForbidden
	case account.CodeNameTaken:
		status = http.StatusConflict
	case account.CodeInvalidName, account.CodeInvalid, account.CodeTypeMismatch,
		account.CodeSourceURLReq, account.CodeSourceURLBad:
		status = http.StatusBadRequest
	case account.CodeTooLarge:
		status = http.StatusRequestEntityTooLarge
	case account.CodeNotOwned, account.CodeForbidden:
		status = http.StatusForbidden
	}
	writeErr(w, status, err.Message)
}

// SyncProfileCreate announces a new profile to the Union hub, async and
// best-effort (shared by the web handler and the account service).
func (b *Backend) SyncProfileCreate(ctx context.Context, uuid, name string) {
	if b.union == nil || !b.union.Enabled() {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := b.union.SyncProfileCreate(ctx, uuid, name); err != nil {
			b.Store.InsertAudit(context.Background(), 0, "union_sync_create_failed",
				map[string]interface{}{"uuid": uuid, "name": name, "error": err.Error()})
			log.Printf("union sync create failed uuid=%s name=%s: %v", uuid, name, err)
		}
	}()
}

// webAccessLog records a web-side entry with the request's IP/UA attached
// (nil-safe through the logger).
func (b *Backend) webAccessLog(r *http.Request, e accesslog.Entry) {
	if b.Access == nil {
		return
	}
	e.IP = ygg.ClientIP(r)
	e.UA = r.UserAgent()
	b.Access.Log(r.Context(), e)
}

// inviteRequired resolves the register gate per request: options-panel value
// (`invite_required`) wins when set, otherwise the env-derived static default.
func (b *Backend) inviteRequired(ctx context.Context) bool {
	if v, ok, err := b.Store.GetOption(ctx, "invite_required"); err == nil && ok {
		return v == "1" || v == "true"
	}
	return b.RequireInvite
}

func New(st *store.Mysql, ts *texture.Store, yggSvc *ygg.Service) *Backend {
	return &Backend{Store: st, Textures: ts, Ygg: yggSvc, MaxChars: 3}
}

const sessionCookie = "skin_session"

// Handler mounts all /api/v1 routes.
func (b *Backend) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/register", b.handleRegister)
	mux.HandleFunc("POST /api/v1/login", b.handleLogin)
	mux.HandleFunc("POST /api/v1/logout", b.handleLogout)
	mux.HandleFunc("GET /api/v1/me", b.handleMe)
	mux.HandleFunc("GET /api/v1/plugins/ui", b.handlePluginUI)
	mux.HandleFunc("GET /api/v1/me/textures", b.handleListTextures)
	mux.HandleFunc("POST /api/v1/me/textures", b.handleUploadTexture)
	mux.HandleFunc("GET /api/v1/me/players", b.handleListPlayers)
	mux.HandleFunc("POST /api/v1/me/players", b.handleCreatePlayer)
	mux.HandleFunc("PUT /api/v1/me/players/{id}/skin", b.handleSetSkin)
	mux.HandleFunc("PUT /api/v1/me/players/{id}/cape", b.handleSetCape)
	mux.HandleFunc("POST /api/v1/me/password", b.handlePasswordChange)
	// A1 找回密码: forgot is enumeration-safe (always 200 when configured);
	// reset consumes the single-use token and mirrors change's revocation.
	mux.HandleFunc("POST /api/v1/password/forgot", b.handleForgotPassword)
	mux.HandleFunc("POST /api/v1/password/reset", b.handleResetPassword)
	// User-center self-service (A 级批次): profile edit / account delete /
	// avatar / own-player rename+delete — ownership-gated, blessing parity.
	mux.HandleFunc("PUT /api/v1/me/profile", b.handleProfileUpdate)
	mux.HandleFunc("DELETE /api/v1/me", b.handleAccountDelete)
	mux.HandleFunc("PUT /api/v1/me/avatar", b.handleAvatarSet)
	mux.HandleFunc("DELETE /api/v1/me/avatar", b.handleAvatarReset)
	mux.HandleFunc("PUT /api/v1/me/players/{id}/name", b.handlePlayerRename)
	mux.HandleFunc("DELETE /api/v1/me/players/{id}", b.handlePlayerDeleteSelf)
	mux.HandleFunc("GET /api/v1/me/closet", b.handleClosetList)
	mux.HandleFunc("POST /api/v1/me/closet", b.handleClosetAdd)
	mux.HandleFunc("PUT /api/v1/me/closet/{id}", b.handleClosetRename)
	mux.HandleFunc("DELETE /api/v1/me/closet/{id}", b.handleClosetRemove)
	// Skinlib (public texture library). Like = closet add per blessing
	// semantics (user_closet is the likers pivot, Texture.php:65).
	mux.HandleFunc("GET /api/v1/skinlib", b.handleSkinlibList)
	mux.HandleFunc("POST /api/v1/skinlib/{tid}/like", b.handleSkinlibLike)
	mux.HandleFunc("DELETE /api/v1/skinlib/{tid}/like", b.handleSkinlibUnlike)
	mux.HandleFunc("PATCH /api/v1/skinlib/{tid}", b.handleSkinlibPatch)
	mux.HandleFunc("POST /api/v1/skinlib/{tid}/report", b.handleTextureReport)
	mux.HandleFunc("GET /api/v1/stats", b.handleStats)
	// Site announcement (blessing parity): public read, admin-edited via
	// the settings panel (option `announcement`). Markdown source; the
	// response carries both the raw text and server-sanitized HTML.
	mux.HandleFunc("GET /api/v1/announcement", b.handleAnnouncement)
	// Install wizard (empty-DB init): state-gated, one-way door after done.
	mux.HandleFunc("GET /api/v1/setup/status", b.handleSetupStatus)
	mux.HandleFunc("POST /api/v1/setup", b.handleSetup)
	// Public site capabilities: lets the register/forgot forms follow the
	// deployment's real configuration (invite field required vs absent,
	// email field + forgot flow available vs hidden). Config state only —
	// both flags were already observable from register-400 / forgot-503
	// behavior, so this leaks nothing user-specific.
	mux.HandleFunc("GET /api/v1/site-config", b.handleSiteConfig)
	// Union user surface: routes are ALWAYS registered and gate per
	// request, so `union_enabled` truly hot-toggles both ways. (Previously
	// registration itself was gated on Enabled() at boot — a later off→on
	// toggle left the mux empty and the bind page showed "union disabled"
	// forever. Off still answers 404 on every route: zero surface.)
	requireUnion := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if b.union == nil || !b.union.Enabled() {
				// Calls against a disabled module are a misconfiguration
				// signal worth seeing in the panel, not silence.
				b.webAccessLog(r, accesslog.Entry{Kind: accesslog.UnionIn,
					Outcome: accesslog.OutcomeDenied,
					Detail:  "module_disabled " + r.Method + " " + r.URL.Path})
				http.NotFound(w, r)
				return
			}
			sw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			h(sw, r)
			outcome := accesslog.OutcomeOK
			if sw.status >= 400 {
				outcome = accesslog.OutcomeFail
			}
			b.webAccessLog(r, accesslog.Entry{Kind: accesslog.UnionIn,
				Outcome: outcome,
				Detail:  r.Method + " " + r.URL.RequestURI() + " status=" + strconv.Itoa(sw.status)})
		}
	}
	mux.HandleFunc("GET /api/v1/union/servers", requireUnion(b.handleUnionServers))
	mux.HandleFunc("GET /api/v1/union/detail", requireUnion(b.handleUnionDetail))
	mux.HandleFunc("GET /api/v1/union/unmapped", requireUnion(b.handleUnionUnmapped))
	mux.HandleFunc("POST /api/v1/union/bind", requireUnion(b.handleUnionBind))
	mux.HandleFunc("POST /api/v1/union/bindto", requireUnion(b.handleUnionBindTo))
	mux.HandleFunc("POST /api/v1/union/unbind", requireUnion(b.handleUnionUnbind))
	mux.HandleFunc("POST /api/v1/union/remapuuid", requireUnion(b.handleUnionRemapUUID))
	return maxBody(mux)
}

// maxBody caps request bodies at 1 MiB (review B P2: unbounded JSON fed
// argon2 over arbitrarily large inputs — cheap CPU/memory amplification).
func maxBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		}
		next.ServeHTTP(w, r)
	})
}

// statusRecorder captures the response status for the union inbound log.
// The JSON union handlers only use Header/WriteHeader/Write, so the plain
// wrapper (no Flusher/Hijacker passthrough) covers the surface.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": http.StatusText(status), "message": msg})
}

// writeErrCode adds a stable machine-readable code on top of the legacy
// message shape (i18n skeleton: frontends translate `code`, fall back to
// `message`). Existing call sites keep writeErr; new errors use this.
func writeErrCode(w http.ResponseWriter, status int, msg, code string) {
	writeJSON(w, status, map[string]string{
		"error": http.StatusText(status), "message": msg, "code": code,
	})
}

// decodeJSON is a one-shot JSON body decoder.
func decodeJSON(r *http.Request, v interface{}) error {
	return json.NewDecoder(r.Body).Decode(v)
}

func (b *Backend) currentUser(r *http.Request) (int64, error) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return 0, store.ErrNotFound
	}
	return b.Store.SessionUser(r.Context(), c.Value)
}

func randomToken() string {
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

// ---- auth handlers ----

// handleSiteConfig: GET /api/v1/site-config — unauthenticated capability
// flags for the auth forms:
//   - invite_required: true → the register card shows a REQUIRED invite
//     field; false → the field is absent entirely (no dead "optional" box).
//   - mail_configured: smtpConfig != nil (same resolution as forgot: panel
//     option wins over env default). false → the register email field and
//     the whole forgot-password flow are unavailable in the UI.
//
// handlePluginUI exposes enabled plugin pages/slots/i18n plus the host's
// additive capability list. It is public and contains no secrets; pages are
// filtered by area/permission client-side and the backend still enforces
// route tiers.
func (b *Backend) handlePluginUI(w http.ResponseWriter, r *http.Request) {
	list := []plugin.UI{}
	if b.PluginUI != nil {
		list = b.PluginUI()
	}
	resp := map[string]interface{}{"plugins": list}
	if b.PluginHostInfo != nil {
		info := b.PluginHostInfo()
		resp["capabilities"] = info.Capabilities
		resp["slots"] = info.Slots
		resp["hostVersion"] = info.Version
	}
	writeJSON(w, http.StatusOK, resp)
}

func (b *Backend) handleSiteConfig(w http.ResponseWriter, r *http.Request) {
	year := time.Now().Year()
	footer := appearance.Footer{
		Links:    []appearance.FooterLink{},
		Credit:   true,
		SiteName: b.SiteName,
		Year:     year,
	}
	if b.Appearance != nil {
		footer = b.Appearance.FooterData(r.Context(), b.SiteName, b.BaseURL, year)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"invite_required": b.inviteRequired(r.Context()),
		"mail_configured": b.smtpConfig(r.Context()) != nil,
		"union_enabled":   b.union != nil && b.union.Enabled(),
		"footer":          footer,
	})
}

func (b *Backend) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Nickname string `json:"nickname"`
		Password string `json:"password"`
		// invitationCode is the legacy field name (blessing plugin register form).
		InvitationCode string `json:"invitationCode"`
		// PluginData carries host-rendered plugin fields keyed by plugin name.
		PluginData map[string]json.RawMessage `json:"pluginData"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if b.Limiter != nil && !b.Limiter.Allow("ip:"+ygg.ClientIP(r)) {
		writeErr(w, http.StatusTooManyRequests, "请求过于频繁，请稍后再试")
		return
	}
	req.Nickname = strings.TrimSpace(req.Nickname)
	if !nickRe.MatchString(req.Nickname) {
		writeErr(w, http.StatusBadRequest, "昵称需 2-24 位字母/数字/下划线/中文")
		return
	}
	if req.Email != "" && !mailRe.MatchString(req.Email) {
		writeErr(w, http.StatusBadRequest, "邮箱格式不正确")
		return
	}
	if len(req.Password) < 8 {
		writeErr(w, http.StatusBadRequest, "密码至少 8 位")
		return
	}
	// Per-IP registration cap (blessing AuthController: count >= regs_per_ip
	// rejects; default 3, 0 = off). Checked before any hashing work.
	if limit := b.Store.RegsPerIP(r.Context()); limit > 0 {
		if n, err := b.Store.CountUsersByIP(r.Context(), ygg.ClientIP(r)); err == nil && n >= limit {
			b.webAccessLog(r, accesslog.Entry{Kind: accesslog.WebRegister,
				Outcome: accesslog.OutcomeDenied, Who: req.Nickname, Detail: "ip_cap"})
			writeErr(w, http.StatusForbidden, "该 IP 注册数已达上限")
			return
		}
	}
	pluginData, perr := b.pluginFormData("auth.register", req.PluginData)
	if perr != nil {
		writePluginFormError(w, perr)
		return
	}
	guardData := map[string]any{"email": req.Email, "nickname": req.Nickname}
	if len(pluginData) > 0 {
		guardData["pluginData"] = pluginData
	}
	if err := b.checkGuard(r.Context(), "user.registration.check", plugin.EventPayload{
		IP: ygg.ClientIP(r), Data: guardData,
	}); err != nil {
		b.webAccessLog(r, accesslog.Entry{Kind: accesslog.WebRegister,
			Outcome: accesslog.OutcomeDenied, Who: req.Nickname, Detail: "plugin_guard"})
		writeErr(w, http.StatusForbidden, err.Error())
		return
	}
	hash, err := auth.HashArgon2id(req.Password)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "hash error")
		return
	}
	code := strings.TrimSpace(req.InvitationCode)
	if code == "" && b.inviteRequired(r.Context()) {
		writeErr(w, http.StatusBadRequest, "邀请码不能为空")
		return
	}
	var uid int64
	if code != "" {
		uid, err = b.Store.CreateUserWithInvitation(r.Context(), req.Email, req.Nickname, hash, b.MaxChars, code, ygg.ClientIP(r))
		if errors.Is(err, store.ErrNotFound) {
			b.webAccessLog(r, accesslog.Entry{Kind: accesslog.WebRegister,
				Outcome: accesslog.OutcomeFail, Who: req.Nickname, Detail: "bad_invite"})
			writeErr(w, http.StatusBadRequest, "邀请码无效")
			return
		}
	} else {
		uid, err = b.Store.CreateUser(r.Context(), req.Email, req.Nickname, hash, b.MaxChars, ygg.ClientIP(r))
	}
	if err != nil {
		// unique constraint violation -> nickname/email taken
		b.webAccessLog(r, accesslog.Entry{Kind: accesslog.WebRegister,
			Outcome: accesslog.OutcomeFail, Who: req.Nickname, Detail: "taken"})
		writeErr(w, http.StatusConflict, "昵称或邮箱已被占用")
		return
	}
	if err := b.issueSession(w, r, uid); err != nil {
		writeErr(w, http.StatusInternalServerError, "会话创建失败，请重试")
		return
	}
	b.webAccessLog(r, accesslog.Entry{Kind: accesslog.WebRegister,
		Outcome: accesslog.OutcomeOK, UID: uid, Who: req.Nickname,
		Detail: "invite=" + strconv.FormatBool(code != "")})
	if b.OnUserLifecycle != nil {
		b.OnUserLifecycle(r.Context(), plugin.UserLifecycleEvent{Kind: "registered", UserID: uid})
	}
	evData := map[string]any{}
	if len(pluginData) > 0 {
		evData["pluginData"] = pluginData
	}
	b.emitEvent(r.Context(), "user.registered", plugin.EventPayload{
		UserID: uid, PlayerName: req.Nickname, IP: ygg.ClientIP(r),
		Data: evData,
	})
	writeJSON(w, http.StatusCreated, map[string]interface{}{"id": uid, "nickname": req.Nickname, "permission": 0})
}

func (b *Backend) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Identifier string `json:"identifier"` // email or nickname
		Password   string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	idKey := "id:" + strings.ToLower(strings.TrimSpace(req.Identifier))
	ipKey := "ip:" + ygg.ClientIP(r)
	if b.Limiter != nil {
		if b.Limiter.Locked(idKey) {
			ra := int(b.Limiter.RetryAfter(idKey).Seconds()) + 1
			w.Header().Set("Retry-After", strconv.Itoa(ra))
			b.webAccessLog(r, accesslog.Entry{Kind: accesslog.WebLogin,
				Outcome: accesslog.OutcomeDenied, Who: req.Identifier, Detail: "locked"})
			writeErr(w, http.StatusTooManyRequests, "尝试次数过多，请稍后再试")
			return
		}
		if !b.Limiter.Allow(ipKey) {
			b.webAccessLog(r, accesslog.Entry{Kind: accesslog.WebLogin,
				Outcome: accesslog.OutcomeDenied, Who: req.Identifier, Detail: "rate_limited"})
			writeErr(w, http.StatusTooManyRequests, "请求过于频繁，请稍后再试")
			return
		}
	}
	u, err := b.Store.UserByIdentifier(r.Context(), req.Identifier)
	if err != nil {
		if b.Limiter != nil {
			b.Limiter.Fail(idKey)
		}
		b.webAccessLog(r, accesslog.Entry{Kind: accesslog.WebLogin,
			Outcome: accesslog.OutcomeFail, Who: req.Identifier, Detail: "unknown_account"})
		writeErr(w, http.StatusUnauthorized, "账号或密码错误")
		return
	}
	// Verify errors (DB/schema problems) must NOT masquerade as a wrong
	// password (review P2: at cutover a schema gap would present as
	// "everyone's password is wrong"). Only a clean !ok is 401.
	ok, err := b.Store.VerifyPassword(r.Context(), u.ID, req.Password)
	if err != nil {
		b.webAccessLog(r, accesslog.Entry{Kind: accesslog.WebLogin,
			Outcome: accesslog.OutcomeError, UID: u.ID, Who: req.Identifier, Detail: "verify_error"})
		writeErr(w, http.StatusInternalServerError, "登录校验失败，请稍后重试")
		return
	}
	if !ok {
		if b.Limiter != nil {
			b.Limiter.Fail(idKey)
		}
		b.webAccessLog(r, accesslog.Entry{Kind: accesslog.WebLogin,
			Outcome: accesslog.OutcomeFail, UID: u.ID, Who: req.Identifier, Detail: "bad_password"})
		writeErr(w, http.StatusUnauthorized, "账号或密码错误")
		return
	}
	if b.Limiter != nil {
		b.Limiter.Succeed(idKey)
	}
	if err := b.checkGuard(r.Context(), "auth.login.check", plugin.EventPayload{
		UserID: u.ID, PlayerName: u.Nickname, IP: ygg.ClientIP(r),
		Data: map[string]any{"identifier": req.Identifier},
	}); err != nil {
		b.webAccessLog(r, accesslog.Entry{Kind: accesslog.WebLogin,
			Outcome: accesslog.OutcomeDenied, UID: u.ID, Who: u.Nickname, Detail: "plugin_guard"})
		writeErr(w, http.StatusForbidden, err.Error())
		return
	}
	// Lazy rehash: legacy digest verified -> upgrade in place (official
	// strategy: login success triggers changePassword).
	if algo, err := b.Store.UserPasswordAlgo(r.Context(), u.ID); err == nil && strings.HasPrefix(algo, "legacy") {
		if nh, err := auth.HashArgon2id(req.Password); err == nil {
			_ = b.Store.RehashPassword(r.Context(), u.ID, nh)
		}
	}
	if err := b.issueSession(w, r, u.ID); err != nil {
		b.webAccessLog(r, accesslog.Entry{Kind: accesslog.WebLogin,
			Outcome: accesslog.OutcomeError, UID: u.ID, Who: u.Nickname, Detail: "session_error"})
		writeErr(w, http.StatusInternalServerError, "会话创建失败，请重试")
		return
	}
	b.webAccessLog(r, accesslog.Entry{Kind: accesslog.WebLogin,
		Outcome: accesslog.OutcomeOK, UID: u.ID, Who: u.Nickname, Detail: "web"})
	if b.OnUserLifecycle != nil {
		b.OnUserLifecycle(r.Context(), plugin.UserLifecycleEvent{Kind: "logged_in", UserID: u.ID})
	}
	b.emitEvent(r.Context(), "user.logged_in", plugin.EventPayload{
		UserID: u.ID, PlayerName: u.Nickname, IP: ygg.ClientIP(r),
	})
	// `permission` is additive: the SPA needs the admin-entry gate right away
	// and previously had to spend another /me round trip after login.
	perm := 0
	if p, err := b.Store.UserPermission(r.Context(), u.ID); err == nil {
		perm = p
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"id": u.ID, "nickname": u.Nickname, "permission": perm})
}

// issueSession persists and hands out a session cookie. The store write is
// no longer swallowed (review P2: a failed INSERT left the client holding a
// cookie for a nonexistent session — silent 401 loop); Secure follows the
// request scheme (TLS or trusted-proxy X-Forwarded-Proto).
func (b *Backend) issueSession(w http.ResponseWriter, r *http.Request, uid int64) error {
	tok := randomToken()
	exp := time.Now().Add(30 * 24 * time.Hour)
	if err := b.Store.CreateSession(context.Background(), tok, uid, exp); err != nil {
		return err
	}
	secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: tok, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Expires: exp, Secure: secure,
	})
	return nil
}

func (b *Backend) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = b.Store.DeleteSession(r.Context(), c.Value)
	}
	secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: secure,
	})
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (b *Backend) handleMe(w http.ResponseWriter, r *http.Request) {
	uid, err := b.currentUser(r)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "not logged in")
		return
	}
	u, err := b.Store.UserByID(r.Context(), uid)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "not logged in")
		return
	}
	limit := b.MaxChars
	if n, err := b.Store.UserPlayerLimit(r.Context(), uid); err == nil && n > 0 {
		limit = n
	}
	// blessing parity: raw legacy permission (0 normal, >0 admin, -1 banned)
	// — the frontend gates the admin panel entry on it. Additive field.
	perm := 0
	if p, err := b.Store.UserPermission(r.Context(), uid); err == nil {
		perm = p
	}
	// User-center dashboard extras (A 级批次): player count vs limit for the
	// usage card, storage bytes for "存储 1/1MB"-style display, avatar hash
	// for the profile page. Storage = sum of own texture file sizes (small
	// N per user; missing file = skipped, not fatal).
	playerCount := 0
	if ps, err := b.Store.PlayersByUID(r.Context(), uid); err == nil {
		playerCount = len(ps)
	}
	storageBytes := int64(0)
	if texs, err := b.Store.ListTextures(r.Context(), uid); err == nil {
		for _, tx := range texs {
			if p, err := b.Textures.Path(tx.Hash); err == nil {
				if fi, err := statFile(p); err == nil {
					storageBytes += fi
				}
			}
		}
	}
	avatarHash := ""
	if h, err := b.Store.UserAvatarHash(r.Context(), uid); err == nil {
		avatarHash = h
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id": u.ID, "nickname": u.Nickname,
		"emailVerified": u.EmailVerified, "playerLimit": limit,
		"permission":  perm,
		"playerCount": playerCount, "storageBytes": storageBytes,
		"avatarHash": avatarHash,
	})
}

// ---- textures ----

func (b *Backend) handleListTextures(w http.ResponseWriter, r *http.Request) {
	uid, err := b.currentUser(r)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "not logged in")
		return
	}
	rows, err := b.Store.ListTextures(r.Context(), uid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "db error")
		return
	}
	type item struct {
		ID          int64  `json:"id"`
		Type        string `json:"type"`
		Hash        string `json:"hash"`
		Model       string `json:"model"`
		Description string `json:"description"`
		Detail      string `json:"detail,omitempty"`
		Source      string `json:"source,omitempty"`
		SourceURL   string `json:"sourceUrl,omitempty"`
		URL         string `json:"url"`
		UploadAt    string `json:"uploadAt"`
	}
	out := make([]item, 0, len(rows))
	for _, t := range rows {
		out = append(out, item{
			ID: t.ID, Type: t.Type, Hash: t.Hash, Model: t.Model,
			Description: t.Description, Detail: t.DetailMD,
			Source: t.Source, SourceURL: t.SourceURL,
			URL:      b.textureURL(r.Context(), t.Hash, t.Type),
			UploadAt: t.UploadAt.Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"textures": out})
}

func (b *Backend) handleUploadTexture(w http.ResponseWriter, r *http.Request) {
	uid, err := b.currentUser(r)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "not logged in")
		return
	}
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, "multipart parse failed")
		return
	}
	typ := texture.Type(r.FormValue("type"))
	if typ != texture.TypeSkin && typ != texture.TypeCape {
		writeErr(w, http.StatusBadRequest, "type must be skin|cape")
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "file required")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, texture.MaxUploadBytes+1))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read upload failed")
		return
	}
	detail := strings.TrimSpace(r.FormValue("detail"))
	source := strings.ToLower(strings.TrimSpace(r.FormValue("source")))
	sourceURL := strings.TrimSpace(r.FormValue("sourceUrl"))
	public := r.FormValue("public") != "false"
	pluginData, perr := b.pluginFormDataJSON("texture.upload", r.FormValue("pluginData"))
	if perr != nil {
		writePluginFormError(w, perr)
		return
	}
	v, created, aerr := b.Account.UploadTextureMeta(r.Context(), uid, string(typ), r.FormValue("model"), account.UploadMeta{
		Name: strings.TrimSpace(r.FormValue("description")), DetailMD: detail,
		Source: source, SourceURL: sourceURL, Public: public,
	}, data, account.CallMeta{
		IP: ygg.ClientIP(r), Filename: hdr.Filename, PluginData: pluginData,
	})
	if aerr != nil {
		writeAccountErr(w, aerr)
		return
	}
	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]interface{}{
		"id": v.ID, "type": v.Type, "hash": v.Hash,
		"url": b.textureURL(r.Context(), v.Hash, v.Type), "created": created,
		"description": v.Description, "detail": v.DetailMD,
		"source": v.Source, "sourceUrl": v.SourceURL, "public": public,
	})
}

// ---- players ----

// textureURL resolves a texture URL and lets plugin URL filters rewrite it
// (fail-open: a plugin error keeps the host-built URL).
func (b *Backend) textureURL(ctx context.Context, hash, typ string) string {
	u := b.Store.TextureURL(hash)
	if b.TextureURL != nil {
		u = b.TextureURL(ctx, hash, u, typ)
	}
	return u
}

func (b *Backend) handleListPlayers(w http.ResponseWriter, r *http.Request) {
	uid, err := b.currentUser(r)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "not logged in")
		return
	}
	rows, aerr := b.Account.ListPlayers(r.Context(), uid)
	if aerr != nil {
		writeAccountErr(w, aerr)
		return
	}
	players := make([]ygg.Player, 0, len(rows))
	for _, p := range rows {
		lp := ygg.Player{
			ID: p.ID, UID: p.UID, Name: p.Name, UUID: p.UUID,
			SkinModel: p.SkinModel, SkinHash: p.SkinHash, CapeHash: p.CapeHash,
		}
		if p.SkinHash != "" {
			lp.SkinURL = b.Store.TextureURL(p.SkinHash)
		}
		if p.CapeHash != "" {
			lp.CapeURL = b.Store.TextureURL(p.CapeHash)
		}
		players = append(players, lp)
	}
	ctx := r.Context()
	for i := range players {
		p := &players[i]
		if b.TextureURL != nil {
			if p.SkinURL != "" {
				p.SkinURL = b.TextureURL(ctx, p.SkinHash, p.SkinURL, "skin")
			}
			if p.CapeURL != "" {
				p.CapeURL = b.TextureURL(ctx, p.CapeHash, p.CapeURL, "cape")
			}
		}
		if b.ProfileFilter != nil {
			pf := plugin.UserProfileFilter{
				PlayerID: p.ID, PlayerName: p.Name, UUID: p.UUID,
				TexturesURL: map[string]string{"SKIN": p.SkinURL, "CAPE": p.CapeURL},
			}
			b.ProfileFilter(ctx, &pf)
			p.SkinURL = pf.TexturesURL["SKIN"]
			p.CapeURL = pf.TexturesURL["CAPE"]
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"players": players})
}

func (b *Backend) handleCreatePlayer(w http.ResponseWriter, r *http.Request) {
	uid, err := b.currentUser(r)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "not logged in")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	v, created, aerr := b.Account.CreatePlayer(r.Context(), uid, req.Name, account.CallMeta{IP: ygg.ClientIP(r)})
	if aerr != nil {
		writeAccountErr(w, aerr)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]interface{}{"id": v.ID, "name": v.Name, "uuid": v.UUID})
}

// handleSetSkin / handleSetCape point a player at one of the user's textures.
func (b *Backend) handleSetSkin(w http.ResponseWriter, r *http.Request) {
	b.setPlayerTexture(w, r, "skin")
}
func (b *Backend) handleSetCape(w http.ResponseWriter, r *http.Request) {
	b.setPlayerTexture(w, r, "cape")
}

func (b *Backend) setPlayerTexture(w http.ResponseWriter, r *http.Request, kind string) {
	uid, err := b.currentUser(r)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "not logged in")
		return
	}
	pid, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var req struct {
		TextureID int64 `json:"textureId"` // 0 = unset
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if _, aerr := b.Account.SetTexture(r.Context(), uid, pid, kind, req.TextureID, account.CallMeta{IP: ygg.ClientIP(r)}); aerr != nil {
		writeAccountErr(w, aerr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "playerId": pid})
}

func (b *Backend) handleStats(w http.ResponseWriter, r *http.Request) {
	// Session-gated (review P3: the weak-hash count was public).
	if _, err := b.currentUser(r); err != nil {
		writeErr(w, http.StatusUnauthorized, "not logged in")
		return
	}
	weak, _ := b.Store.WeakHashCount(r.Context())
	writeJSON(w, http.StatusOK, map[string]interface{}{"weakHashUsers": weak})
}

// handleAnnouncement: public site announcement. Markdown source (raw) plus
// server-sanitized HTML (goldmark + bluemonday — 拍板双层消毒的服务端层；
// the frontend re-sanitizes with marked + DOMPurify before v-html). Empty
// raw hides the card client-side.
func (b *Backend) handleAnnouncement(w http.ResponseWriter, r *http.Request) {
	raw := b.Store.Announcement(r.Context())
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"announcement": raw,
		"html":         announce.Render(raw),
	})
}

var _ = errors.Is
