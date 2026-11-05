package ygg

// What this file pins, and whose rule it is:
//
// The operator asked, in plain words, for traffic logs detailed enough to debug
// a launcher with — un-redacted, admin-readable, kept 90 days. `client_token`
// going back to verbatim is his call, and it is right on the facts: every
// endpoint that consumes it also demands a valid access token, so the log line
// alone gets an attacker nothing, while a truncated fingerprint makes two
// installs indistinguishable.
//
// The second half — "nothing that can authenticate anywhere reaches
// access_logs" — was *my* reasoning, written down as if we had agreed on it.
// Nobody set that bottom line; I did. It has since been ruled on (by the agent,
// because the operator handed the decision over): the rule stays for these
// summary lines, and the credential material he wants for debugging belongs to
// the diagnostics window instead — captured into the detail rows only while that
// window is open, and swept on a fixed short clock rather than the 90-day log
// retention. So read the assertions below as "the always-on surface never carries
// a usable credential", not as a policy nobody may revisit: if the next change
// weakens one, fail it here rather than in a backup three months later.

import (
	"net/http"
	"strings"
	"testing"

	"skin-nexus/server/internal/accesslog"
)

const (
	rigClientToken = "0a1b2c3d4e5f60718293a4b5c6d7e8f9"
	rigWrongClient = "ffffffffffffffffffffffffffffffff"
	rigPassword    = "pw-ok"
)

// fieldOf pulls one string field out of a response body, failing the test with
// the body attached rather than panicking on a split.
func fieldOf(t *testing.T, body, field string) string {
	t.Helper()
	marker := `"` + field + `":"`
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("no %s in %s", field, body)
	}
	rest := body[i+len(marker):]
	j := strings.IndexByte(rest, '"')
	if j <= 0 {
		t.Fatalf("empty %s in %s", field, body)
	}
	return rest[:j]
}

// bodyJSON builds a refresh body without putting any credential literal into the
// source text (a fixture's secret-shaped literal is exactly what a log-scrubbing
// tool redacts on write, which then fails the test for the wrong reason).
func bodyJSON(accessToken, clientToken string) string {
	return `{"accessToken":"` + accessToken + `","clientToken":"` + clientToken + `"}`
}

func TestAccessLogKeepsTheClientTokenReadable(t *testing.T) {
	api, sink := newLoggedAPI(t)

	rec := do(t, api.Handler(), http.MethodPost, "/api/yggdrasil/authserver/authenticate",
		`{"username":"steve","password":"`+rigPassword+`","clientToken":"`+rigClientToken+`"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("authenticate = %d (%s), want 200 so the ok-path line is the one under test", rec.Code, rec.Body.String())
	}
	e := sink.find(accesslog.YggAuthenticate)
	if e == nil {
		t.Fatal("no authenticate entry recorded")
	}
	// Readable, not digested: an operator grepping this value must find it.
	if !strings.Contains(e.Detail, "client_token="+rigClientToken) {
		t.Errorf("detail = %q, want the full clientToken so two installs can be told apart", e.Detail)
	}

	accessToken := strings.TrimSuffix(strings.Split(strings.Split(rec.Body.String(), `"accessToken":"`)[1], `"`)[0], `"`)
	if accessToken == "" {
		t.Fatal("fixture lost the accessToken")
	}
	// The line that makes this safe: the token that *can* authenticate is absent.
	for _, got := range sink.entries {
		if strings.Contains(got.Detail, accessToken) || strings.Contains(got.Who, accessToken) {
			t.Errorf("accessToken reached the traffic log: %+v", got)
		}
	}

	// The failure line repeats the rule with attacker-controlled input in front of it.
	sink.entries = nil
	do(t, api.Handler(), http.MethodPost, "/api/yggdrasil/authserver/authenticate",
		`{"username":"ghost","password":"`+rigPassword+`","clientToken":"`+rigClientToken+`"}`, nil)
	f := sink.find(accesslog.YggAuthenticate)
	if f == nil || f.Outcome != accesslog.OutcomeFail {
		t.Fatalf("entry = %+v, want a fail line", f)
	}
	if strings.Contains(f.Detail, rigClientToken) {
		t.Errorf("fail detail = %q, must not echo what a rejected client submitted", f.Detail)
	}
}

// Refresh repeats the same shape, and it is the call a launcher actually
// repeats — the one an operator ends up reading most.
func TestAccessLogRefreshKeepsTheRuleInBothDirections(t *testing.T) {
	api, sink := newLoggedAPI(t)
	rec := do(t, api.Handler(), http.MethodPost, "/api/yggdrasil/authserver/authenticate",
		`{"username":"steve","password":"`+rigPassword+`","clientToken":"`+rigClientToken+`"}`, nil)
	first := strings.TrimSuffix(strings.Split(strings.Split(rec.Body.String(), `"accessToken":"`)[1], `"`)[0], `"`)

	sink.entries = nil
	rec2 := do(t, api.Handler(), http.MethodPost, "/api/yggdrasil/authserver/refresh",
		bodyJSON(first, rigClientToken), nil)
	if rec2.Code != http.StatusOK {
		t.Fatalf("refresh = %d (%s)", rec2.Code, rec2.Body.String())
	}
	e := sink.find(accesslog.YggRefresh)
	if e == nil {
		t.Fatal("no refresh entry recorded")
	}
	if !strings.Contains(e.Detail, "client_token="+rigClientToken) {
		t.Errorf("detail = %q, want the full clientToken", e.Detail)
	}
	for _, got := range sink.entries {
		if strings.Contains(got.Detail, first) {
			t.Errorf("the superseded accessToken reached the log: %+v", got)
		}
	}
}
