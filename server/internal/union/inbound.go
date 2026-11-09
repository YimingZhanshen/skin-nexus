package union

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"
)

// Hooks are the data callbacks of the member site. Everything the Union core
// needs from the store/panel layer comes in through them, so the package stays
// free of store imports (repo rule: new queries live in internal/store).
// Nil callbacks are no-ops except QueryEmailFunc/OnRemapUUID, which report a
// configuration error on their endpoints instead of pretending success.
type Hooks struct {
	// OnServerList receives the fresh hub server list (the "servers" array as
	// decoded data) and its version string after a successful updatelist.
	OnServerList func(list any, version string)

	// OnMemberKeyRotated receives the new shared key after an accepted
	// updatebackendkey call. The caller persists it (options) — the core only
	// keeps its own Outbound/Grantor copies in sync.
	OnMemberKeyRotated func(newKey string)

	// OnRemapUUID applies the hub-issued {oldUUID: newUUID} batch remap.
	OnRemapUUID func(map[string]string) error

	// ProfileListFunc supplies {uuid:name} for POST /sync. Nil = empty list.
	ProfileListFunc func() (map[string]string, error)

	// QueryEmailFunc resolves a player name to the bound account email
	// (200 {"email":...} / 204 when unknown). Only called in "full" mode.
	QueryEmailFunc func(username string) (string, bool, error)
}

// API serves the hub -> member endpoints under /api/union/member (§4.3/§4.4).
// All of them run behind the signed-message Verifier except the public
// self-description and the OAuth2 signature-key document.
type API struct {
	cfg    Config
	out    *Outbound
	signer *UnionSigner
	grant  *Grantor
	hooks  Hooks
	audit  AuditFunc
	verify *Verifier
	now    func() time.Time // injectable clock for tests

	serverListVersion atomic.Value // string (cached option union_server_list_version)
	privateKeyVersion atomic.Value // string (cached option union_private_key_version)
}

// NewAPI assembles the inbound surface. out/signer/grant may be nil; the
// endpoints that need them then report a configuration error. Returns an
// error only when the enabled pinned key cannot be parsed.
func NewAPI(cfg Config, out *Outbound, signer *UnionSigner, grant *Grantor, hooks Hooks, audit AuditFunc) (*API, error) {
	verifier, err := NewVerifier(cfg, audit)
	if err != nil {
		return nil, err
	}
	a := &API{
		cfg:    cfg,
		out:    out,
		signer: signer,
		grant:  grant,
		hooks:  hooks,
		audit:  audit,
		verify: verifier,
		now:    time.Now,
	}
	a.serverListVersion.Store("")
	a.privateKeyVersion.Store("")
	return a, nil
}

// Enabled reports whether the inbound surface is live (nil-safe).
func (a *API) Enabled() bool { return a != nil && a.cfg.Enabled }

// Verifier exposes the message verifier so the caller can protect additional
// hub-facing routes with the same policy.
func (a *API) Verifier() *Verifier { return a.verify }

// SetServerListVersion / SetPrivateKeyVersion seed the versions reported by
// the self-description after a restart (they are persisted in options).
func (a *API) SetServerListVersion(v string)   { a.serverListVersion.Store(v) }
func (a *API) SetPrivateKeyVersion(v string)   { a.privateKeyVersion.Store(v) }
func (a *API) ServerListVersion() string       { s, _ := a.serverListVersion.Load().(string); return s }
func (a *API) PrivateKeyVersionString() string { s, _ := a.privateKeyVersion.Load().(string); return s }

// Handler returns the full route table with the frozen absolute paths (same
// assembly style as internal/ygg). Disabled => 404 everywhere (the caller is
// expected to mount nothing at all in that case).
func (a *API) Handler() http.Handler {
	if !a.Enabled() {
		return http.NotFoundHandler()
	}
	mux := http.NewServeMux()

	// hub -> member, all behind the signed-message middleware.
	protected := map[string]http.HandlerFunc{
		"POST /api/union/member/updatelist":       a.handleUpdateList,
		"POST /api/union/member/updateprivatekey": a.handleUpdatePrivateKey,
		"POST /api/union/member/updatebackendkey": a.handleUpdateBackendKey,
		"POST /api/union/member/sync":             a.handleSync,
		"POST /api/union/member/remapuuid":        a.handleRemapUUID,
		"POST /api/union/member/diagnose":         a.handleDiagnose,
		"GET /api/union/member/queryemail":        a.handleQueryEmail,
		// Never implemented: remote code installation channel.
		"POST /api/union/member/updateplugin": a.handleUpdatePlugin,
	}
	for pattern, h := range protected {
		mux.Handle(pattern, a.verify.Middleware(http.HandlerFunc(h)))
	}

	// Public, no signature (legacy routes are outside UnionHostVerify).
	mux.HandleFunc("GET /api/union/member/{$}", a.handleSelfDescription)
	// The no-slash form is what a cross-origin caller actually uses: the MUA
	// hub reads this document right after the metadata to decide whether to
	// enable its OAuth button. "{$}" alone made ServeMux answer a bare 307
	// there, and a redirect it writes itself carries no CORS headers, so that
	// chain failed and the hub showed "✕ <code>" with a disabled button.
	// See canonical_member_cors_test.go.
	mux.HandleFunc("GET /api/union/member", a.handleSelfDescription)
	if a.grant != nil {
		mux.Handle("GET /api/union/member/oauth2/{$}", a.grant.SigKeyHandler())
	}
	return mux
}

