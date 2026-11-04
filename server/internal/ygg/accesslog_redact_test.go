package ygg

// The traffic log is admin-readable and rows are kept for 90 days, so a line
// that quotes a launcher's clientToken turns the panel into a store of usable
// credentials — the same defect class as keeping tokens in plaintext at rest,
// reached by a different table. The log still needs something to correlate on
// (「这台启动器今晚重登了六次」), so it records a digest prefix instead.
//
// The expected fingerprint below is not produced by the function under test: it
// is sha256 of the literal, computed once by hand
// (`printf '%s' <token> | sha256sum | cut -c1-12`), which is what makes the
// assertion say something about the code rather than about itself.

import (
	"net/http"
	"strings"
	"testing"

	"skin-nexus/server/internal/accesslog"
)

const (
	testClientToken   = "3a7f1c9b8e2d4f60a1b2c3d4e5f60718"
	testClientTokenFP = "6cf3afda7762"
)

func TestAccessLogRecordsAClientTokenFingerprintNotTheToken(t *testing.T) {
	api, sink := newLoggedAPI(t)

	rec := do(t, api.Handler(), http.MethodPost, "/api/yggdrasil/authserver/authenticate",
		`{"username":"steve","password":"pw-ok","clientToken":"`+testClientToken+`"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("authenticate = %d (%s), want 200 so the ok-path line is the one under test", rec.Code, rec.Body.String())
	}
	e := sink.find(accesslog.YggAuthenticate)
	if e == nil {
		t.Fatal("no authenticate entry recorded")
	}
	if strings.Contains(e.Detail, testClientToken) {
		t.Errorf("detail holds the credential verbatim: %q", e.Detail)
	}
	if !strings.Contains(e.Detail, testClientTokenFP) {
		t.Errorf("detail = %q, want the hand-computed sha256 prefix %s so an operator can still correlate clients",
			e.Detail, testClientTokenFP)
	}
	if !strings.Contains(e.Detail, "client_token=") {
		t.Errorf("detail = %q, want the key name kept — operators grep for it", e.Detail)
	}
	// The wire response is the frozen part: the launcher must still get its own
	// clientToken back, digest or not.
	if body := rec.Body.String(); !strings.Contains(body, testClientToken) {
		t.Errorf("authenticate response lost the clientToken: %s", body)
	}

	// Refresh takes the same shape of line, and it is the call a launcher repeats.
	sink.entries = nil
	tok := strings.TrimSuffix(strings.Split(strings.Split(rec.Body.String(), `"accessToken":"`)[1], `"`)[0], `"`)
	do(t, api.Handler(), http.MethodPost, "/api/yggdrasil/authserver/refresh",
		`{"accessToken":"`+tok+`","clientToken":"`+testClientToken+`"}`, nil)
	r := sink.find(accesslog.YggRefresh)
	if r == nil {
		t.Fatal("no refresh entry recorded")
	}
	if strings.Contains(r.Detail, testClientToken) {
		t.Errorf("refresh detail holds the credential verbatim: %q", r.Detail)
	}
	if !strings.Contains(r.Detail, testClientTokenFP) {
		t.Errorf("refresh detail = %q, want the fingerprint", r.Detail)
	}
}

// A failed login must not start quoting whatever the client sent either: the
// failure line is written from attacker-controlled input.
func TestAccessLogFailureLineCarriesNoCredentialMaterial(t *testing.T) {
	api, sink := newLoggedAPI(t)
	do(t, api.Handler(), http.MethodPost, "/api/yggdrasil/authserver/authenticate",
		`{"username":"ghost","password":"pw-bad","clientToken":"`+testClientToken+`"}`, nil)
	e := sink.find(accesslog.YggAuthenticate)
	if e == nil || e.Outcome != accesslog.OutcomeFail {
		t.Fatalf("entry = %+v, want a fail line", e)
	}
	if strings.Contains(e.Detail, testClientToken) {
		t.Errorf("fail detail = %q, must not repeat submitted credentials", e.Detail)
	}
}
