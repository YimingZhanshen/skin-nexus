package mailer

import "strings"

// IsPlaceholderAddress reports a mailbox that can never receive mail: a domain
// with no dot (admin@localhost) or the literal localhost. The set is CLOSED —
// .local / .internal / .test are not included, because a LAN relay can
// legitimately deliver to them and refusing would break a working intranet
// deployment. Callers pass the bare address, not a `Name <a@b.c>` header.
func IsPlaceholderAddress(addr string) bool {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return true
	}
	i := strings.LastIndex(addr, "@")
	if i < 0 || i == len(addr)-1 {
		return true
	}
	domain := strings.ToLower(addr[i+1:])
	return domain == "localhost" || !strings.Contains(domain, ".")
}
