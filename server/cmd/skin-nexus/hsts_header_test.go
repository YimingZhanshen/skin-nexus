package main

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

// HSTS is the one header in this set that cannot be unconditional: a station
// served over plain http (a LAN box, a container port someone pokes directly,
// a cert that lapsed) must NOT tell browsers to force https forever — that
// locks the operator out of their own site. So the gate is the same scheme
// read the Secure-cookie and ALI-header paths already use: TLS on this
// connection, or X-Forwarded-Proto: https from the proxy deploy.md mandates.
//
// The reason this moved into the app at all: 宝塔's generated server block does
// declare Strict-Transport-Security, and it reaches nobody. nginx inherits
// add_header into a level only when that level defines none of its own, and the
// proxy location defines `add_header X-Cache`, so the whole server-level set —
// HSTS included — is silently dropped. Measured live: zero HSTS on every 200
// response of a running station whose operator believes it is on. A header that
// depends on the proxy's directive layout is a header that quietly disappears,
// so the site sends it itself and the operator has one less thing to configure.
func TestHSTSIsSentOnlyWhenTheSchemeIsHTTPS(t *testing.T) {
	h := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))

	cases := []struct {
		name  string
		tls   bool
		forpt string // "" = header absent
		want  string
	}{
		{"plain http", false, "", ""},
		{"proxy says http", false, "http", ""},
		{"direct tls", true, "", "max-age=31536000"},
		{"proxy says https", false, "https", "max-age=31536000"},
		{"tls and forwarded https", true, "https", "max-age=31536000"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if c.tls {
				req.TLS = &tls.ConnectionState{}
			}
			if c.forpt != "" {
				req.Header.Set("X-Forwarded-Proto", c.forpt)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			got := rec.Header().Get("Strict-Transport-Security")
			if got != c.want {
				t.Fatalf("Strict-Transport-Security = %q, want %q", got, c.want)
			}
			// Exactly one value: a proxy that also adds the header is the CORS
			// bug all over again, and two policies are not merged — the browser
			// keeps one and the operator cannot tell which.
			if n := len(rec.Header().Values("Strict-Transport-Security")); n > 1 {
				t.Fatalf("Strict-Transport-Security appears %d times, want at most 1", n)
			}
		})
	}
}
