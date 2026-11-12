package oidc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"

	"skin-nexus/server/internal/i18n"
	"skin-nexus/server/internal/webi18n"
)

// Config is the provider bootstrap configuration (SKIN_OIDC_* env vars).
type Config struct {
	Issuer         string // e.g. https://skin.example.com (site root, per contract)
	Key            *rsa.PrivateKey
	KID            string // JWK key id (kid) — launchers match on it
	SharedClientID string // discovery shared_client_id ("2" by default)
	LoginURL       string // SPA login page for the consent/selection step
	DeviceFormPath string // verification page path, "/device"
	DeviceLifetime time.Duration
	DevicePollSecs int
}

// Provider owns the zitadel OP plus the contract-shaped wrappers.
type Provider struct {
	cfg     Config
	storage *Storage
	engine  *op.Provider
	backend Backend

	// SessionResolver maps a site session cookie value to a user id
	// (wired from webapi's session store in main).
	SessionResolver func(ctx context.Context, sessionToken string) (int64, error)

	// Audit receives security-relevant events (device approve/deny, token
	// revocation). Nil = silent (tests); wired to the audit_log in main.
	Audit func(event string, detail map[string]any)
}

// auditEvent is a nil-safe fire-and-forget emit.
func (p *Provider) auditEvent(event string, detail map[string]any) {
	if p.Audit != nil {
		p.Audit(event, detail)
	}
}

// New builds the provider and its engine. Clients and keys are persisted via
// backend; the shared public client is seeded on first boot.
func New(backend Backend, profiles ProfileSource, cfg Config) (*Provider, error) {
	if cfg.DeviceFormPath == "" {
		cfg.DeviceFormPath = "/device"
	}
	if cfg.DeviceLifetime == 0 {
		cfg.DeviceLifetime = 10 * time.Minute // Janus parity: expires_in 600
	}
	if cfg.DevicePollSecs == 0 {
		cfg.DevicePollSecs = 5
	}
	kid := cfg.KID
	if kid == "" {
		kid = keyFingerprint(&cfg.Key.PublicKey)
	}
	storage := NewStorage(backend, profiles, cfg.Key, kid, cfg.Issuer)

	engine, err := op.NewProvider(&op.Config{
		CryptoKey:             randomCryptoKey(),
		CodeMethodS256:        true,
		AuthMethodPost:        true, // client_secret_post (legacy compat)
		GrantTypeRefreshToken: true,
		SupportedScopes: []string{
			"openid", "profile", "email", "offline_access",
			"Yggdrasil.PlayerProfiles.Read", "Yggdrasil.PlayerProfiles.Select",
			"Yggdrasil.Server.Join",
		},
		SupportedClaims: []string{
			"sub", "iss", "aud", "exp", "iat", "auth_time", "sid",
			"email", "email_verified", "nickname", "picture",
			"selectedProfile", "availableProfiles",
		},
		DeviceAuthorization: op.DeviceAuthorizationConfig{
			Lifetime:     cfg.DeviceLifetime,
			PollInterval: time.Duration(cfg.DevicePollSecs) * time.Second,
			UserFormPath: cfg.DeviceFormPath,
			UserCode:     op.UserCodeBase20, // XXXX-XXXX — matches live Janus shape
		},
	}, storage, op.StaticIssuer(cfg.Issuer))
	if err != nil {
		return nil, err
	}
	p := &Provider{cfg: cfg, storage: storage, engine: engine, backend: backend}
	return p, nil
}

// SeedSharedClient upserts the public launcher client advertised as
// discovery shared_client_id.
func (p *Provider) SeedSharedClient(ctx context.Context) error {
	seeder, ok := p.backend.(interface {
		EnsureOidcClient(context.Context, ClientRecord) error
	})
	if !ok {
		return errors.New("oidc: backend cannot seed clients")
	}
	return seeder.EnsureOidcClient(ctx, ClientRecord{
		ID:   p.cfg.SharedClientID,
		Name: "Shared launcher client",
		GrantTypes: []string{
			"urn:ietf:params:oauth:grant-type:device_code",
			"refresh_token",
			"authorization_code",
		},
		RedirectURIs: nil,
		Scopes:       nil,
	})
}

