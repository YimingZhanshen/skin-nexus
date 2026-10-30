package ygg

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"

	"skin-nexus/server/internal/accesslog"
)

// Metadata is the ALI discovery document (live-capture shape,
// docs/phase1-dev-plan.md §1.1).
type Metadata struct {
	Meta struct {
		ServerName            string            `json:"serverName"`
		ImplementationName    string            `json:"implementationName"`
		ImplementationVersion string            `json:"implementationVersion"`
		Links                 map[string]string `json:"links"`
		Features              map[string]bool   `json:"-"` // serialized flat below
	} `json:"meta"`
	SkinDomains        []string `json:"skinDomains"`
	SignaturePublicKey string   `json:"signaturePublickey"`
}

// MetadataConfig is deployment configuration for the metadata document.
type MetadataConfig struct {
	ServerName  string
	Version     string
	Homepage    string
	Register    string
	SkinDomains []string
	// OpenIDConfigurationURL enables meta.feature.openid_configuration_url
	// (yggc/OIDC passwordless login discovery; phase 2, absent when empty).
	OpenIDConfigurationURL string
	// ExtraSkinDomains supplies dynamic whitelist entries per request
	// (union member hosts, options-cached). The legacy formula is
	// config ∪ union hosts ∪ site_url host ∪ request host.
	ExtraSkinDomains func() []string
}

// API serves the Yggdrasil HTTP surface mounted at /api/yggdrasil.
type API struct {
	svc *Service
	cfg MetadataConfig
	// A2 Mojang fallback (nil = off, the default): misses on profile and
	// hasJoined are offered to the official sessionserver before the 204.
	fallback *Fallback
	// Access is the observability sidecar (nil = off): authenticate/refresh/
	// validate/join/hasJoined/profile traffic lines for the admin panel.
	// Recording is best-effort and can never change a wire response.
	access *accesslog.Logger
}

func NewAPI(svc *Service, cfg MetadataConfig) *API {
	return &API{svc: svc, cfg: cfg}
}

// SetFallback installs the official-sessionserver fallback (main wires the
// hot `ygg_mojang_fallback` panel toggle).
func (a *API) SetFallback(f *Fallback) { a.fallback = f }

// SetAccess installs the traffic logger (main wires the access_logs sink).
func (a *API) SetAccess(l *accesslog.Logger) { a.access = l }

// accessLog is the nil-safe recording call shared by every handler.
func (a *API) accessLog(r *http.Request, e accesslog.Entry) {
	if a.access == nil {
		return
	}
	e.IP = clientIP(r)
	e.UA = r.UserAgent()
	a.access.Log(r.Context(), e)
}

// Handler returns the route table. Routes are rooted at the /api/yggdrasil
// prefix — the compatibility surface itself (legacy clients hardcode it).
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/yggdrasil/{$}", a.handleMetadata)
	mux.HandleFunc("POST /api/yggdrasil/authserver/authenticate", a.handleAuthenticate)
	mux.HandleFunc("POST /api/yggdrasil/authserver/refresh", a.handleRefresh)
	mux.HandleFunc("POST /api/yggdrasil/authserver/validate", a.handleValidate)
	mux.HandleFunc("POST /api/yggdrasil/authserver/invalidate", a.handleInvalidate)
	mux.HandleFunc("POST /api/yggdrasil/authserver/signout", a.handleSignout)
	mux.HandleFunc("POST /api/yggdrasil/sessionserver/session/minecraft/join", a.handleJoin)
	mux.HandleFunc("GET /api/yggdrasil/sessionserver/session/minecraft/hasJoined", a.handleHasJoined)
	mux.HandleFunc("GET /api/yggdrasil/sessionserver/session/minecraft/profile/{uuid}", a.handleProfile)
	mux.HandleFunc("POST /api/yggdrasil/api/profiles/minecraft", a.handleLookupBulk)
	mux.HandleFunc("GET /api/yggdrasil/api/users/profiles/minecraft/{name}", a.handleLookupName)
	return mux
}

