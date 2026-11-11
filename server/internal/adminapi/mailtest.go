package adminapi

// mailtest.go: "did the mail settings actually work?" (U17).
//
// The panel can save an SMTP account that delivers nothing, and the only way an
// operator finds out is a player's password reset that silently never arrives
// (webapi swallows the send failure on purpose — see the note in
// internal/webapi/password.go, where an error response would be an account
// existence oracle). This is the one place allowed to report the failure,
// because the recipient is the operator themselves.
//
// There is deliberately no `to` field. An endpoint that takes an address is a
// mail relay sitting behind an admin password, and "which address did it send
// to" then becomes a phishing question rather than a configuration one.

import (
	"log"
	"net/http"
	"strings"
	"time"

	"skin-nexus/server/internal/i18n"
	"skin-nexus/server/internal/mailer"
	"skin-nexus/server/internal/redact"
	"skin-nexus/server/internal/webi18n"
)

// handleMailTest: POST /api/admin/v1/mail-test — sends one message to the
// caller's own account address. Body is empty and unused.
func (b *Backend) handleMailTest(w http.ResponseWriter, r *http.Request) {
	uid := actorUID(r)
	if uid == 0 {
		// A token call has no inbox to test into (X-Admin-Token is the machine
		// path; see the same rule on permission changes).
		writeErrCode(w, http.StatusForbidden, "sign in with an admin account to test mail", i18n.MsgWebSessionRequired)
		return
	}
	ctx := r.Context()
	cfg := b.mailConfig(ctx)
	if cfg == nil {
		// A separate code from the player-facing mail.not_configured: the reader here
		// is the operator who has to fix it, so the sentence names the setting instead
		// of telling them to contact an admin.
		writeErrCode(w, http.StatusServiceUnavailable, "this site has no mail account yet; configure SMTP in Settings", i18n.MsgMailAdminNotConfigured)
		return
	}
	// The panel's own client gives up at 20s, so this route may not spend the default
	// 10s dial + 30s session: the operator would see a network error while the send was
	// still running, and no answer is worse than a late one. Every other caller keeps
	// the wide window.
	cfg.Budget = 12 * time.Second
	brief, err := b.Store.UserBrief(ctx, uid)
	if err != nil {
		writeInternal(r, w, err)
		return
	}
	to := strings.TrimSpace(brief.Email)
	if to == "" {
		writeErrCode(w, http.StatusBadRequest, "this account has no email address to test with", i18n.MsgEmailNotSet)
		return
	}
	// Refuse before dialing: a placeholder inbox is an address problem the
	// operator fixes in the profile, not a transport failure to retry.
	if mailer.IsPlaceholderAddress(to) {
		writeErrCode(w, http.StatusBadRequest, "the account address is a placeholder mailbox", i18n.MsgMailRecipientPlaceholder)
		return
	}
	lang := webi18n.ResolveLang(r)
	palette := webi18n.DefaultMailPalette()
	if b.ThemePalette != nil {
		palette = webi18n.PaletteFromTokens(b.ThemePalette())
	}
	themeID := ""
	if b.ThemeID != nil {
		themeID = b.ThemeID()
	}
	subject, text, html := webi18n.TestMail(lang, webi18n.TestMailConfig{
		SiteName: cfg.SiteName, Host: cfg.Host, Port: cfg.Port,
		Sender: cfg.SenderAddress(), Lang: lang, ThemeID: themeID,
	}, palette)
	if err := cfg.SendPart(mailer.Message{To: to, Subject: subject, Text: text, HTML: html, Lang: lang}); err != nil {
		// The raw transport error names the operator's own host and reason, so
		// it goes to the log and the audit row; the player-facing dictionary
		// entry stays generic.
		log.Printf("admin mail-test uid=%d: %v", uid, err)
		b.Store.InsertAudit(ctx, uid, "mail_test_failed", map[string]any{
			"host": cfg.Host, "error": err.Error(),
		})
		writeErrCode(w, http.StatusBadGateway, "the mail server refused the message", i18n.MsgMailSendFailed)
		return
	}
	// Audit discipline matches the settings panel: record shape, not the value
	// (the plaintext address already lives in the users row; the log is an
	// archive anyone with panel access can read).
	masked := maskEmail(to)
	b.Store.InsertAudit(ctx, uid, "mail_test_sent", map[string]any{
		"host": cfg.Host, "to": masked,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "to": masked, "host": cfg.Host,
	})
}

// senderOf mirrors the envelope the transport really uses, so the test letter
// and the diagnostics row name the same mailbox.
func senderOf(cfg *mailer.Config) string { return cfg.SenderAddress() }

// maskEmail keeps the local part's first character and the whole domain.
func maskEmail(email string) string { return redact.Email(email) }