// Wrap routes OIDC-owned paths to the provider and everything else to next
// (the SPA handler). This keeps one root mux in main without path collisions.
func (p *Provider) Wrap(next http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/.well-known/openid-configuration", p.DiscoveryHandler())
	mux.Handle("/userinfo", p.UserinfoHandler())
	mux.Handle("/yggc/userinfo", p.UserinfoHandler())
	// /device is the SPA verification page (DevicePage.vue, path-probed in
	// App.vue); the minimal no-JS fallback moved to /device.html.
	mux.Handle("/device.html", p.DeviceFormHandler())
	mux.Handle("/api/v1/oidc/", p.DeviceAPIHandler())
	mux.Handle("/authorize", p.engine.HttpHandler())
	mux.Handle("/oauth/token", p.tokenWrapper(p.engine.HttpHandler()))
	mux.Handle("/oauth/introspect", p.engine.HttpHandler())
	mux.Handle("/authorize/callback", p.engine.HttpHandler())
	mux.Handle("/revoke", p.engine.HttpHandler())
	mux.Handle("/end_session", p.engine.HttpHandler())
	mux.Handle("/keys", p.engine.HttpHandler())
	mux.Handle("/device_authorization", p.engine.HttpHandler())
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if handler, pattern := mux.Handler(r); pattern != "" {
			handler.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// DiscoveryHandler serves the discovery document with the client contract
// fields injected (shared_client_id et al.). It is also mounted at the
// yggdrasil API root so the launchers' "/.well-known/ truncation" heuristic
// resolves the API root correctly (launchers' "/.well-known/ truncation"
// heuristic).
func (p *Provider) DiscoveryHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The engine serves discovery only at "/.well-known/openid-configuration";
		// when this handler is mounted at the yggdrasil API root the forwarded
		// path must be normalized or the engine 404s (live bug, 2026-10-23).
		rr := r.Clone(r.Context())
		u := *r.URL
		u.Path = "/.well-known/openid-configuration"
		rr.URL = &u
		rec := httptest.NewRecorder()
		p.engine.HttpHandler().ServeHTTP(rec, rr.WithContext(context.WithValue(r.Context(), issuerCtxKey, p.cfg.Issuer)))
		if rec.Code != http.StatusOK {
			w.WriteHeader(rec.Code)
			_, _ = w.Write(rec.Body.Bytes())
			return
		}
		var doc map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
			http.Error(w, "discovery decode failed", http.StatusInternalServerError)
			return
		}
		// Contract overrides (synthesis §1/§2).
		doc["shared_client_id"] = p.cfg.SharedClientID
		doc["id_token_signing_alg_values_supported"] = []string{"RS256"}
		doc["scopes_supported"] = []string{
			"openid", "profile", "email", "offline_access",
			"Yggdrasil.PlayerProfiles.Select", "Yggdrasil.PlayerProfiles.Read",
			"Yggdrasil.Server.Join",
		}
		doc["claims_supported"] = []string{
			"email", "email_verified", "nickname", "picture",
			"selectedProfile", "availableProfiles", "sub", "sid", "auth_time", "iss",
		}
		doc["authorization_response_iss_parameter_supported"] = true
		doc["token_endpoint_auth_methods_supported"] = []string{"client_secret_post", "none"}
		doc["response_modes_supported"] = []string{"form_post", "fragment", "query"}
		writeJSON(w, doc)
	})
}

// UserinfoHandler serves the OIDC userinfo with the exact legacy shape and
// error contract (401 + WWW-Authenticate: Bearer realm="openid").
func (p *Provider) UserinfoHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		raw := bearerToken(r)
		if raw == "" {
			unauthorized(w)
			return
		}
		rec, err := VerifyBearer(r.Context(), p.backend, p.cfg.Issuer, &p.cfg.Key.PublicKey, raw)
		if err != nil {
			unauthorized(w)
			return
		}
		claims, err := p.storage.privateClaims(r.Context(), rec.UserID, rec.SelectedPID, rec.Scopes)
		if err != nil {
			unauthorized(w)
			return
		}
		u, err := p.storage.profiles.User(r.Context(), rec.UserID)
		if err != nil {
			unauthorized(w)
			return
		}
		out := map[string]any{
			"sub": strconv.FormatInt(rec.UserID, 10),
		}
		for k, v := range claims {
			out[k] = v
		}
		for _, sc := range rec.Scopes {
			switch sc {
			case "profile":
				out["nickname"] = u.Nickname
			case "email":
				out["email"] = u.Email
				out["email_verified"] = u.EmailVerified
			}
		}
		writeJSON(w, out)
	})
}

