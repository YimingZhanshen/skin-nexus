package union

// urlnorm.go — one station, one identity.
//
// The hub's server list is hand-written on fifty stations, so the same station
// arrives spelled several ways: host case, a trailing slash, credentials left
// in the URL, the yggdrasil API path appended. Two spellings of one station
// are two rows in the panel (the Vue row key is code + '|' + bs_root) and two
// entries in the whitelist consumer's view, so this file reduces a base URL to
// the one spelling this station compares as an identity.
//
// Deliberately NOT the same as memberNameTarget: that one refuses credentials
// and IP literals because it decides whether to fetch on the operator's
// behalf, and it drops the path because a peer must not aim our requests.
// This one only decides sameness — it folds what is not part of who the
// station is (scheme case, host case, credentials, query, fragment, a
// trailing /authserver) and keeps what is (the port, the rest of the path,
// whose case is significant).

import (
	"net/url"
	"strings"
)

// CanonicalRoot is the identity spelling of a station's base URL. ok is false
// when raw is not an http(s) URL with a host — there is no identity to fold,
// so callers keep the raw value instead of inventing one.
func CanonicalRoot(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", false
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return "", false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", false
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	// Trailing slashes are not part of the address, and a trailing
	// /authserver is the yggdrasil API path rather than the station.
	p := strings.TrimRight(u.Path, "/")
	p = strings.TrimSuffix(p, "/authserver")
	u.Path = p
	u.RawPath = "" // stale encoding of the path we just rewrote
	return u.String(), true
}

// CanonicalizeServers rewrites every row's bs_root to its canonical spelling
// and drops a row that collapses onto an earlier one. Rows whose address
// cannot be folded keep their raw value, so nothing the hub sent is hidden —
// it just stops being a second copy of a row already shown. A different code
// or a different station is a different row and survives: the hub really does
// ship one code twice with two addresses, and that is two rows on purpose.
func CanonicalizeServers(list []ServerEntry) []ServerEntry {
	out := make([]ServerEntry, 0, len(list))
	seen := make(map[string]bool, len(list))
	for _, s := range list {
		key := s.Code + "\x00" + s.BSRoot
		if root, ok := CanonicalRoot(s.BSRoot); ok {
			s.BSRoot = root
			key = s.Code + "\x00" + root
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	return out
}
