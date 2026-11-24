package serveraccess

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"

	"skin-nexus/server/internal/accesslog"
	"skin-nexus/server/internal/metrics"
	"skin-nexus/server/internal/store"
	"skin-nexus/server/internal/ygg"
)

type verificationStore interface {
	SACredentialBySecretHash(context.Context, string, time.Time) (store.SACredential, error)
	SAServerByPublicID(context.Context, int64, string) (store.SAServer, error)
	SAServerPolicy(context.Context, int64, int64) (store.SAServerPolicy, error)
	TouchSACredential(context.Context, int64, int64, int64, time.Time) error
	SAWhitelistMember(context.Context, int64, int64, int64) (bool, error)
	SAActiveBan(context.Context, int64, int64, int64, time.Time) (bool, error)
	AdmitSAQuota(context.Context, int64, time.Time, string, int, time.Time) (int64, bool, error)
	InsertSAJoinEvent(context.Context, store.SAJoinEvent) error
	GetOption(context.Context, string) (string, bool, error)
}

type requestPolicy struct {
	store    verificationStore
	now      time.Time
	server   store.SAServer
	policy   store.SAServerPolicy
	identity ygg.VerificationIdentity
	acquire  func(int64, int) func()
	release  func()
}

func (p *requestPolicy) Evaluate(ctx context.Context, identity ygg.VerificationIdentity) (ygg.VerificationDecision, error) {
	p.identity = identity
	if identity.Player != nil {
		banned, err := p.store.SAActiveBan(ctx, p.server.TenantID, p.server.ID, identity.Player.UID, p.now)
		if err != nil {
			return ygg.VerificationDecision{}, err
		}
		if banned {
			return ygg.VerificationDecision{ReasonCode: "tenant_banned"}, nil
		}
	}
	if p.policy.WhitelistEnabled {
		if identity.Player == nil {
			return ygg.VerificationDecision{ReasonCode: "whitelist_unknown_player"}, nil
		}
		member, err := p.store.SAWhitelistMember(ctx, p.server.TenantID, p.server.ID, identity.Player.UID)
		if err != nil {
			return ygg.VerificationDecision{}, err
		}
		if !member {
			return ygg.VerificationDecision{ReasonCode: "not_whitelisted"}, nil
		}
	}
	window := p.now.UTC().Truncate(time.Minute)
	_, allowed, err := p.store.AdmitSAQuota(ctx, p.server.ID, window, "verify", p.policy.VerifyRateLimit, p.now)
	if err != nil {
		return ygg.VerificationDecision{}, err
	}
	if !allowed {
		return ygg.VerificationDecision{ReasonCode: "quota_exceeded"}, nil
	}
	fallback := p.policy.PremiumFallbackEnabled && !p.policy.WhitelistEnabled
	// Concurrency is checked after bans, whitelist and quota, and the slot is
	// held until the verification callback so it also bounds official fallback.
	if p.acquire != nil {
		p.release = p.acquire(p.server.ID, p.policy.MaxConcurrent)
		if p.release == nil {
			return ygg.VerificationDecision{ReasonCode: "concurrent_limit"}, nil
		}
	}
	return ygg.VerificationDecision{Allow: true, AllowFallback: fallback, ReasonCode: "allowed"}, nil
}

func (p *requestPolicy) ResolveFallback(ctx context.Context, _ ygg.VerificationIdentity) (bool, error) {
	if !p.policy.PremiumFallbackEnabled || p.policy.WhitelistEnabled {
		return false, nil
	}
	return saGlobalFallback(ctx, p.store)
}

func saGlobalFallback(ctx context.Context, st verificationStore) (bool, error) {
	value, ok, err := st.GetOption(ctx, "serveraccess_global_fallback")
	if err != nil {
		return false, err
	}
	return ok && (value == "1" || value == "true"), nil
}

type serverConcurrency struct {
	mu     sync.Mutex
	active int
}

type verifier struct {
	cfg         Config
	store       verificationStore
	concurrency map[int64]*serverConcurrency
	mu          sync.Mutex
}

func (v *verifier) acquire(serverID int64, limit int) func() {
	v.mu.Lock()
	entry := v.concurrency[serverID]
	if entry == nil {
		entry = &serverConcurrency{}
		v.concurrency[serverID] = entry
	}
	v.mu.Unlock()
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.active >= limit {
		return nil
	}
	entry.active++
	return func() {
		entry.mu.Lock()
		entry.active--
		entry.mu.Unlock()
	}
}

