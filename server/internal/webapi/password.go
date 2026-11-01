package webapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"skin-nexus/server/internal/auth"
	"skin-nexus/server/internal/i18n"
	"skin-nexus/server/internal/mailer"
	"skin-nexus/server/internal/store"
	"skin-nexus/server/internal/ygg"
)

// sessionCookieValue returns the caller's raw session token ("" if none).
func sessionCookieValue(r *http.Request) string {
	if c, err := r.Cookie(sessionCookie); err == nil {
		return c.Value
	}
	return ""
}

// handlePasswordChange: POST /api/v1/me/password
// {currentPassword, newPassword}. Legacy semantics (phase2 A2 step 8):
// after a successful change ALL launcher/OIDC authorizations die
// (RevokeUserTokens) — wired via OnPasswordChange in main. The caller's own
// web session stays (they just proved control).
func (b *Backend) handlePasswordChange(w http.ResponseWriter, r *http.Request) {
	uid, err := b.currentUser(r)
	if err != nil {
		writeErrCode(w, http.StatusUnauthorized, "not logged in", i18n.MsgUnauthorized)
		return
	}
	var req struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrCode(w, http.StatusBadRequest, "invalid JSON", i18n.MsgBadBody)
		return
	}
	ok, err := b.Store.VerifyPassword(r.Context(), uid, req.CurrentPassword)
	if err != nil || !ok {
		writeErrCode(w, http.StatusUnauthorized, "current password is incorrect", i18n.MsgCurrentPassword)
		return
	}
	// Passwords may legitimately contain spaces — never trim (review P3:
	// the old TrimSpace silently changed the credential the user typed).
	if len(req.NewPassword) < 8 {
		writeErrCode(w, http.StatusBadRequest, "new password must be at least 8 characters", i18n.MsgPasswordTooShort)
		return
	}
	if req.NewPassword == req.CurrentPassword {
		writeErrCode(w, http.StatusBadRequest, "new password must differ from the current one", i18n.MsgPasswordSame)
		return
	}
	hash, err := auth.HashArgon2id(req.NewPassword)
	if err != nil {
		writeInternal(r, w, err)
		return
	}
	// SetPasswordHash writes UNCONDITIONALLY (chain-trace P0: the legacy-
	// guarded RehashPassword is for lazy upgrade ONLY — reusing it here made
	// the change a silent no-op for argon2id users while tokens were still
	// revoked, turning "change password to stop a leak" into an amplifier).
	if err := b.Store.SetPasswordHash(r.Context(), uid, hash); err != nil {
		writeInternal(r, w, err)
		return
	}
	// Kill every yggdrasil token this user holds (join/validate credentials).
	// Failure is non-fatal to the login session but must leave a trace
	// (review P2: silent swallow hid half-done revocations).
	if err := b.Store.InvalidateUserTokens(r.Context(), uid); err != nil {
		b.Store.InsertAudit(r.Context(), uid, "password_change_token_revoke_failed", map[string]any{"error": err.Error()})
	}
	// Launcher/OIDC grants die too (legacy RevokeUserTokens semantics); the
	// hook is panic-shielded so a bad hook can't cut the response after the
	// hash already changed.
	if b.OnPasswordChange != nil {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					b.Store.InsertAudit(r.Context(), uid, "password_change_hook_panic", map[string]any{"panic": fmt.Sprint(rec)})
				}
			}()
			b.OnPasswordChange(uid)
		}()
	}
	// Other web sessions die too (stolen-session hygiene); the caller's own
	// session survives by design.
	if err := b.Store.DeleteUserSessionsExcept(r.Context(), uid, sessionCookieValue(r)); err != nil {
		b.Store.InsertAudit(r.Context(), uid, "password_change_session_purge_failed", map[string]any{"error": err.Error()})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

// ---- A1 找回密码 (2026-10-26): forgot / reset pair ----
//
// Threat-model decisions:
//   - No enumeration: forgot answers the same {"ok":true} for known and
//     unknown emails; abuse control is rate limiting, not honesty.
//   - No state oracle: reset collapses unknown / expired / already-used
//     into one 400 message.
//   - Tokens: 32 bytes of crypto/rand, hex-encoded; only the SHA-256 digest
//     is stored; 30-minute TTL on the DB clock; single-use atomic consume;
//     a new forgot request invalidates the user's previous tokens.
//   - A successful reset mirrors handlePasswordChange revocation exactly:
//     yggdrasil tokens die, OIDC/launcher grants die (OnPasswordChange),
//     every web session except the caller's own dies.

const resetTokenTTL = 30 * time.Minute

// smtpConfig resolves the SMTP account per request: panel option wins over
// the env-derived default (same precedence as every other settings key).
// Hot by design — save on the settings panel and the next forgot-password
// already uses it; no restart.
func (b *Backend) smtpConfig(ctx context.Context) *mailer.Config {
	get := func(key string) string {
		if v, ok, err := b.Store.GetOption(ctx, key); err == nil && ok {
			return v
		}
		return b.SMTPDefaults[key]
	}
	host := strings.TrimSpace(get("smtp_host"))
	if host == "" {
		return nil
	}
	port := 465
	if p, err := strconv.Atoi(strings.TrimSpace(get("smtp_port"))); err == nil && p > 0 && p < 65536 {
		port = p
	}
	cfg := &mailer.Config{
		Host:     host,
		Port:     port,
		User:     strings.TrimSpace(get("smtp_user")),
		Pass:     get("smtp_pass"),
		From:     strings.TrimSpace(get("smtp_from")),
		SiteName: b.SiteName,
	}
	// A From address is mandatory (header + envelope), and it falls back to
	// smtp_user — with NEITHER set there is nothing to send FROM, so the
	// config is as unusable as a missing host. Treat it as unconfigured so
	// the user gets the honest 503 instead of a 200 that mails nothing
	// (live finding: host-only flip answered 200 while every send died on
	// "empty address" in the log). Config state is address-independent, so
	// the 503 leaks no user existence.
	if cfg.From == "" && cfg.User == "" {
		return nil
	}
	return cfg
}

// handleForgotPassword: POST /api/v1/password/forgot {email}
func (b *Backend) handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if err := jsonDecode(r, &req); err != nil {
		writeErrCode(w, http.StatusBadRequest, "invalid JSON", i18n.MsgBadBody)
		return
	}
	req.Email = strings.TrimSpace(req.Email)
	if req.Email == "" || !mailRe.MatchString(req.Email) {
		writeErrCode(w, http.StatusBadRequest, "email address is not valid", i18n.MsgEmailInvalid)
		return
	}
	// Sliding-window limits in the shared limiter's key space: per IP stops
	// one host spraying many addresses, per address stops one target being
	// mailed repeatedly. (Allow only — Fail/lockout is for login guessing.)
	if b.Limiter != nil {
		if !b.Limiter.Allow("pwf-ip:"+ygg.ClientIP(r)) ||
			!b.Limiter.Allow("pwf-mail:"+strings.ToLower(req.Email)) {
			writeErrCode(w, http.StatusTooManyRequests, "too many requests, please slow down", i18n.MsgRateLimited)
			return
		}
	}
	// Config state, not user data: an unconfigured site must say so instead
	// of pretending a mail went out. Identical for every address.
	cfg := b.smtpConfig(r.Context())
	if cfg == nil {
		writeErrCode(w, http.StatusServiceUnavailable, "mail is not configured on this site, contact the owner", i18n.MsgMailNotConfigured)
		return
	}
	uid, err := b.Store.UserByEmail(r.Context(), req.Email)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeInternal(r, w, err)
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		// Unknown address: identical shape, no mail, no hint.
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		writeInternal(r, w, err)
		return
	}
	token := hex.EncodeToString(raw[:])
	digest := sha256.Sum256([]byte(token))
	if err := b.Store.CreatePasswordResetToken(r.Context(), uid, hex.EncodeToString(digest[:]), resetTokenTTL); err != nil {
		writeInternal(r, w, err)
		return
	}
	link := strings.TrimRight(b.BaseURL, "/") + "/reset-password?token=" + url.QueryEscape(token)
	subject := fmt.Sprintf("%s 密码重置", cfg.SiteName)
	body := fmt.Sprintf(
		"你正在重置 %s 的账号密码。\n\n请在 30 分钟内打开以下链接完成重置：\n%s\n\n若非本人操作，请忽略本邮件——你的密码不会发生任何变化。",
		cfg.SiteName, link)
	// A send failure must NOT change the response shape (it would turn SMTP
	// hiccups into an existence oracle). The user retries; the log carries
	// the diagnostic for the operator.
	if err := cfg.Send(req.Email, subject, body); err != nil {
		log.Printf("password-reset mail to %s: %v", req.Email, err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleResetPassword: POST /api/v1/password/reset {token, newPassword}
func (b *Backend) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := jsonDecode(r, &req); err != nil {
		writeErrCode(w, http.StatusBadRequest, "invalid JSON", i18n.MsgBadBody)
		return
	}
	// Cheap pre-filter only — every failure below collapses to the SAME
	// message so the endpoint never says which check failed.
	badLink := "reset link is invalid or expired, request a new one"
	if len(req.Token) != 64 {
		writeErrCode(w, http.StatusBadRequest, badLink, i18n.MsgResetLink)
		return
	}
	if _, err := hex.DecodeString(req.Token); err != nil {
		writeErrCode(w, http.StatusBadRequest, badLink, i18n.MsgResetLink)
		return
	}
	// Password policy mirrors register/change (never trimmed: spaces are
	// legal credential characters).
	if len(req.Password) < 8 {
		writeErrCode(w, http.StatusBadRequest, "password must be at least 8 characters", i18n.MsgPasswordTooShort)
		return
	}
	if b.Limiter != nil && !b.Limiter.Allow("pwr-ip:"+ygg.ClientIP(r)) {
		writeErrCode(w, http.StatusTooManyRequests, "too many requests, please slow down", i18n.MsgRateLimited)
		return
	}
	digest := sha256.Sum256([]byte(req.Token))
	uid, err := b.Store.ConsumePasswordResetToken(r.Context(), hex.EncodeToString(digest[:]))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErrCode(w, http.StatusBadRequest, badLink, i18n.MsgResetLink)
			return
		}
		writeInternal(r, w, err)
		return
	}
	hash, err := auth.HashArgon2id(req.Password)
	if err != nil {
		writeInternal(r, w, err)
		return
	}
	// SetPasswordHash writes UNCONDITIONALLY (same P0 guard as change).
	if err := b.Store.SetPasswordHash(r.Context(), uid, hash); err != nil {
		writeInternal(r, w, err)
		return
	}
	// Revocation mirrors handlePasswordChange, minus "keep my session": the
	// reset caller is typically not logged in, and a hijacked password must
	// not keep a foothold. A session cookie present here still survives
	// (same hygiene rule as change).
	if err := b.Store.InvalidateUserTokens(r.Context(), uid); err != nil {
		b.Store.InsertAudit(r.Context(), uid, "password_reset_token_revoke_failed", map[string]any{"error": err.Error()})
	}
	if b.OnPasswordChange != nil {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					b.Store.InsertAudit(r.Context(), uid, "password_reset_hook_panic", map[string]any{"panic": fmt.Sprint(rec)})
				}
			}()
			b.OnPasswordChange(uid)
		}()
	}
	if err := b.Store.DeleteUserSessionsExcept(r.Context(), uid, sessionCookieValue(r)); err != nil {
		b.Store.InsertAudit(r.Context(), uid, "password_reset_session_purge_failed", map[string]any{"error": err.Error()})
	}
	b.Store.InsertAudit(r.Context(), uid, "password_reset", map[string]any{"ip": ygg.ClientIP(r)})
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}
