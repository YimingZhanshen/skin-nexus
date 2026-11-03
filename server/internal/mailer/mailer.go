// Package mailer delivers mail over SMTP using only the standard library.
// net/smtp is frozen but sufficient here: opportunistic STARTTLS, PLAIN auth
// (which itself refuses to emit credentials over a clear channel), and a
// writer that dot-stuffs DATA. Zero third-party dependencies — data
// sovereignty rule: everything stays reviewable in-tree.
package mailer

import (
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// Config is the resolved SMTP account. Host == "" (or a nil receiver) means
// mail is disabled and Send always fails fast.
type Config struct {
	Host string // SMTP server hostname; empty = feature off
	Port int    // 465 = implicit TLS; any other port = plain + opportunistic STARTTLS
	User string // AUTH PLAIN identity (most providers want the full email address)
	Pass string // AUTH PLAIN secret (providers call it 授权码, not the login password)
	From string // RFC5322 mailbox: "a@b.c" or "Name <a@b.c>"; empty falls back to User
	// SiteName prefixes the Subject ("{Site} 密码重置").
	SiteName string
	// Budget caps the whole exchange for callers that answer a browser. Zero keeps
	// the default window; see sessionBudget.
	Budget time.Duration
}

// sessionBudget splits the wall clock into a dial window and a session deadline.
// The default is the historical 10s dial + 30s session. The panel's test-send sets
// Budget because its own HTTP client gives up at 20s: a 40s worst case means the
// button reports a network error while the message is still being attempted, which
// is the one answer an operator cannot act on.
func (c *Config) sessionBudget() (dial, session time.Duration) {
	if c.Budget <= 0 {
		return 10 * time.Second, 30 * time.Second
	}
	return c.Budget / 3, c.Budget
}

// Enabled reports whether mail can be attempted at all.
func (c *Config) Enabled() bool { return c != nil && c.Host != "" }

// checkAddr rejects header-injection payloads before anything dials out:
// a bare mailbox may not contain CR/LF or whitespace.
func checkAddr(addr string) error {
	if addr == "" {
		return fmt.Errorf("mailer: empty address")
	}
	if strings.ContainsAny(addr, "\r\n") {
		return fmt.Errorf("mailer: address contains CR/LF")
	}
	return nil
}

// envelopeFrom extracts the bare mailbox from `Name <a@b.c>` (or returns the
// input when it already is one) for the SMTP MAIL FROM command.
func envelopeFrom(from string) (string, error) {
	if i := strings.LastIndex(from, "<"); i >= 0 {
		if j := strings.LastIndex(from, ">"); j > i {
			inner := from[i+1 : j]
			if err := checkAddr(inner); err != nil {
				return "", err
			}
			return inner, nil
		}
	}
	if err := checkAddr(from); err != nil {
		return "", err
	}
	return from, nil
}

// implicitTLS reports whether the port implies TLS from the first byte
// (SMTPS, 465) instead of upgrading via STARTTLS.
func implicitTLS(port int) bool { return port == 465 }

// buildMessage assembles the wire message: RFC5322 headers (subject
// RFC2047-encoded so Chinese survives), base64 body (line-wrapped at 76 —
// 8-bit UTF-8 would depend on the server announcing SMTPUTF8).
func buildMessage(from, to, subject, body string) string {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	enc := base64.StdEncoding.EncodeToString([]byte(body))
	for len(enc) > 76 {
		b.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	b.WriteString(enc + "\r\n")
	return b.String()
}

// Send delivers one plain-text message. Its clock is sessionBudget: a 10s dial and
// a 30s session by default, or the caller's Budget when a browser is waiting.
func (c *Config) Send(to, subject, body string) error {
	if !c.Enabled() {
		return fmt.Errorf("mailer: disabled (smtp_host empty)")
	}
	if err := checkAddr(to); err != nil {
		return err
	}
	from := c.From
	if from == "" {
		from = c.User
	}
	if err := checkAddr(from); err != nil {
		return err
	}
	envFrom, err := envelopeFrom(from)
	if err != nil {
		return err
	}
	port := c.Port
	if port <= 0 || port > 65535 {
		port = 465
	}
	addr := net.JoinHostPort(c.Host, fmt.Sprint(port))
	tlsCfg := &tls.Config{ServerName: c.Host}

	var conn net.Conn
	dialWindow, sessionWindow := c.sessionBudget()
	if implicitTLS(port) {
		conn, err = tls.DialWithDialer(&net.Dialer{Timeout: dialWindow}, "tcp", addr, tlsCfg)
	} else {
		conn, err = net.DialTimeout("tcp", addr, dialWindow)
	}
	if err != nil {
		return fmt.Errorf("mailer: dial %s: %w", addr, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(sessionWindow))

	client, err := smtp.NewClient(conn, c.Host)
	if err != nil {
		return fmt.Errorf("mailer: greeting: %w", err)
	}
	defer client.Close()

	// STARTTLS on plain ports when the server offers it (opportunistic —
	// a server that doesn't advertise it still works on a trusted LAN relay).
	if !implicitTLS(port) {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(tlsCfg); err != nil {
				return fmt.Errorf("mailer: starttls: %w", err)
			}
		}
	}
	// PlainAuth refuses to emit credentials outside TLS/localhost — the
	// stdlib enforces the "never send the password in the clear" rule for us.
	if c.User != "" {
		auth := smtp.PlainAuth("", c.User, c.Pass, c.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("mailer: auth: %w", err)
		}
	}
	if err := client.Mail(envFrom); err != nil {
		return fmt.Errorf("mailer: mail from: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("mailer: rcpt to: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("mailer: data: %w", err)
	}
	if _, err := w.Write([]byte(buildMessage(from, to, subject, body))); err != nil {
		return fmt.Errorf("mailer: write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mailer: finish: %w", err)
	}
	return client.Quit()
}