// ---- protected endpoints ----

// handleUpdateList pulls GET {root}/serverlist and hands the list to the
// OnServerList callback (cache write lives with the caller, matching the
// options-based legacy cache union_server_list[_version]).
func (a *API) handleUpdateList(w http.ResponseWriter, r *http.Request) {
	if a.out == nil {
		a.internalError(w, "serverlist pull not configured")
		return
	}
	doc, err := a.out.ServerList(r.Context())
	if err != nil {
		a.upstreamError(w, err)
		return
	}
	a.serverListVersion.Store(doc.Version)
	if a.hooks.OnServerList != nil {
		a.hooks.OnServerList(doc.Servers, doc.Version)
	}
	a.audit.call("union.updatelist.updated", map[string]any{"version": doc.Version, "servers": len(doc.Servers)})
	w.WriteHeader(http.StatusOK) // legacy: empty 200
}

// handleUpdatePrivateKey pulls GET {root}/privatekey and swaps the shared
// texture-signing key (union_private_key_version is recorded on the signer).
func (a *API) handleUpdatePrivateKey(w http.ResponseWriter, r *http.Request) {
	if a.out == nil || a.signer == nil {
		a.internalError(w, "private key pull not configured")
		return
	}
	doc, err := a.out.Privatekey(r.Context())
	if err != nil {
		a.upstreamError(w, err)
		return
	}
	if err := a.signer.SetKey(doc.PrivateKey, doc.PrivateKeyVersion); err != nil {
		a.audit.call("union.updateprivatekey.rejected", map[string]any{"error": err.Error()})
		writeError(w, http.StatusInternalServerError, codeIllegalArg, "hub private key rejected: "+err.Error())
		return
	}
	a.privateKeyVersion.Store(doc.PrivateKeyVersion)
	a.audit.call("union.updateprivatekey.updated", map[string]any{
		"version":     doc.PrivateKeyVersion,
		"fingerprint": a.signer.KeyFingerprint(),
	})
	w.WriteHeader(http.StatusOK) // legacy: empty 200
}

// handleUpdateBackendKey rotates the shared member key (body {"key":...}).
// Default is DENY (Config.AllowUpdateBackendKey == false): a remote party must
// not rewrite the shared secret just because it can sign one message
// (analysis/yggdrasil-connect.md §7.2 security note).
func (a *API) handleUpdateBackendKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Key == "" {
		writeError(w, http.StatusBadRequest, codeIllegalArg, `body must be {"key":"..."}`)
		return
	}
	if !a.cfg.AllowUpdateBackendKey {
		a.audit.call("union.updatebackendkey.denied", map[string]any{"reason": "allow_update_backend_key_disabled"})
		writeError(w, http.StatusForbidden, codeForbidden, "member key rotation is disabled on this server.")
		return
	}
	// Never audit the key material itself.
	if a.out != nil {
		a.out.SetMemberKey(body.Key)
	}
	if a.grant != nil {
		a.grant.SetMemberKey(body.Key)
	}
	if a.hooks.OnMemberKeyRotated != nil {
		a.hooks.OnMemberKeyRotated(body.Key)
	}
	a.audit.call("union.updatebackendkey.rotated", nil)
	w.WriteHeader(http.StatusOK) // legacy: empty 200
}

// handleSync pushes the local {uuid:name} roster to the hub (POST /sync).
func (a *API) handleSync(w http.ResponseWriter, r *http.Request) {
	if a.out == nil {
		a.internalError(w, "sync not configured")
		return
	}
	profileList := map[string]string{}
	if a.hooks.ProfileListFunc != nil {
		list, err := a.hooks.ProfileListFunc()
		if err != nil {
			writeError(w, http.StatusInternalServerError, codeIllegalArg, "profile list unavailable: "+err.Error())
			return
		}
		if list != nil {
			profileList = list
		}
	}
	if err := a.out.Sync(r.Context(), profileList); err != nil {
		a.upstreamError(w, err)
		return
	}
	a.audit.call("union.sync.done", map[string]any{"profiles": len(profileList)})
	w.WriteHeader(http.StatusOK) // legacy: empty 200
}