// ---- responses ----

// writeError emits the legacy error body {error,errorMessage,cause?}.
func writeError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body := map[string]string{"error": code, "errorMessage": msg}
	_ = json.NewEncoder(w).Encode(body)
}

// noContent emits 204 — the "not found / done" signal clients expect.
// Callers must never turn these paths into 4xx/5xx (clients treat those as
// network errors, docs/phase1-dev-plan.md §1.3).
func noContent(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) }

// checkJSON enforces the legacy content-type behavior: a non-JSON POST gets
// 200 + error body (yggdrasil-connect CheckContentType semantics).
func checkJSON(r *http.Request, w http.ResponseWriter) bool {
	ct := r.Header.Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		writeError(w, http.StatusOK, "UnsupportedMediaType", "Content-Type must be application/json")
		return false
	}
	return true
}

// ---- metadata ----

func (a *API) handleMetadata(w http.ResponseWriter, r *http.Request) {
	var b strings.Builder
	b.WriteString(`{"meta":{"serverName":`)
	writeJSONString(&b, a.cfg.ServerName)
	b.WriteString(`,"implementationName":"SkinNexus","implementationVersion":`)
	writeJSONString(&b, a.cfg.Version)
	b.WriteString(`,"links":{"homepage":`)
	writeJSONString(&b, a.cfg.Homepage)
	b.WriteString(`,"register":`)
	writeJSONString(&b, a.cfg.Register)
	b.WriteString(`},"feature.non_email_login":true`)
	if a.cfg.OpenIDConfigurationURL != "" {
		b.WriteString(`,"feature.openid_configuration_url":`)
		writeJSONString(&b, a.cfg.OpenIDConfigurationURL)
	}
	b.WriteString(`},"skinDomains":[`)
	for i, d := range a.skinDomains(r) {
		if i > 0 {
			b.WriteByte(',')
		}
		writeJSONString(&b, d)
	}
	b.WriteString(`],"signaturePublickey":`)
	writeJSONString(&b, a.svc.PublicKeyPEM())
	b.WriteString(`}`)
	w.Header().Set("Content-Type", "application/json")
	// Authlib-injector discovery: relative or absolute location of this API.
	w.Header().Set("X-Authlib-Injector-API-Location", apiLocation(r))
	// Cross-origin metadata reads (2026-10-24 UI 名称回填): the union server
	// list fills each member's display name by fetching its
	// /api/yggdrasil meta.serverName from the browser (old plugin
	// servers.twig:20-31 + HandleCors.php:18-20). Mirror that CORS surface.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD")
	_, _ = w.Write([]byte(b.String()))
}

// skinDomains computes the texture whitelist per the live legacy formula
// (yggdrasil-connect ConfigController.php:138-147): configured domains,
// union member hosts, the site_url host, and the current request host.
// authlib-injector trusts textures only from listed hosts, so Union
// cross-station skins keep verifying on member stations.
func (a *API) skinDomains(r *http.Request) []string {
	seen := map[string]bool{}
	out := make([]string, 0, 8)
	add := func(d string) {
		if d != "" && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	for _, d := range a.cfg.SkinDomains {
		add(d)
	}
	if a.cfg.ExtraSkinDomains != nil {
		for _, d := range a.cfg.ExtraSkinDomains() {
			add(d)
		}
	}
	if u, err := url.Parse(a.cfg.Homepage); err == nil {
		add(u.Host)
	}
	add(r.Host)
	return out
}

// apiLocation derives the ALI value from the request so the deployment
// works under any host (matches the live legacy header shape).
func apiLocation(r *http.Request) string {
	base := "https://" + r.Host
	if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
		base = "http://" + r.Host
	}
	prefix := strings.TrimSuffix(r.URL.Path, "/")
	return base + prefix
}

