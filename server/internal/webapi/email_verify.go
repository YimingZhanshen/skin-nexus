// Email verification (2026-11-02). The `require_verification` join gate and
// the `email_verified` column existed long before this file, but nothing could
// set the flag except an admin clicking a box — so the gate was only ever a
// way to lock a site's players out. This is the missing half: a bearer link
// sent to the account's own address, a single-use token, and a grace window.
//
// Threat-model decisions, inherited from the password-reset flow it mirrors:
//   - the request endpoint is session-scoped and takes no address, so it can
//     not be pointed at somebody else's mailbox;
//   - confirm collapses unknown / expired / already-used into one message;
//   - only the SHA-256 digest is stored, expiry is compared on the DB clock;
//   - changing the address clears the flag and burns outstanding links.
package webapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"skin-nexus/server/internal/i18n"
	"skin-nexus/server/internal/mailer"
	"skin-nexus/server/internal/redact"
	"skin-nexus/server/internal/store"
	"skin-nexus/server/internal/webi18n"
	"skin-nexus/server/internal/ygg"
)

// errPlaceholderRecipient marks an address that can never receive mail. The
// request endpoint translates it to a 400 the owner can act on; the detached
// registration path only logs it, keeping the register response shape intact.
var errPlaceholderRecipient = errors.New("recipient address is a placeholder mailbox")

// verifyTokenTTL is longer than a reset link's: a verification mail may sit in
// an inbox until the player opens Minecraft, and the token is single-use
// anyway.
const verifyTokenTTL = 24 * time.Hour

// verifyMailTimeout bounds the detached registration send; the mailer itself
// enforces a 30s deadline per message.
const verifyMailTimeout = 45 * time.Second

// verificationEnabled reports the gate's live state (nil = not enforced).
func (b *Backend) verificationEnabled(ctx context.Context) bool {
	return b.VerificationEnabled != nil && b.VerificationEnabled(ctx)
}

// mailReplyBudget bounds a send whose caller is a browser still waiting for an
// answer. The mailer's own window is 10s dial + 30s session; the SPA aborts its
// requests at 15s (web/src/api.js), so an honest 502 written after 30s is a
// spinner that became a network error — the one answer the player cannot act
// on. A var so the deadline test does not have to wait the real window.
var mailReplyBudget = 8 * time.Second

// sendVerificationMail mints a token for uid and mails a link to address.
// Failure is returned so the caller decides what it means: the request endpoint
// tells the owner (only they can see it), registration only logs it — a broken
// SMTP host must never change a protocol response shape. A zero budget keeps
// the mailer's own window, which is right when nobody is waiting on the answer.
func (b *Backend) sendVerificationMail(ctx context.Context, uid int64, address string, budget time.Duration, lang string) error {
	cfg := b.smtpConfig(ctx)
	if cfg == nil {
		return errors.New("mail is not configured")
	}
	if mailer.IsPlaceholderAddress(address) {
		return errPlaceholderRecipient
	}
	if budget > 0 {
		cfg.Budget = budget
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return err
	}
	token := hex.EncodeToString(raw[:])
	digest := sha256.Sum256([]byte(token))
	if err := b.Store.CreateEmailVerifyToken(ctx, uid, hex.EncodeToString(digest[:]), verifyTokenTTL); err != nil {
		return err
	}
	link := strings.TrimRight(b.BaseURL, "/") + "/email-verify?token=" + token
	subject, text, html := webi18n.VerifyMail(lang, cfg.SiteName, link, 24*60, b.mailPalette())
	if err := cfg.SendPart(mailer.Message{To: address, Subject: subject, Text: text, HTML: html, Lang: lang}); err != nil {
		// The link never left the server, so the credential must not stay
		// behind: an unread token is still a live one for whoever dumps the DB.
		if delErr := b.Store.DeleteEmailVerifyToken(ctx, hex.EncodeToString(digest[:])); delErr != nil {
			log.Printf("email-verification token cleanup uid=%d: %v", uid, delErr)
		}
		log.Printf("email-verification mail to %s (uid %d): %v", redact.Email(address), uid, err)
		b.Store.InsertAudit(ctx, uid, "email_verify_mail_failed", map[string]any{
			"to": redact.Email(address), "error": err.Error(),
		})
		return err
	}
	return nil
}