// DeviceFormHandler serves the no-JS-fallback verification page (moved from
// /device to /device.html when the SPA DevicePage took over /device). Its
// buttons now fetch() the JSON submit endpoint in place.
//
// The launcher opens THIS url, so a reader who never reaches the SPA still
// needs the page in their own language: the labels come from the same catalog
// the texture pages use, resolved from the request.
func (p *Provider) DeviceFormHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var sb strings.Builder
		if err := deviceFormTmpl.Execute(&sb, map[string]string{"Lang": webi18n.ResolveLang(r)}); err != nil {
			http.Error(w, "device page", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(sb.String()))
	})
}

var deviceFormTmpl = template.Must(template.New("device").Funcs(template.FuncMap{
	"t": webi18n.T,
}).Parse(`<!doctype html><html lang="{{.Lang}}"><head><meta charset="utf-8"><title>{{t .Lang "device.title"}}</title></head><body>
<div id="app">{{t .Lang "device.loading"}}</div>
<script>
const L = {
  done: {{t .Lang "device.done"}},
  failed: {{t .Lang "device.failed"}},
  unknown: {{t .Lang "device.unknown"}},
  submitFailed: {{t .Lang "device.submitFailed"}},
  loadFailed: {{t .Lang "device.loadFailed"}},
  authorize: {{t .Lang "device.authorize"}},
  userCode: {{t .Lang "device.userCode"}},
  scopes: {{t .Lang "device.scopes"}},
  approve: {{t .Lang "device.approve"}},
  deny: {{t .Lang "device.deny"}},
};
const fill = (s, err) => s.replace("{err}", err);
const code = new URLSearchParams(location.search).get("user_code") || "";
function esc(s){return String(s).replace(/[&<>"']/g, c=>({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;","'":"&#39;"}[c]));}
function submit(decision){
  const sel=document.querySelector('input[name=pid]:checked');
  fetch("/api/v1/oidc/device/submit",{method:"POST",credentials:"include",
    headers:{"Content-Type":"application/json"},
    body:JSON.stringify({user_code:code,decision:decision,pid:sel?Number(sel.value):0})})
   .then(r=>r.json()).then(d=>{
     document.getElementById("app").textContent = d.ok ? L.done : fill(L.failed, d.error || L.unknown);
   }).catch(e=>{document.getElementById("app").textContent = fill(L.submitFailed, e);});
}
fetch("/api/v1/oidc/device/info?user_code="+encodeURIComponent(code),{credentials:"include"})
 .then(r=>r.json()).then(d=>{
   if(d.error){document.getElementById("app").textContent=d.error;return;}
   const players=(d.players||[]).map(p=>'<label><input type="radio" name="pid" value="'+p.id+'" '+(p.id===d.default_pid?'checked':'')+'>'+esc(p.name)+' ('+esc(p.uuid)+')</label><br>').join("");
   document.getElementById("app").innerHTML='<h2>'+esc(L.authorize.replace("{client}", esc(d.client_name||d.client_id)))+'</h2>'
     +'<p>'+L.userCode+'<b>'+esc(d.user_code)+'</b></p><p>'+L.scopes+(d.scopes||[]).map(esc).join(", ")+'</p>'
     +players
     +'<button onclick="submit(\'approve\')">'+L.approve+'</button> <button onclick="submit(\'deny\')">'+L.deny+'</button>';
 }).catch(e=>{document.getElementById("app").textContent = fill(L.loadFailed, e);});
</script></body></html>`))

// DeviceAPIHandler serves the approval JSON/API surface used by the
// verification page. Session auth is injected (SessionResolver).
func (p *Provider) DeviceAPIHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/oidc/device/info", p.deviceInfo)
	mux.HandleFunc("POST /api/v1/oidc/device/submit", p.deviceSubmit)
	mux.HandleFunc("GET /api/v1/oidc/tokens", p.listTokens)
	mux.HandleFunc("POST /api/v1/oidc/tokens/revoke", p.revokeToken)
	return mux
}

