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
	"fmt"
	"log"
	"net/http"
	"strings"

	"skin-nexus/server/internal/i18n"
	"skin-nexus/server/internal/mailer"
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
		// The honest answer to "why did no player ever get a reset link".
		writeErrCode(w, http.StatusServiceUnavailable, "this site has no mail account configured", i18n.MsgMailNotConfigured)
		return
	}
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
	subject := fmt.Sprintf("%s 邮件发送测试", cfg.SiteName)
	body := fmt.Sprintf(
		"这是 %s 管理面板「部署自检 → 发送测试邮件」发出的一封测试信。\n\n"+
			"收到它就说明站点真的能把邮件送出去——找回密码、邮箱验证、站内通知走的是同一条路。\n"+
			"发信账号：%s:%d（信封发件人 %s）\n\n若在本机的收件箱里找不到它，请检查垃圾箱与域名解析。",
		cfg.SiteName, cfg.Host, cfg.Port, senderOf(cfg))
	if err := cfg.Send(to, subject, body); err != nil {
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

// senderOf mirrors mailer.Config.Send's fallback so the test letter states the
// address actually used on the envelope.
func senderOf(cfg *mailer.Config) string {
	if cfg.From != "" {
		return cfg.From
	}
	return cfg.User
}

// maskEmail keeps the local part's first character and the whole domain:
// enough to confirm "yes, that is my address", not enough to harvest.
func maskEmail(email string) string {
	at := strings.LastIndex(email, "@")
	if at <= 0 {
		return "***"
	}
	return email[:1] + "***" + email[at:]
}
