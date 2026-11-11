// message.go: wire composition. The transport (Send/SendPart) lives in
// mailer.go; everything about what the bytes look like lives here so the
// multipart structure and header order can be tested without a socket.

package mailer

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"mime"
	"net/mail"
	"strings"
	"time"
)

// Message is one letter. HTML == "" keeps the historical plain-text wire shape
// (body byte-identical); a non-empty HTML adds a multipart/alternative pair.
type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
	Lang    string // Content-Language; empty omits the header
}

// SenderAddress is the bare envelope mailbox, shared by the test letter's
// summary, the diagnostics mail row and the envelope itself so the four places
// that name the sender cannot drift apart.
func (c *Config) SenderAddress() string {
	if c == nil {
		return ""
	}
	from := c.From
	if from == "" {
		from = c.User
	}
	addr, err := envelopeFrom(from)
	if err != nil {
		return from
	}
	return addr
}

// composeFromHeader builds the From header and the envelope mailbox. An empty
// From falls back to the account with the site name as display name (RFC2047
// encoded when non-ASCII); an operator-supplied `Name <a@b.c>` wins verbatim.
// A parse failure falls back to today's envelopeFrom behaviour: composing a
// header must never introduce a new way for a send to fail.
func composeFromHeader(c *Config) (header, envelope string, err error) {
	raw := c.From
	if raw == "" {
		raw = c.User
	}
	envelope, err = envelopeFrom(raw)
	if err != nil {
		return "", "", err
	}
	display := ""
	if c.From != "" {
		if addr, perr := mail.ParseAddress(c.From); perr == nil {
			display = addr.Name
		}
	}
	if display == "" {
		display = c.SiteName
	}
	if display == "" {
		return envelope, envelope, nil
	}
	return mime.QEncoding.Encode("utf-8", display) + " <" + envelope + ">", envelope, nil
}

// compose renders the whole wire message. boundary and msgID are injected so
// tests can pin them; production passes fresh random values.
func compose(fromHeader string, m Message, boundary, msgID string, now time.Time) string {
	var b strings.Builder
	b.WriteString("From: " + fromHeader + "\r\n")
	b.WriteString("To: " + m.To + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", m.Subject) + "\r\n")
	b.WriteString("Date: " + now.Format(time.RFC1123Z) + "\r\n")
	b.WriteString("Message-ID: <" + msgID + ">\r\n")
	b.WriteString("Auto-Submitted: auto-generated\r\n")
	if m.Lang != "" {
		b.WriteString("Content-Language: " + m.Lang + "\r\n")
	}
	b.WriteString("MIME-Version: 1.0\r\n")
	if m.HTML == "" {
		b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
		b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
		writeBase64Body(&b, m.Text)
		return b.String()
	}
	b.WriteString("Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n\r\n")
	b.WriteString(buildMultipart(m, boundary))
	return b.String()
}

// buildMultipart emits plain first, HTML last: RFC 2046 makes the LAST part the
// preferred one, so HTML-capable clients pick the HTML and the rest keep text.
// Each part carries its own base64 CTE; a CTE on multipart/* itself is illegal.
func buildMultipart(m Message, boundary string) string {
	var b strings.Builder
	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	writeBase64Body(&b, m.Text)
	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/html; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	writeBase64Body(&b, m.HTML)
	b.WriteString("--" + boundary + "--\r\n")
	return b.String()
}

// writeBase64Body keeps the whole letter 7-bit clean (no SMTPUTF8 dependency)
// and wraps at 76 columns, the historical plain-text encoding.
func writeBase64Body(b *strings.Builder, s string) {
	enc := base64.StdEncoding.EncodeToString([]byte(s))
	for len(enc) > 76 {
		b.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	b.WriteString(enc + "\r\n")
}

// newMessageID names the envelope domain, falling back to the SMTP host when
// the account is a bare local name. Missing Message-ID costs spam score.
func newMessageID(envelope, host string) string {
	domain := host
	if i := strings.LastIndex(envelope, "@"); i >= 0 && i < len(envelope)-1 {
		domain = envelope[i+1:]
	}
	var r [8]byte
	_, _ = rand.Read(r[:])
	return fmt.Sprintf("%d.%s@%s", time.Now().UnixNano(), hex.EncodeToString(r[:]), domain)
}

// newBoundary picks a token that appears in neither body; on the astronomically
// unlikely collision (or a dead crypto/rand) it derives one from the content.
func newBoundary(text, html string) string {
	for i := 0; i < 4; i++ {
		var r [16]byte
		if _, err := rand.Read(r[:]); err != nil {
			break
		}
		cand := "sn-" + hex.EncodeToString(r[:])
		if !strings.Contains(text, cand) && !strings.Contains(html, cand) {
			return cand
		}
	}
	sum := sha256.Sum256([]byte(text + "\x00" + html))
	return "sn-" + hex.EncodeToString(sum[:])[:32]
}
