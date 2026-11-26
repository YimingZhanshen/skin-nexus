// Package origingate provides the shared Origin and Fetch Metadata check used
// by cookie-authenticated mutation boundaries.
package origingate

import (
	"net/http"
	"net/url"
	"strings"
)

// SameOrigin reports whether the request carries acceptable same-origin
// evidence. A non-null Origin is authoritative and must match the request Host;
// otherwise Fetch Metadata must say same-origin or none. Missing evidence fails
// closed.
func SameOrigin(r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" && origin != "null" {
		u, err := url.Parse(origin)
		return err == nil && strings.EqualFold(u.Host, r.Host)
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		return site == "same-origin" || site == "none"
	}
	return false
}