// handleRemapUUID applies the hub batch remap (body {"remapped_uuid":{旧:新}}).
func (a *API) handleRemapUUID(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Remapped map[string]string `json:"remapped_uuid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Remapped) == 0 {
		writeError(w, http.StatusBadRequest, codeIllegalArg, `body must be {"remapped_uuid":{"<old>":"<new>"}}`)
		return
	}
	if a.hooks.OnRemapUUID == nil {
		a.internalError(w, "remapuuid handler not configured")
		return
	}
	if err := a.hooks.OnRemapUUID(body.Remapped); err != nil {
		writeError(w, http.StatusInternalServerError, codeIllegalArg, "remap failed: "+err.Error())
		return
	}
	a.audit.call("union.remapuuid.applied", map[string]any{"count": len(body.Remapped)})
	w.WriteHeader(http.StatusOK) // legacy: empty 200
}

// handleDiagnose echoes the hub nonce next to our microsecond clock
// (legacy: ['nonce'=>..., 'timestamp'=>microtime(true)] => JSON object with
// exactly these two keys, float seconds).
func (a *API) handleDiagnose(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		body = map[string]any{}
	}
	echo := map[string]any{
		"nonce":     body["nonce"],
		"timestamp": float64(a.now().UnixMicro()) / 1e6,
	}
	writeJSON(w, http.StatusOK, echo)
}

// handleQueryEmail resolves player name -> account email for the union
// blacklist. Mode "off" (and any unknown mode) answers 403 — the privacy
// exposure of this endpoint is deliberate and audited in both directions
// (analysis/yggdrasil-connect.md §7.2).
func (a *API) handleQueryEmail(w http.ResponseWriter, r *http.Request) {
	username := r.URL.Query().Get("username")
	detail := map[string]any{"mode": a.cfg.QueryEmailMode, "username": username}
	if !a.cfg.queryEmailFull() {
		detail["result"] = "denied"
		a.audit.call("union.queryemail", detail)
		writeError(w, http.StatusForbidden, codeForbidden, "email lookup is disabled on this server.")
		return
	}
	if a.hooks.QueryEmailFunc == nil {
		detail["result"] = "error"
		a.audit.call("union.queryemail", detail)
		a.internalError(w, "queryemail handler not configured")
		return
	}
	email, ok, err := a.hooks.QueryEmailFunc(username)
	if err != nil {
		detail["result"] = "error"
		a.audit.call("union.queryemail", detail)
		writeError(w, http.StatusInternalServerError, codeIllegalArg, "email lookup failed: "+err.Error())
		return
	}
	if !ok {
		detail["result"] = "not_found"
		a.audit.call("union.queryemail", detail)
		w.WriteHeader(http.StatusNoContent) // legacy: 204 = no such username
		return
	}
	detail["result"] = "email"
	a.audit.call("union.queryemail", detail) // email value itself is never audited
	writeJSON(w, http.StatusOK, map[string]string{"email": email})
}

// handleUpdatePlugin is deliberately NOT implemented: it is a remote code
// installation channel (the legacy plugin downloads a zip and hot-installs it,
// UpdateController.php:15-35, default-on!). Phase-2 security decision
// (docs/phase2-dev-plan.md §0/§5, analysis §7.2): SkinNexus never installs
// code pushed over the network, so this endpoint answers 403 unconditionally.
func (a *API) handleUpdatePlugin(w http.ResponseWriter, r *http.Request) {
	a.audit.call("union.updateplugin.denied", map[string]any{"reason": "remote_install_never_implemented"})
	writeError(w, http.StatusForbidden, codeForbidden,
		"Remote plugin installation is not supported by SkinNexus (security decision).")
}

// ---- public endpoints ----

// selfDescription is the frozen GET /api/union/member/ document (§4.4):
// field names are interop-critical and must not change.
type selfDescription struct {
	YggdrasilApiVersion string   `json:"yggdrasilApiVersion"`
	ServerListVersion   string   `json:"serverListVersion"`
	PrivateKeyVersion   string   `json:"privateKeyVersion"`
	EnabledFeatures     []string `json:"enabledFeatures"`
}

// handleSelfDescription serves the member capability document (no auth).
func (a *API) handleSelfDescription(w http.ResponseWriter, r *http.Request) {
	features := []string{"unionBlacklist"} // always listed, legacy parity
	if a.cfg.EmailVerification {
		features = append(features, "emailVerification")
	}
	if a.cfg.InvitationCodesForUnion {
		features = append(features, "invitationCodesForUnion")
	}
	if a.cfg.UnionOAuth2 {
		features = append(features, "unionOAuth2")
	}
	w.Header().Set("Access-Control-Allow-Origin", "*") // legacy hello() parity
	writeJSON(w, http.StatusOK, selfDescription{
		YggdrasilApiVersion: a.cfg.YggdrasilApiVersion,
		ServerListVersion:   a.ServerListVersion(),
		PrivateKeyVersion:   a.PrivateKeyVersionString(),
		EnabledFeatures:     features,
	})
}

// ---- shared error helpers ----

// upstreamError reports a hub call failure as 502. Deviation note: the legacy
// plugin relays the hub status verbatim; 502 keeps the cause in the body while
// making "this member could not reach/parse the hub" unambiguous.
func (a *API) upstreamError(w http.ResponseWriter, err error) {
	writeError(w, http.StatusBadGateway, codeUpstream, err.Error())
}

func (a *API) internalError(w http.ResponseWriter, msg string) {
	writeError(w, http.StatusInternalServerError, codeNotSupported, msg)
}
