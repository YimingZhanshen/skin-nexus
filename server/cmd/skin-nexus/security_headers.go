package main

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// The site had no response-header baseline at all: nothing stopped a page being
// framed for a clickjacking, nothing bounded what an injected script could reach,
// and every future escaping mistake would land on an unprotected document.
//
// The CSP deliberately allows 'unsafe-inline' for script and style. Custom CSS/JS
// (internal/appearance), theme packs and plugin bundles are features the operator
// installs on purpose; a nonce-based policy would break all three, and that trade
// is recorded rather than silently made. What the policy does buy is the rest of
// the boundary: no other origin for scripts, no object/embed, no framing, no base
// hijack, forms only to ourselves.
//
// "No other origin for scripts" is also the reason a pasted Google Tag Manager or
// Clarity snippet does nothing but log a CSP violation: those tags create a
// <script> whose src is a foreign host. The operator can now declare the hosts
// they installed on purpose (SKIN_CSP_SCRIPT_EXTRA_SOURCES /
// SKIN_CSP_CONNECT_EXTRA_SOURCES). Empty means the shipped policy, byte for
// byte — the default is still the closed one, and the values are validated here
// rather than pasted through, because a CSP is delimited by exactly the
// characters a hostile value would use.
type cspExtras struct {
	script  []string
	connect []string
}

func appendSources(base string, sources []string) string {
	if len(sources) == 0 {
		return base
	}
	return base + " " + strings.Join(sources, " ")
}

// cspPolicy builds one policy from its directives. The order is the shipped
// order; a caller that passes no extras gets the string the site has always
// sent.
func cspPolicy(ancestors string, extras cspExtras) string {
	return strings.Join([]string{
		"default-src 'self'",
		appendSources("script-src 'self' 'unsafe-inline'", extras.script),
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: blob: http: https:",
		"font-src 'self' data:",
		"media-src 'self'",
		appendSources("connect-src 'self'", extras.connect),
		"frame-src 'self' blob:",
		"base-uri 'self'",
		"form-action 'self'",
		"object-src 'none'",
		ancestors,
	}, "; ")
}

// cspSourceRE is one CSP source expression as an operator is allowed to write
// it: an optional http(s) scheme, an optional leading wildcard label, a host
// made of DNS labels, and an optional port. No path, no quotes, no semicolon,
// no keyword — 'self' and 'unsafe-eval' are not sources an operator needs to
// declare, and a value carrying ';' or a newline would split the header.
var cspSourceRE = regexp.MustCompile(`^(?:https?://)?(?:\*\.)?[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)*(?::[0-9]{1,5})?$`)

// parseCSPSources turns one env value into policy sources, or refuses it.
// Refusing at load is the whole point: a value that is not a source is either a
// typo that would silently widen nothing, or someone trying to write the rest
// of the header for us.
func parseCSPSources(envName, raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !cspSourceRE.MatchString(part) {
			return nil, fmt.Errorf("config: %s: %q is not a CSP source (expected https://host, http://host, host or https://*.host, optional :port; no path, quotes, spaces or semicolons)", envName, part)
		}
		// Port range is checked on the host part only: "http://localhost" has a
		// colon in its scheme and no port at all.
		host := part
		if j := strings.Index(host, "://"); j >= 0 {
			host = host[j+3:]
		}
		if k := strings.LastIndex(host, ":"); k >= 0 {
			port, err := strconv.Atoi(host[k+1:])
			if err != nil || port < 1 || port > 65535 {
				return nil, fmt.Errorf("config: %s: %q has a port outside 1-65535", envName, part)
			}
		}
		out = append(out, part)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// framedAssetPrefix is the one surface this site embeds in its own iframes:
// plugin UI documents served from /plugin-assets/. Everything else refuses to
// be framed at all.
const framedAssetPrefix = "/plugin-assets/"

func isFramedAssetPath(p string) bool { return strings.HasPrefix(p, framedAssetPrefix) }

// hstsPolicy is one year, no subdomains: this site's certificate covers a host,
// not a domain, and includeSubDomains would commit hosts the operator does not
// control here.
const hstsPolicy = "max-age=31536000"

// servedOverTLS is the scheme read the Secure-cookie and ALI-header paths
// already make (langswitch.go, internal/ygg/http.go): TLS on this connection,
// or X-Forwarded-Proto: https from the TLS-terminating proxy deploy.md requires.
// It is deliberately not a config flag — an operator who turns out to be plain
// http must not have told the browser otherwise in advance.
func servedOverTLS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// securityHeaders wraps the whole mux. It sets before any handler writes, so a
// handler that forgets — or a template that gets it wrong — still inherits the
// baseline. The optional extras are the operator's declared analytics origins;
// with none, every response carries the shipped policy unchanged.
func securityHeaders(next http.Handler, extras ...cspExtras) http.Handler {
	var ex cspExtras
	for _, e := range extras {
		ex.script = append(ex.script, e.script...)
		ex.connect = append(ex.connect, e.connect...)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		framed := isFramedAssetPath(r.URL.Path)
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		if framed {
			h.Set("X-Frame-Options", "SAMEORIGIN")
			h.Set("Content-Security-Policy", cspPolicy("frame-ancestors 'self'", ex))
		} else {
			h.Set("X-Frame-Options", "DENY")
			h.Set("Content-Security-Policy", cspPolicy("frame-ancestors 'none'", ex))
		}
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=(), payment=(), usb=(), interest-cohort=()")
		// Only over https: a station on plain http that is told to require https
		// for a year cannot be reached again by its own visitors.
		if servedOverTLS(r) {
			h.Set("Strict-Transport-Security", hstsPolicy)
		}
		next.ServeHTTP(w, r)
	})
}

// sandboxDocument mirrors the flags PluginFrame puts on its iframe, so a plugin
// UI page reached as a top-level navigation runs in an opaque origin (no cookies,
// no same-origin storage) instead of the site's. Same flag set means a browser
// that combines the header with the attribute cannot end up with fewer rights
// than the panel already grants.
func sandboxDocument(w http.ResponseWriter, ctype string) {
	if strings.HasPrefix(ctype, "text/html") {
		w.Header().Set("Sandbox", "allow-scripts allow-forms")
	}
}