func (p *Provider) deviceInfo(w http.ResponseWriter, r *http.Request) {
	userCode := r.URL.Query().Get("user_code")
	rec, err := p.backend.GetDeviceAuthByUserCode(r.Context(), userCode)
	if err != nil || rec.Done || rec.Denied || time.Now().After(rec.ExpiresAt) {
		writeJSON(w, errEnvelope(i18n.MsgDeviceCodeInvalid, "invalid or expired user code"))
		return
	}
	var players []PlayerBrief
	var defaultPID int64
	if uid, serr := p.sessionUser(r); serr == nil {
		if pls, perr := p.storage.profiles.Players(r.Context(), uid); perr == nil {
			players = pls
			if len(players) == 1 {
				defaultPID = players[0].ID
			}
		}
	}
	out := map[string]any{
		"user_code":   rec.UserCode,
		"client_id":   rec.ClientID,
		"scopes":      rec.Scopes,
		"players":     players,
		"default_pid": defaultPID,
	}
	writeJSON(w, out)
}

func (p *Provider) deviceSubmit(w http.ResponseWriter, r *http.Request) {
	uid, err := p.sessionUser(r)
	if err != nil {
		unauthorized(w)
		return
	}
	// Accepts JSON (SPA DevicePage) and form-encoded (fallback page);
	// always answers JSON {ok:true}|{error}.
	var userCode, decision string
	var pid int64
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var body struct {
			UserCode string `json:"user_code"`
			Decision string `json:"decision"`
			PID      int64  `json:"pid"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, errEnvelope(i18n.MsgDeviceFormInvalid, "the consent form could not be read"))
			return
		}
		userCode, decision, pid = body.UserCode, body.Decision, body.PID
	} else {
		if err := r.ParseForm(); err != nil {
			writeJSON(w, errEnvelope(i18n.MsgDeviceFormInvalid, "the consent form could not be read"))
			return
		}
		userCode = r.FormValue("user_code")
		decision = r.FormValue("decision")
		pid, _ = strconv.ParseInt(r.FormValue("pid"), 10, 64)
	}
	rec, err := p.backend.GetDeviceAuthByUserCode(r.Context(), userCode)
	if err != nil || rec.Done || rec.Denied {
		writeJSON(w, errEnvelope(i18n.MsgDeviceCodeInvalid, "invalid user code"))
		return
	}
	if time.Now().After(rec.ExpiresAt) {
		// review P2: expired codes could be "approved" (ok:true) yet never yield tokens
		writeJSON(w, errEnvelope(i18n.MsgDeviceCodeExpired, "user code expired"))
		return
	}
	switch decision {
	case "approve":
		if err := p.ApproveDevice(r.Context(), uid, userCode, pid); err != nil {
			writeJSON(w, errEnvelope(i18n.MsgDeviceApproveFail, "approval failed"))
			return
		}
		p.auditEvent("oidc.device_approve", map[string]any{
			"uid": uid, "user_code": userCode, "client_id": rec.ClientID, "pid": pid,
		})
	case "deny":
		if err := p.DenyDevice(r.Context(), userCode); err != nil {
			writeJSON(w, errEnvelope(i18n.MsgDeviceDenyFailed, "denial failed, please try again"))
			return
		}
		p.auditEvent("oidc.device_deny", map[string]any{
			"uid": uid, "user_code": userCode, "client_id": rec.ClientID,
		})
	default:
		writeJSON(w, errEnvelope(i18n.MsgDeviceDecisionUnknown, "the decision is neither approve nor deny"))
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// ApproveDevice marks a device authorization as approved (subject + profile).
func (p *Provider) ApproveDevice(ctx context.Context, uid int64, userCode string, pid int64) error {
	rec, err := p.backend.GetDeviceAuthByUserCode(ctx, userCode)
	if err != nil {
		return err
	}
	rec.Done = true
	rec.Denied = false
	rec.UserID = uid
	rec.SelectedPID = pid
	rec.AuthTime = time.Now()
	return p.backend.UpdateDeviceAuth(ctx, rec)
}

// DenyDevice marks a device authorization as denied (access_denied on poll).
func (p *Provider) DenyDevice(ctx context.Context, userCode string) error {
	rec, err := p.backend.GetDeviceAuthByUserCode(ctx, userCode)
	if err != nil {
		return err
	}
	rec.Denied = true
	return p.backend.UpdateDeviceAuth(ctx, rec)
}

// RevokeUserTokens revokes every ledger token of a user (password change,
// signout). Refresh tokens are covered by RevokeUserAccessTokens' sibling.
func (p *Provider) RevokeUserTokens(ctx context.Context, uid int64) error {
	return p.backend.RevokeUserAccessTokens(ctx, uid)
}

// RevokeUserAllTokens kills every access AND refresh token the user holds —
// the password-change semantic (phase2 A2 step 8: 改密 → 所有启动器授权失效).
// Both steps are attempted even when the first fails (review P1: a DB blip
// used to skip refresh revocation silently); the combined error is returned
// so callers can log/audit instead of claiming success.
func (p *Provider) RevokeUserAllTokens(ctx context.Context, uid int64) error {
	err := p.backend.RevokeUserAccessTokens(ctx, uid)
	if err2 := p.backend.RevokeUserRefreshTokens(ctx, uid); err == nil {
		err = err2
	}
	return err
}

func (p *Provider) sessionUser(r *http.Request) (int64, error) {
	if p.SessionResolver == nil {
		return 0, errors.New("oidc: no session resolver")
	}
	tok := sessionCookie(r)
	if tok == "" {
		return 0, errors.New("oidc: no session")
	}
	return p.SessionResolver(r.Context(), tok)
}

// sessionCookie reads the site session cookie (same name webapi issues).
var sessionCookie = func(r *http.Request) string {
	if c, err := r.Cookie("skin_session"); err == nil {
		return c.Value
	}
	return ""
}

// tokenWrapper re-shapes device polling errors to the launcher contract:
// HTTP 400 + {error,...} (RFC 8628 states). If the engine already complies it
// passes through untouched.
func (p *Provider) tokenWrapper(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Let the storage layer flag genuine store faults on this request; see
		// storeFaultKey in storage.go for why the flag is needed here.
		fault := &storeFault{}
		r = r.WithContext(context.WithValue(r.Context(), storeFaultKey{}, fault))
		rec := httptest.NewRecorder()
		next.ServeHTTP(rec, r)
		body := rec.Body.Bytes()
		status := rec.Code
		var oe struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &oe) == nil && oe.Error != "" {
			switch oe.Error {
			case "authorization_pending", "slow_down", "access_denied",
				"expired_token", "authorization_declined":
				status = http.StatusBadRequest
			}
			// zitadel wraps every refresh-lookup error in ErrInvalidGrant, so
			// a store fault surfaces here as 400 invalid_grant — which tells
			// the launcher to discard a perfectly good refresh token. The
			// storage layer recorded the fault; restore the real answer.
			if fault.hit && oe.Error == "invalid_grant" {
				status = http.StatusInternalServerError
				body, _ = json.Marshal(oidc.ErrServerError())
			}
		}
		for k, vs := range rec.Header() {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		// body length may have changed in the rewrite above
		w.Header().Del("Content-Length")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	})
}

// ---- helpers ----

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="openid"`)
	w.WriteHeader(http.StatusUnauthorized)
}

// errEnvelope is the standard envelope: `code` is the half the page can
// translate, the English text is what an older client falls back to. The four
// answers this replaced were Chinese literals, so the device page showed
// Chinese to every language but the one it was written in.
func errEnvelope(code, msg string) map[string]any {
	return map[string]any{"error": code, "code": code, "message": msg}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func randomCryptoKey() (k [32]byte) {
	_, _ = rand.Read(k[:])
	return k
}

func keyFingerprint(pub *rsa.PublicKey) string {
	b, _ := json.Marshal(map[string]any{"kty": "RSA", "n": pub.N.String(), "e": pub.E})
	sum := sha256.Sum256(b)
	return base64.RawURLEncoding.EncodeToString(sum[:])[:22] // stable short kid
}

// issuerCtxKey lets the discovery wrapper force the configured issuer.
type ctxKey string

const issuerCtxKey ctxKey = "oidc-issuer"
