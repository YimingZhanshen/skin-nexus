// Package redact turns personal values into something safe to write to a log.
// Logs outlive the request: they are shipped, retained and read by whoever has
// shell access, so an address collected for one purpose must not become a
// permanent copy there.
package redact

import "strings"

// Email keeps the first character of the local part and the whole domain —
// enough to answer "did the right account get mail" without storing the address.
func Email(addr string) string {
	at := strings.LastIndex(addr, "@")
	if at <= 0 {
		// One mask for "no address", "no local part" and "not an address at all":
		// the reader only needs to know nothing was revealed.
		return "***"
	}
	local := addr[:at]
	return local[:1] + "***" + addr[at:]
}
