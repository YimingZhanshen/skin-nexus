package main

import (
	"net/http"
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

const pageCSP = "default-src 'self'; " +
	"script-src 'self' 'unsafe-inline'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob: http: https:; " +
	"font-src 'self' data:; " +
	"media-src 'self'; " +
	"connect-src 'self'; " +
	"frame-src 'self' blob:; " +
	"frame-ancestors 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'; " +
	"object-src 'none'"

// securityHeaders wraps the whole mux. It sets before any handler writes, so a
// handler that forgets — or a template that gets it wrong — still inherits the
// baseline.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", pageCSP)
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=(), payment=(), usb=(), interest-cohort=()")
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