// writeJSONString appends a quoted JSON string without allocating an encoder.
func writeJSONString(b *strings.Builder, s string) {
	enc, _ := json.Marshal(s)
	b.Write(enc)
}

// ---- authserver ----

func (a *API) handleAuthenticate(w http.ResponseWriter, r *http.Request) {
	if !checkJSON(r, w) {
		return
	}
	var req struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		ClientToken string `json:"clientToken"`
		RequestUser bool   `json:"requestUser"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "IllegalArgumentException", "invalid JSON body")
		return
	}
	// Rate limit (P1 ygg_rate_limit): IP sliding window + per-identifier
	// failure lockout. 429 is an additive response — the legacy contract
	// never defined rate limiting (recorded in docs/compat-matrix.md).
	// B5.2 gap throttle (upstream Throttle.php:15 parity): same-username
	// minimum interval, error body keeps the ForbiddenOperationException
	// shape upstream throws.
	idKey := "id:" + strings.ToLower(strings.TrimSpace(req.Username))
	ipKey := "ip:" + clientIP(r)
	if ok, retryAfter := a.svc.AuthGapAllow(r.Context(), strings.TrimSpace(req.Username)); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
		a.accessLog(r, accesslog.Entry{Kind: accesslog.YggAuthenticate,
			Outcome: accesslog.OutcomeDenied, Who: req.Username, Detail: "auth_gap"})
		writeError(w, http.StatusForbidden, "ForbiddenOperationException",
			"Too many requests. Please try again in "+strconv.Itoa(int(retryAfter.Seconds())+1)+"s.")
		return
	}
	if a.svc.Limiter != nil {
		if a.svc.Limiter.Locked(idKey) {
			ra := int(a.svc.Limiter.RetryAfter(idKey).Seconds()) + 1
			w.Header().Set("Retry-After", strconv.Itoa(ra))
			a.accessLog(r, accesslog.Entry{Kind: accesslog.YggAuthenticate,
				Outcome: accesslog.OutcomeDenied, Who: req.Username, Detail: "locked"})
			writeError(w, http.StatusTooManyRequests, "TooManyRequests", "Too many failed attempts. Please try again later.")
			return
		}
		if !a.svc.Limiter.Allow(ipKey) {
			a.accessLog(r, accesslog.Entry{Kind: accesslog.YggAuthenticate,
				Outcome: accesslog.OutcomeDenied, Who: req.Username, Detail: "rate_limited"})
			writeError(w, http.StatusTooManyRequests, "TooManyRequests", "Too many requests from your address.")
			return
		}
	}
	t, players, err := a.svc.Authenticate(r.Context(), req.Username, req.Password, req.ClientToken, req.RequestUser)
	if err != nil {
		if a.svc.Limiter != nil {
			a.svc.Limiter.Fail(idKey)
		}
		a.accessLog(r, accesslog.Entry{Kind: accesslog.YggAuthenticate,
			Outcome: accesslog.OutcomeFail, Who: req.Username, Detail: "invalid_credentials"})
		writeError(w, http.StatusForbidden, "ForbiddenOperationException", "Invalid credentials. Invalid username or password.")
		return
	}
	if a.svc.Limiter != nil {
		a.svc.Limiter.Succeed(idKey)
	}
	a.accessLog(r, accesslog.Entry{Kind: accesslog.YggAuthenticate,
		Outcome: accesslog.OutcomeOK, Who: req.Username, Detail: "client_token=" + req.ClientToken})
	writeTokenResponse(w, t, players, req.RequestUser)
}

func (a *API) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if !checkJSON(r, w) {
		return
	}
	var req struct {
		AccessToken     string          `json:"accessToken"`
		ClientToken     string          `json:"clientToken"`
		RequestUser     bool            `json:"requestUser"`
		SelectedProfile json.RawMessage `json:"selectedProfile"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "IllegalArgumentException", "invalid JSON body")
		return
	}
	// IP sliding window on refresh too (opaque-token brute force).
	if a.svc.Limiter != nil && !a.svc.Limiter.Allow("ip:"+clientIP(r)) {
		a.accessLog(r, accesslog.Entry{Kind: accesslog.YggRefresh,
			Outcome: accesslog.OutcomeDenied, Detail: "rate_limited"})
		writeError(w, http.StatusTooManyRequests, "TooManyRequests", "Too many requests from your address.")
		return
	}
	// Honour the client's profile selection (legacy refresh-with-select).
	var pid int64
	if ref := parseProfileRef(req.SelectedProfile); ref != "" {
		p, err := a.svc.PlayerPIDByUUID(r.Context(), ref)
		if err != nil {
			a.accessLog(r, accesslog.Entry{Kind: accesslog.YggRefresh,
				Outcome: accesslog.OutcomeFail, Detail: "bad_profile_ref uuid=" + ref})
			writeError(w, http.StatusBadRequest, "IllegalArgumentException", "请求参数不合法")
			return
		}
		pid = p
	}
	t, players, err := a.svc.Refresh(r.Context(), req.AccessToken, req.ClientToken, pid)
	if err != nil {
		a.accessLog(r, accesslog.Entry{Kind: accesslog.YggRefresh,
			Outcome: accesslog.OutcomeFail, Detail: "invalid_token"})
		writeError(w, http.StatusForbidden, "ForbiddenOperationException", "Invalid token.")
		return
	}
	a.accessLog(r, accesslog.Entry{Kind: accesslog.YggRefresh,
		Outcome: accesslog.OutcomeOK, Detail: "client_token=" + req.ClientToken})
	writeTokenResponse(w, t, players, req.RequestUser)
}

