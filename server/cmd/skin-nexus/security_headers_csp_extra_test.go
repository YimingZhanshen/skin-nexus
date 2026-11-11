package main

// security_headers_csp_extra_test.go — the operator-declared analytics origins.
//
// The shipped policy says script-src 'self' 'unsafe-inline', which is correct
// for a skin server that ships its own code and refuses to become the host of
// somebody else's script by accident. It also means the custom-JS box cannot
// load Google Tag Manager or Microsoft Clarity: those create a <script> whose
// src is a foreign origin, and the browser blocks it before the tag ever runs.
// The webmaster hit exactly that on the demo site (2026-11-12), so the policy
// now takes a per-site list of extra sources — empty by default, and validated
// at load, because a value that is not a CSP source is a header injection
// attempt (or a typo) and must never reach the wire.
//
// The default expectation below is transcribed from the response header the
// live demo site served on 2026-11-12, not from the Go constant, so a change to
// the shipped policy fails here instead of being echoed by itself.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const wantDefaultPageCSP = "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: http: https:; font-src 'self' data:; media-src 'self'; connect-src 'self'; frame-src 'self' blob:; base-uri 'self'; form-action 'self'; object-src 'none'; frame-ancestors 'none'"

func servedCSP(t *testing.T, path string, extras ...cspExtras) (string, string) {
	t.Helper()
	h := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}), extras...)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Header().Get("Content-Security-Policy"), rec.Header().Get("X-Frame-Options")
}

// TestCSPWithoutExtrasIsByteForByte is the "nothing changed by default" gate:
// an operator who never sets the new variables must receive the same header the
// site has always sent.
func TestCSPWithoutExtrasIsByteForByte(t *testing.T) {
	csp, _ := servedCSP(t, "/")
	if csp != wantDefaultPageCSP {
		t.Errorf("default policy drifted:\n got %s\nwant %s", csp, wantDefaultPageCSP)
	}
	csp, xfo := servedCSP(t, "/plugin-assets/demo/ui.html", cspExtras{})
	if xfo != "SAMEORIGIN" || !strings.Contains(csp, "frame-ancestors 'self'") {
		t.Errorf("the framed-plugin exemption moved: xfo=%q csp=%s", xfo, csp)
	}
}

func TestCSPExtraSourcesLandInTheRightDirectives(t *testing.T) {
	csp, _ := servedCSP(t, "/", cspExtras{
		script:  []string{"https://www.googletagmanager.com", "https://www.clarity.ms"},
		connect: []string{"https://www.clarity.ms", "https://www.google-analytics.com"},
	})
	for _, want := range []string{
		"script-src 'self' 'unsafe-inline' https://www.googletagmanager.com https://www.clarity.ms;",
		"connect-src 'self' https://www.clarity.ms https://www.google-analytics.com;",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("policy is missing %q\nfull: %s", want, csp)
		}
	}
	// Everything the policy is for stays: the extras widen two directives, they
	// do not rewrite the boundary around framing, objects or form targets.
	for _, must := range []string{
		"default-src 'self'", "object-src 'none'", "base-uri 'self'",
		"form-action 'self'", "frame-ancestors 'none'", "style-src 'self' 'unsafe-inline'",
	} {
		if !strings.Contains(csp, must) {
			t.Errorf("an extra source cost us %q\nfull: %s", must, csp)
		}
	}
	if strings.Contains(csp, "unsafe-eval") {
		t.Errorf("'unsafe-eval' appeared from a source list: %s", csp)
	}
}

// The plugin pages are the same operator's site: if the analytics tag is
// declared, it is declared for every document, so the framed variant carries
// the same extras while keeping its own frame-ancestors.
func TestFramedCSPCarriesTheSameExtras(t *testing.T) {
	csp, xfo := servedCSP(t, "/plugin-assets/demo/ui.html", cspExtras{
		script: []string{"https://www.clarity.ms"},
	})
	if xfo != "SAMEORIGIN" {
		t.Errorf("framed XFO = %q, want SAMEORIGIN", xfo)
	}
	if !strings.Contains(csp, "script-src 'self' 'unsafe-inline' https://www.clarity.ms;") {
		t.Errorf("framed policy lost the declared source: %s", csp)
	}
	if !strings.Contains(csp, "frame-ancestors 'self'") {
		t.Errorf("framed policy must still allow our own frames: %s", csp)
	}
}

func TestParseCSPSourcesAcceptsRealAnalyticsTags(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"https://www.googletagmanager.com", "https://www.googletagmanager.com"},
		{"http://localhost:8085", "http://localhost:8085"},
		{"clarity.ms", "clarity.ms"},
		{"https://*.clarity.ms", "https://*.clarity.ms"},
		{"https://a.example.com, https://b.example.com", "https://a.example.com https://b.example.com"},
		{"  https://a.example.com ,, https://b.example.com  ", "https://a.example.com https://b.example.com"},
	} {
		got, err := parseCSPSources("SKIN_CSP_SCRIPT_EXTRA_SOURCES", tc.raw)
		if err != nil {
			t.Errorf("parseCSPSources(%q) failed: %v", tc.raw, err)
			continue
		}
		if joined := strings.Join(got, " "); joined != tc.want {
			t.Errorf("parseCSPSources(%q) = %q, want %q", tc.raw, joined, tc.want)
		}
	}
	if got, err := parseCSPSources("SKIN_CSP_SCRIPT_EXTRA_SOURCES", ""); err != nil || len(got) != 0 {
		t.Errorf(`empty value must mean "no extras, shipped policy unchanged", got %v / %v`, got, err)
	}
}

// Every rejected shape below is either a header-injection attempt or the kind
// of typo that would silently do nothing. A value that is not one CSP source
// must fail the start instead of reaching the response, because the policy is
// delimited by the very characters these values contain.
func TestParseCSPSourcesRejectsInjectionAndGarbage(t *testing.T) {
	for _, bad := range []string{
		"https://a.example.com; frame-ancestors 'self'",
		"https://a.example.com; object-src *",
		"https://a.example.com\nX-Frame-Options: DENY",
		"'unsafe-eval'",
		"'self'",
		"*",
		"https://*",
		"https://a.example.com/evil.js",
		"data:",
		"blob:",
		"https://",
		"not a url at all",
		"ftp://a.example.com",
		"javascript:alert(1)",
		"https://a.example.com:99999",
		"https://a exam.com",
		"-a.example.com",
		"https://a.example.com,\"https://evil\",connect-src *",
	} {
		if got, err := parseCSPSources("SKIN_CSP_SCRIPT_EXTRA_SOURCES", bad); err == nil {
			t.Errorf("parseCSPSources(%q) accepted %v; a non-source must fail the start", bad, got)
		}
	}
}
