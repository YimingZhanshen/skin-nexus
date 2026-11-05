// Package redact turns personal values into something safe to write to a log.
// Logs outlive the request: they are shipped, retained and read by whoever has
// shell access, so an address collected for one purpose must not become a
// permanent copy there.
package redact

import (
	"strings"
	"unicode/utf8"
)

// Email keeps the first character of the local part and the whole domain —
// enough to answer "did the right account get mail" without storing the address.
func Email(addr string) string {
	at := strings.LastIndex(addr, "@")
	if at <= 0 {
		// One mask for "no address", "no local part" and "not an address at all":
		// the reader only needs to know nothing was revealed.
		return "***"
	}
	// First rune, not first byte: a local part in any non-ASCII script would
	// otherwise contribute half a character to a log line that is read forever.
	first, _ := utf8.DecodeRuneInString(addr[:at])
	if first == utf8.RuneError {
		return "***"
	}
	return string(first) + "***" + addr[at:]
}

// Identifier masks whatever a login form received. That field is either a
// nickname or an e-mail address, and only the second one turns the access log
// into a permanent copy of personal data — so a nickname is kept as written,
// because operators use it to answer "which account was this".
func Identifier(s string) string {
	if !strings.Contains(s, "@") {
		return s
	}
	return Email(s)
}