// sendVerificationMailDetached fires the same send off the request: the mailer
// runs to a 30s deadline, and registration must not stall behind a SMTP host
// that is down (the SPA gives up at 15s and would show a network error for an
// account that was in fact created). The context outlives the request on
// purpose.
func (b *Backend) sendVerificationMailDetached(uid int64, address string, lang string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), verifyMailTimeout)
		defer cancel()
		if err := b.sendVerificationMail(ctx, uid, address, 0, lang); err != nil {
			log.Printf("register %d: verification mail: %v", uid, err)
		}
	}()
}

// handleEmailVerifyRequest: POST /api/v1/email/verify — mail a fresh link to
// the signed-in account's own address. No body: the address comes from the DB.
func (b *Backend) handleEmailVerifyRequest(w http.ResponseWriter, r *http.Request) {
	uid, err := b.currentUser(r)
	if err != nil {
		writeErrCode(w, http.StatusUnauthorized, "not logged in", i18n.MsgUnauthorized)
		return
	}
	if b.Limiter != nil &&
		(!b.Limiter.Allow("evf-ip:"+ygg.ClientIP(r)) || !b.Limiter.Allow(fmt.Sprintf("evf-uid:%d", uid))) {
		writeErrCode(w, http.StatusTooManyRequests, "too many requests, please slow down", i18n.MsgRateLimited)
		return
	}
	if cfg := b.smtpConfig(r.Context()); cfg == nil {
		// Config state, not user data: identical for every account.
		writeErrCode(w, http.StatusServiceUnavailable, "mail is not configured on this site, contact the owner", i18n.MsgMailNotConfigured)
		return
	}
	brief, err := b.Store.UserBrief(r.Context(), uid)
	if err != nil {
		writeInternal(r, w, err)
		return
	}
	if strings.TrimSpace(brief.Email) == "" {
		writeErrCode(w, http.StatusBadRequest, "this account has no email address, add one first", i18n.MsgEmailNotSet)
		return
	}
	if brief.EmailVerified {
		writeErrCode(w, http.StatusConflict, "this email address is already verified", i18n.MsgEmailAlreadyVerified)
		return
	}
	// The owner of this mailbox is the only party that can be told about a
	// failure, so the honest answer is safe here (unlike the reset flow).
	if err := b.sendVerificationMail(r.Context(), uid, brief.Email, mailReplyBudget, webi18n.ResolveLang(r)); err != nil {
		if errors.Is(err, errPlaceholderRecipient) {
			writeErrCode(w, http.StatusBadRequest, "the account address is a placeholder mailbox", i18n.MsgMailRecipientPlaceholder)
			return
		}
		writeLogged(r, w, http.StatusBadGateway, i18n.MsgEmailSendFailed,
			"could not send the verification mail, try again later", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleEmailVerifyConfirm: POST /api/v1/email/confirm {token} — consumes the
// link and sets the flag. Session-free on purpose: the link is the credential
// and the player may open it on another device.
func (b *Backend) handleEmailVerifyConfirm(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := jsonDecode(r, &req); err != nil {
		writeErrCode(w, http.StatusBadRequest, "invalid JSON", i18n.MsgBadBody)
		return
	}
	// Cheap pre-filter only — every failure below answers the same way.
	badLink := "verification link is invalid or expired, request a new one"
	if len(req.Token) != 64 {
		writeErrCode(w, http.StatusBadRequest, badLink, i18n.MsgEmailLink)
		return
	}
	if _, err := hex.DecodeString(req.Token); err != nil {
		writeErrCode(w, http.StatusBadRequest, badLink, i18n.MsgEmailLink)
		return
	}
	if b.Limiter != nil && !b.Limiter.Allow("evc-ip:"+ygg.ClientIP(r)) {
		writeErrCode(w, http.StatusTooManyRequests, "too many requests, please slow down", i18n.MsgRateLimited)
		return
	}
	digest := sha256.Sum256([]byte(req.Token))
	uid, err := b.Store.ConsumeEmailVerifyToken(r.Context(), hex.EncodeToString(digest[:]))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErrCode(w, http.StatusBadRequest, badLink, i18n.MsgEmailLink)
			return
		}
		writeInternal(r, w, err)
		return
	}
	if err := b.Store.SetEmailVerified(r.Context(), uid, true); err != nil {
		writeInternal(r, w, err)
		return
	}
	b.Store.InsertAudit(r.Context(), uid, "email_verified", map[string]any{"ip": ygg.ClientIP(r)})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