func (a *API) handleValidate(w http.ResponseWriter, r *http.Request) {
	if !checkJSON(r, w) {
		return
	}
	var req struct {
		AccessToken string `json:"accessToken"`
		ClientToken string `json:"clientToken"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "IllegalArgumentException", "invalid JSON body")
		return
	}
	if err := a.svc.Validate(r.Context(), req.AccessToken, req.ClientToken); err != nil {
		a.accessLog(r, accesslog.Entry{Kind: accesslog.YggValidate,
			Outcome: accesslog.OutcomeFail, Detail: "invalid_token"})
		writeError(w, http.StatusForbidden, "ForbiddenOperationException", "Invalid token.")
		return
	}
	a.accessLog(r, accesslog.Entry{Kind: accesslog.YggValidate,
		Outcome: accesslog.OutcomeOK, Detail: ""})
	noContent(w)
}

func (a *API) handleInvalidate(w http.ResponseWriter, r *http.Request) {
	if !checkJSON(r, w) {
		return
	}
	var req struct {
		AccessToken string `json:"accessToken"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	a.svc.Invalidate(r.Context(), req.AccessToken)
	noContent(w) // always 204
}

func (a *API) handleSignout(w http.ResponseWriter, r *http.Request) {
	if !checkJSON(r, w) {
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	// B5.2 gap throttle applies upstream (routes.php:11-15 puts Throttle on
	// signout too), but the frozen wire contract is "signout always 204" —
	// a throttle hit DROPS the work (no password check = anti-brute-force
	// preserved) and still answers 204.
	if ok, _ := a.svc.AuthGapAllow(r.Context(), strings.TrimSpace(req.Username)); ok {
		a.svc.SignOut(r.Context(), req.Username, req.Password)
	}
	noContent(w) // always 204
}

// ---- sessionserver ----

func (a *API) handleJoin(w http.ResponseWriter, r *http.Request) {
	if !checkJSON(r, w) {
		return
	}
	var req struct {
		AccessToken     string          `json:"accessToken"`
		SelectedProfile json.RawMessage `json:"selectedProfile"`
		ServerID        string          `json:"serverId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "IllegalArgumentException", "请求参数不合法")
		return
	}
	// authlib-injector sends selectedProfile as a bare UUID string; our own
	// clients and Yggdrasil refresh use {"id": "..."} — accept both (and both
	// UUID spellings), matching the live legacy wire (capture 2026-10-23).
	uu := parseProfileRef(req.SelectedProfile)
	if uu == "" {
		a.accessLog(r, accesslog.Entry{Kind: accesslog.YggJoin,
			Outcome: accesslog.OutcomeFail, Detail: "bad_profile_ref serverId=" + req.ServerID})
		writeError(w, http.StatusBadRequest, "IllegalArgumentException", "请求参数不合法")
		return
	}
	err := a.svc.Join(r.Context(), req.AccessToken, uu, req.ServerID, clientIP(r))
	switch {
	case errors.Is(err, ErrEmailUnverified):
		a.accessLog(r, accesslog.Entry{Kind: accesslog.YggJoin,
			Outcome: accesslog.OutcomeFail, Detail: "email_unverified uuid=" + uu + " serverId=" + req.ServerID})
		writeError(w, http.StatusForbidden, "ForbiddenOperationException", "Email not verified.")
	case errors.Is(err, ErrPlayerMismatch):
		a.accessLog(r, accesslog.Entry{Kind: accesslog.YggJoin,
			Outcome: accesslog.OutcomeFail, Detail: "player_mismatch uuid=" + uu + " serverId=" + req.ServerID})
		writeError(w, http.StatusForbidden, "ForbiddenOperationException", "请求的角色与令牌绑定的角色不一致")
	case isJoinDenied(err):
		var denied *JoinDeniedError
		_ = errors.As(err, &denied)
		a.accessLog(r, accesslog.Entry{Kind: accesslog.YggJoin,
			Outcome: accesslog.OutcomeDenied, Detail: "plugin_guard uuid=" + uu + " serverId=" + req.ServerID})
		writeError(w, http.StatusForbidden, "ForbiddenOperationException", denied.Message)
	case err != nil:
		a.accessLog(r, accesslog.Entry{Kind: accesslog.YggJoin,
			Outcome: accesslog.OutcomeFail, Detail: "invalid_token uuid=" + uu + " serverId=" + req.ServerID})
		writeError(w, http.StatusForbidden, "ForbiddenOperationException", "Invalid token.")
	default:
		a.accessLog(r, accesslog.Entry{Kind: accesslog.YggJoin,
			Outcome: accesslog.OutcomeOK, Detail: "uuid=" + uu + " serverId=" + req.ServerID})
		noContent(w)
	}
}

// parseProfileRef accepts the two profile-reference wire forms: a bare UUID
// string or {"id":"..."} (extra members tolerated). "" when absent/broken.
func parseProfileRef(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var obj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return obj.ID
	}
	return ""
}

func (a *API) handleHasJoined(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	name, sid := q.Get("username"), q.Get("serverId")
	p, err := a.svc.HasJoined(r.Context(), name, sid, q.Get("ip"))
	if err != nil {
		// A2 fallback: no local join record → ask the official sessionserver
		// before answering 204 (Mojang confirms joins it recorded itself —
		// official-account players on authlib-injector servers).
		if body, ok := a.fallback.hasJoined(r.Context(), name, sid, q.Get("ip")); ok {
			a.accessLog(r, accesslog.Entry{Kind: accesslog.YggHasJoined,
				Outcome: accesslog.OutcomeOK, Who: name,
				Detail: "src=official serverId=" + sid})
			writeFallbackProfile(w, body)
			return
		}
		a.accessLog(r, accesslog.Entry{Kind: accesslog.YggHasJoined,
			Outcome: accesslog.OutcomeMiss, Who: name,
			Detail: "src=none serverId=" + sid})
		noContent(w) // unknown / expired / mismatch: 204, never 4xx
		return
	}
	a.accessLog(r, accesslog.Entry{Kind: accesslog.YggHasJoined,
		Outcome: accesslog.OutcomeOK, Who: name,
		Detail: "src=local serverId=" + sid})
	writeJSON(w, p)
}

func (a *API) handleProfile(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	withSig := r.URL.Query().Get("unsigned") == "false"
	p, err := a.svc.ProfileByUUID(r.Context(), uuid, withSig)
	if err != nil {
		// A2 fallback: unknown-to-us UUID → the official sessionserver may
		// still know it (signature policy mirrored: unsigned=true only when
		// our own handler would have stripped signatures).
		if body, ok := a.fallback.profile(r.Context(), uuid, withSig); ok {
			a.accessLog(r, accesslog.Entry{Kind: accesslog.YggProfile,
				Outcome: accesslog.OutcomeOK, Who: uuid, Detail: "src=official"})
			writeFallbackProfile(w, body)
			return
		}
		a.accessLog(r, accesslog.Entry{Kind: accesslog.YggProfile,
			Outcome: accesslog.OutcomeMiss, Who: uuid, Detail: "src=none"})
		noContent(w) // unknown profile: empty body
		return
	}
	a.accessLog(r, accesslog.Entry{Kind: accesslog.YggProfile,
		Outcome: accesslog.OutcomeOK, Who: uuid, Detail: "src=local"})
	if !withSig {
		for i := range p.Properties {
			p.Properties[i].Signature = ""
		}
	}
	writeJSON(w, p)
}

// writeFallbackProfile emits a validated official-sessionserver body
// verbatim (already proven JSON with id+name by the fallback gate).
func writeFallbackProfile(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// ---- lookup ----

func (a *API) handleLookupBulk(w http.ResponseWriter, r *http.Request) {
	if !checkJSON(r, w) {
		return
	}
	var names []string
	if err := json.NewDecoder(r.Body).Decode(&names); err != nil {
		writeError(w, http.StatusBadRequest, "IllegalArgumentException", "invalid JSON body")
		return
	}
	// B5.2 ygg_search_profile_max (upstream ProfileController.php:62-68
	// parity): array_unique FIRST, then reject over the cap with
	// ForbiddenOperationException — no silent truncation.
	uniq := names[:0:0]
	seen := make(map[string]struct{}, len(names))
	for _, n := range names {
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		uniq = append(uniq, n)
	}
	if max := a.svc.SearchProfileMax(r.Context()); len(uniq) > max {
		writeError(w, http.StatusForbidden, "ForbiddenOperationException",
			"A maximum of "+strconv.Itoa(max)+" profiles may be queried at once.")
		return
	}
	players := a.svc.LookupBulk(r.Context(), uniq)
	type entry struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	out := make([]entry, 0, len(players))
	for _, p := range players {
		out = append(out, entry{ID: p.UUID, Name: p.Name})
	}
	writeJSON(w, out)
}

func (a *API) handleLookupName(w http.ResponseWriter, r *http.Request) {
	p, err := a.svc.LookupByName(r.Context(), r.PathValue("name"))
	if err != nil {
		noContent(w) // legacy: 204 for unknown player
		return
	}
	writeJSON(w, map[string]string{"id": p.UUID, "name": p.Name})
}

// ---- shared writers ----

func writeTokenResponse(w http.ResponseWriter, t Token, players []Player, withUser bool) {
	type prof struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	available := make([]prof, 0, len(players))
	var selected *prof
	for _, p := range players {
		available = append(available, prof{ID: p.UUID, Name: p.Name})
		if p.ID == t.SelectedPID {
			sel := prof{ID: p.UUID, Name: p.Name}
			selected = &sel
		}
	}
	resp := map[string]interface{}{
		"accessToken":       t.AccessToken,
		"clientToken":       t.ClientToken,
		"availableProfiles": available,
	}
	if selected != nil {
		resp["selectedProfile"] = selected
	}
	if withUser {
		// live legacy capture parity (requestUser:true is sent by every
		// launcher login/refresh): user.id = uuid5(NAMESPACE_DNS, uid) as
		// 32 lowercase hex, properties [] (chain-trace P2 2026-10-23).
		resp["user"] = map[string]interface{}{
			"id":         uuid5DNSHex(strconv.FormatInt(t.UserID, 10)),
			"properties": []interface{}{},
		}
	}
	writeJSON(w, resp)
}

// uuid5DNSHex computes uuid5(NAMESPACE_DNS, s) in the 32-lowercase-hex
// spelling the legacy wire uses (no dashes).
func uuid5DNSHex(s string) string {
	ns, _ := hex.DecodeString("6ba7b8109dad11d180b400c04fd430c8")
	h := sha1.New()
	h.Write(ns)
	h.Write([]byte(s))
	sum := h.Sum(nil)
	sum[6] = (sum[6] & 0x0f) | 0x50 // version 5
	sum[8] = (sum[8] & 0x3f) | 0x80 // RFC 4122 variant
	return hex.EncodeToString(sum[:16])
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// DefaultCDNHeaders is the ordered list used when CDN mode is enabled and no
// custom list is configured. CDN-specific headers win over X-Forwarded-For
// because a plain XFF chain usually ends at the CDN node, not the player.
func DefaultCDNHeaders() []string {
	return []string{"CF-Connecting-IP", "True-Client-IP", "Ali-CDN-Real-IP", "X-Real-IP", "X-Forwarded-For"}
}

type clientIPPolicy struct {
	enabled bool
	headers []string
}

var clientIPPolicyStore atomic.Value // clientIPPolicy

// ConfigureClientIP applies the admin CDN setting. It is safe to call while
// requests are in flight; an empty header list falls back to DefaultCDNHeaders.
func ConfigureClientIP(enabled bool, headers []string) {
	clean := make([]string, 0, len(headers))
	for _, h := range headers {
		if h = strings.TrimSpace(h); h != "" {
			clean = append(clean, http.CanonicalHeaderKey(h))
		}
	}
	if len(clean) == 0 {
		clean = DefaultCDNHeaders()
	}
	clientIPPolicyStore.Store(clientIPPolicy{enabled: enabled, headers: clean})
}

// clientIP returns the client address for rate limiting. The direct peer
// must be a trusted proxy (loopback/private). In CDN mode the configured
// headers are checked in order; X-Forwarded-For keeps the last-hop rule so a
// client cannot rotate spoofed leading entries. Without CDN mode the legacy
// XFF-last behaviour is unchanged.
func clientIP(r *http.Request) string {
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	if !isTrustedProxy(host) {
		return host
	}
	if p, ok := clientIPPolicyStore.Load().(clientIPPolicy); ok && p.enabled {
		for _, name := range p.headers {
			raw := strings.TrimSpace(r.Header.Get(name))
			if raw == "" {
				continue
			}
			if strings.EqualFold(name, "X-Forwarded-For") {
				parts := strings.Split(raw, ",")
				raw = parts[len(parts)-1]
			}
			if ip := normalizeIP(raw); ip != "" {
				return ip
			}
		}
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[len(parts)-1])
	}
	return host
}

// normalizeIP accepts bare IPs and host:port forms (IPv4 and [IPv6]).
func normalizeIP(raw string) string {
	s := strings.Trim(strings.TrimSpace(raw), `"`)
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	s = strings.Trim(s, "[]")
	ip := net.ParseIP(s)
	if ip == nil {
		return ""
	}
	return ip.String()
}

// isTrustedProxy reports whether the peer address is inside the local trust
// zone (loopback or RFC1918 — docker bridge / same-host nginx).
func isTrustedProxy(host string) bool {
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate()
}

// ClientIP exposes the XFF-aware client address for sibling modules
// (webapi rate limiting shares one definition).
func ClientIP(r *http.Request) string { return clientIP(r) }

var _ = errors.Is // keep errors import for future sentinel mapping

func isJoinDenied(err error) bool {
	var d *JoinDeniedError
	return errors.As(err, &d)
}
