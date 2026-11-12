// Package mailer delivers mail over SMTP using only the standard library.
// net/smtp is frozen but sufficient here: opportunistic STARTTLS, PLAIN auth
// (which itself refuses to emit credentials over a clear channel), and a
// writer that dot-stuffs DATA. Zero third-party dependencies — data
// sovereignty rule: everything stays reviewable in-tree.
package mailer

import (
	"crypto/tls"
	"fmt"
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
//
// The defaults are package vars so tests can shrink the window without setting
// Budget (Budget > 0 disables the retry, which is exactly what some tests exercise).
var (
	defaultDialWindow    = 10 * time.Second
	defaultSessionWindow = 30 * time.Second
)

func (c *Config) sessionBudget() (dial, session time.Duration) {
	if c.Budget <= 0 {
		return defaultDialWindow, defaultSessionWindow
	}
	return c.Budget / 3, c.Budget
}

// Enabled reports whether mail can be attempted at all.
func (c *Config) Enabled() bool { return c != nil && c.Host != "" }

// checkAddr rejects header-injection payloads before anything dials out:
// a bare mailbox may not contain CR/LF (the only bytes that can start a new
// header line). Whitespace is left to the caller's address validation.
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

// buildMessage assembles the historical plain-text wire message. Kept as a
// thin shell over compose so the encoding tests keep pinning the same bytes.
func buildMessage(from, to, subject, body string) string {
	header, _, err := composeFromHeader(&Config{From: from})
	if err != nil {
		header = from
	}
	return compose(header, Message{To: to, Subject: subject, Text: body}, "", newMessageID(from, ""), time.Now())
}

// Send delivers one plain-text message. Its clock is sessionBudget: a 10s dial and
// a 30s session by default, or the caller's Budget when a browser is waiting.
func (c *Config) Send(to, subject, body string) error {
	return c.SendPart(Message{To: to, Subject: subject, Text: body})
}

// SendPart delivers a Message, plain or multipart/alternative. One absolute
// deadline covers the whole exchange including a single retry: a transient
// failure (wedged dial, greylist, dropped connection) gets one more attempt
// after a short backoff, but only when nobody is waiting on the answer
// (Budget > 0 means a browser is) and only while time remains. A failure after
// the server accepted DATA is never retried — that would deliver twice.
func (c *Config) SendPart(m Message) error {
	if !c.Enabled() {
		return fmt.Errorf("mailer: disabled (smtp_host empty)")
	}
	if err := checkAddr(m.To); err != nil {
		return err
	}
	header, envFrom, err := composeFromHeader(c)
	if err != nil {
		return err
	}
	port := c.Port
	if port <= 0 || port > 65535 {
		port = 465
	}
	addr := net.JoinHostPort(c.Host, fmt.Sprint(port))
	tlsCfg := &tls.Config{ServerName: c.Host}
	dialWindow, sessionWindow := c.sessionBudget()
	deadline := time.Now().Add(sessionWindow)

	err, committed := c.attempt(m, header, envFrom, addr, port, tlsCfg, dialWindow, deadline)
	if err == nil || committed || c.Budget > 0 || !retryable(err) {
		return err
	}
	const backoff = 500 * time.Millisecond
	if time.Now().Add(backoff + 200*time.Millisecond).After(deadline) {
		return err
	}
	time.Sleep(backoff)
	dial2 := dialWindow
	if remaining := time.Until(deadline); remaining < dial2 {
		dial2 = remaining
	}
	err2, _ := c.attempt(m, header, envFrom, addr, port, tlsCfg, dial2, deadline)
	if err2 == nil {
		return nil
	}
	return fmt.Errorf("%w (first attempt: %v)", err2, err)
}

// attempt runs one SMTP exchange against the shared absolute deadline. The
// second return value reports whether the server already queued the message,
// which forbids any retry regardless of the error that follows.
func (c *Config) attempt(m Message, header, envFrom, addr string, port int, tlsCfg *tls.Config, dialWindow time.Duration, deadline time.Time) (error, bool) {
	var conn net.Conn
	var err error
	if implicitTLS(port) {
		conn, err = tls.DialWithDialer(&net.Dialer{Timeout: dialWindow}, "tcp", addr, tlsCfg)
	} else {
		conn, err = net.DialTimeout("tcp", addr, dialWindow)
	}
	if err != nil {
		return fmt.Errorf("mailer: dial %s: %w", addr, err), false
	}
	defer conn.Close()
	_ = conn.SetDeadline(deadline)

	client, err := smtp.NewClient(conn, c.Host)
	if err != nil {
		return fmt.Errorf("mailer: greeting: %w", err), false
	}
	defer client.Close()

	// STARTTLS on plain ports when the server offers it (opportunistic —
	// a server that doesn't advertise it still works on a trusted LAN relay).
	if !implicitTLS(c.Port) {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(tlsCfg); err != nil {
				return fmt.Errorf("mailer: starttls: %w", err), false
			}
		}
	}
	// PlainAuth refuses to emit credentials outside TLS/localhost — the
	// stdlib enforces the "never send the password in the clear" rule for us.
	if c.User != "" {
		auth := smtp.PlainAuth("", c.User, c.Pass, c.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("mailer: auth: %w", err), false
		}
	}
	if err := client.Mail(envFrom); err != nil {
		return fmt.Errorf("mailer: mail from: %w", err), false
	}
	if err := client.Rcpt(m.To); err != nil {
		return fmt.Errorf("mailer: rcpt to: %w", err), false
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("mailer: data: %w", err), false
	}
	msg := compose(header, m, newBoundary(m.Text, m.HTML), newMessageID(envFrom, c.Host), time.Now())
	if _, err := w.Write([]byte(msg)); err != nil {
		return fmt.Errorf("mailer: write: %w", err), false
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mailer: finish: %w", err), false
	}
	// DATA accepted: everything from here on is post-commit.
	if err := client.Quit(); err != nil {
		return &committedError{fmt.Errorf("mailer: quit: %w", err)}, true
	}
	return nil, true
}
