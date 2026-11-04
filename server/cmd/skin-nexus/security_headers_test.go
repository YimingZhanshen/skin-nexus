package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurityHeadersApplyToEveryResponse(t *testing.T) {
	h := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	for name, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "same-origin",
		"X-Frame-Options":        "DENY",
	} {
		if got := rec.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, must := range []string{"default-src 'self'", "frame-ancestors 'none'", "object-src 'none'", "base-uri 'self'", "form-action 'self'"} {
		if !strings.Contains(csp, must) {
			t.Errorf("CSP is missing %q; full policy: %s", must, csp)
		}
	}
	// Recorded deliberately: custom CSS/JS, theme packs and plugin bundles are
	// inline by design, so this policy is not a defence against an installed
	// malicious package — it is the boundary around everything else.
	if !strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
		t.Errorf("CSP dropped the inline allowance the operator features depend on: %s", csp)
	}
	if !strings.Contains(rec.Header().Get("Permissions-Policy"), "camera=()") {
		t.Error("Permissions-Policy must at least close the device APIs this site never uses")
	}
}

func TestOnlyDocumentsGetSandboxed(t *testing.T) {
	rec := httptest.NewRecorder()
	sandboxDocument(rec, "text/html; charset=utf-8")
	if got := rec.Header().Get("Sandbox"); got != "allow-scripts allow-forms" {
		t.Fatalf("html asset Sandbox = %q", got)
	}

	// A stylesheet or script loaded by the page must not carry the header: those
	// are subresources, and sandboxing them would sandbox the page that includes
	// them (or nothing at all) depending on the browser.
	for _, ctype := range []string{"text/css", "application/javascript", "image/png"} {
		rec := httptest.NewRecorder()
		sandboxDocument(rec, ctype)
		if got := rec.Header().Get("Sandbox"); got != "" {
			t.Fatalf("%s must not get a Sandbox header, got %q", ctype, got)
		}
	}
}