func (v *verifier) handler(w http.ResponseWriter, r *http.Request) {
	accesslog.SuppressCapture(r.Context())
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if v.cfg.Verifier == nil || v.store == nil {
		http.NotFound(w, r)
		return
	}
	secret, mode, publicID, ok := v.credential(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	credential, err := v.store.SACredentialBySecretHash(r.Context(), saHash(secret), v.now())
	if err != nil || credential.Mode != mode || credential.ServerID <= 0 || !saVerifyScope(credential.Scopes) {
		http.NotFound(w, r)
		return
	}
	if mode == "header" {
		server, err := v.store.SAServerByPublicID(r.Context(), credential.TenantID, publicID)
		if err != nil || server.ID != credential.ServerID || server.Status != "active" {
			http.NotFound(w, r)
			return
		}
	}
	if err := v.store.TouchSACredential(r.Context(), credential.TenantID, credential.ServerID, credential.ID, v.now()); err != nil {
		http.NotFound(w, r)
		return
	}
	policy, err := v.store.SAServerPolicy(r.Context(), credential.TenantID, credential.ServerID)
	if err != nil {
		v.writeUnavailable(w, r, credential, mode, "policy_fault")
		return
	}
	requestID := saRequestID(r)
	w.Header().Set("X-Request-Id", requestID)
	started := v.now()
	requestPolicy := &requestPolicy{store: v.store, now: started, server: store.SAServer{ID: credential.ServerID, TenantID: credential.TenantID}, policy: policy,
		identity: ygg.VerificationIdentity{Username: saText(r.URL.Query().Get("username"), 64)}, acquire: v.acquire}
	defer func() {
		if requestPolicy.release != nil {
			requestPolicy.release()
			requestPolicy.release = nil
		}
	}()
	v.cfg.Verifier.ScopedHasJoinedHandler(requestPolicy, func(result ygg.VerificationResult, verifyErr error) {
		if requestPolicy.release != nil {
			requestPolicy.release()
			requestPolicy.release = nil
		}
		requestPolicy.identity = result.Identity
		v.observe(r, credential, mode, requestID, requestPolicy.identity, result, started)
	}).ServeHTTP(w, r)
}

func (v *verifier) credential(r *http.Request) (secret, mode, publicID string, ok bool) {
	if r.Method != http.MethodGet {
		return "", "", "", false
	}
	secret = r.PathValue("secret")
	publicID = r.PathValue("public_id")
	if secret != "" {
		mode = "url_secret"
	} else {
		mode = "header"
		auth := r.Header.Get("Authorization")
		scheme, value, found := strings.Cut(auth, " ")
		if !found || !strings.EqualFold(scheme, "Bearer") {
			return "", "", "", false
		}
		secret = value
	}
	if len(secret) < 32 || len(secret) > 512 || (mode == "header" && publicID == "") {
		return "", "", "", false
	}
	return secret, mode, publicID, true
}

func saVerifyScope(raw string) bool {
	var scopes []string
	return json.Unmarshal([]byte(raw), &scopes) == nil && saContains(scopes, "verify:join")
}

func saContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func (v *verifier) now() time.Time {
	if v.cfg.Now != nil {
		return v.cfg.Now().UTC()
	}
	return time.Now().UTC()
}

func (v *verifier) writeUnavailable(w http.ResponseWriter, r *http.Request, credential store.SACredential, mode, reason string) {
	requestID := saRequestID(r)
	w.Header().Set("X-Request-Id", requestID)
	identity := ygg.VerificationIdentity{Username: saText(r.URL.Query().Get("username"), 64)}
	v.observe(r, credential, mode, requestID, identity, ygg.VerificationResult{Outcome: "error", ReasonCode: reason}, v.now())
	w.WriteHeader(http.StatusNoContent)
}

func (v *verifier) observe(r *http.Request, credential store.SACredential, mode, requestID string, identity ygg.VerificationIdentity, result ygg.VerificationResult, started time.Time) {
	event := store.SAJoinEvent{TenantID: credential.TenantID, ServerID: credential.ServerID,
		JoinSessionID: sql.NullInt64{Int64: result.JoinSessionID, Valid: result.JoinSessionID > 0},
		UUID:          saPlayerUUID(identity), PlayerName: saText(identity.Username, 64), Outcome: result.Outcome,
		ReasonCode: result.ReasonCode, SourceMode: mode, DurationMS: v.now().Sub(started).Milliseconds(), RequestID: requestID}
	if identity.OfficialUUID != "" {
		event.UUID = identity.OfficialUUID
		event.PlayerName = saText(identity.OfficialName, 64)
	} else if identity.Player != nil {
		event.UID = sql.NullInt64{Int64: identity.Player.UID, Valid: identity.Player.UID > 0}
		if event.UUID == "" {
			event.UUID = identity.Player.UUID
		}
		event.PlayerName = saText(identity.Player.Name, 64)
	}
	if event.DurationMS < 0 {
		event.DurationMS = 0
	}
	if err := v.store.InsertSAJoinEvent(r.Context(), event); err != nil {
		metrics.ServerAccessEventWriteFailures.Inc()
		slog.Error("serveraccess.verify event_write_failed", "request_id", requestID, "tenant_id", event.TenantID, "server_id", event.ServerID)
		if v.cfg.OnEventError != nil {
			func() {
				defer func() {
					if recover() != nil {
						slog.Error("serveraccess.verify event_error_hook_panic", "request_id", requestID, "tenant_id", event.TenantID, "server_id", event.ServerID)
					}
				}()
				v.cfg.OnEventError(r.Context(), event, err)
			}()
		}
	}
}

func saPlayerUUID(identity ygg.VerificationIdentity) string {
	if identity.Player == nil {
		return ""
	}
	return identity.Player.UUID
}

func saRequestID(r *http.Request) string {
	value := r.Header.Get("X-Request-Id")
	if value == "" {
		value, _ = saRandomToken()
	}
	var clean strings.Builder
	for _, ch := range value {
		if unicode.IsControl(ch) || unicode.IsSpace(ch) {
			continue
		}
		clean.WriteRune(ch)
	}
	value = clean.String()
	if value == "" {
		return "unavailable"
	}
	return saText(value, 128)
}

func (v *verifier) routes(mux *http.ServeMux) {
	mux.Handle("GET /v/{secret}/sessionserver/session/minecraft/hasJoined", http.HandlerFunc(v.handler))
	mux.Handle("GET /api/serveraccess/v1/servers/{public_id}/hasJoined", http.HandlerFunc(v.handler))
}

var _ ygg.VerificationFallbackPolicy = (*requestPolicy)(nil)
var _ ygg.VerificationPolicy = (*requestPolicy)(nil)
