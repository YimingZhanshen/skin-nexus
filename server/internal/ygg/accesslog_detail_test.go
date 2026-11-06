package ygg

// accesslog_detail_test.go — the frozen surface, pinned from the other side.
//
// internal/ygg/accesslog_credentials_test.go owns the always-on rule: no usable
// credential reaches access_logs. Ticket 5c opens exactly one exception (a
// super-admin window, into access_log_details), so the promise this file keeps
// is the one that made the exception acceptable: opening the window must not
// move a single byte of a /api/yggdrasil answer, and the credential must arrive
// through the capture and never through the summary columns.

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"skin-nexus/server/internal/accesslog"
)

const detRequestID = "req-77"

// detLoggedAPI wires one API with the diagnostic window opened (or not) at
// `now`, behind the Observe middleware the production chain installs.
func detLoggedAPI(t *testing.T, open bool, now time.Time) (*API, *memSink, http.Handler) {
	t.Helper()
	api, _ := newTestAPI(t)
	sink := &memSink{}
	l := accesslog.New(sink.put)
	if open {
		vals := map[string]string{
			accesslog.DetailUntilOption: now.Add(30 * time.Minute).Format(time.RFC3339),
		}
		l.SetDiagnostic(&accesslog.Diagnostic{
			Option: func(_ context.Context, k string) (string, bool, error) {
				v, ok := vals[k]
				return v, ok, nil
			},
			Now: func() time.Time { return now },
		})
	}
	api.SetAccess(l)
	return api, sink, accesslog.Observe(api.Handler())
}

// detAuthenticate runs one authenticate through a handler and hands back the
// response with the freshly minted accessToken normalised away, so the two
// windows can be compared byte for byte.
func detAuthenticate(t *testing.T, h http.Handler) (status int, body, token string) {
	t.Helper()
	rec := do(t, h, http.MethodPost, "/api/yggdrasil/authserver/authenticate",
		`{"username":"steve","password":"`+rigPassword+`","clientToken":"`+rigClientToken+`"}`,
		map[string]string{"User-Agent": "TestLauncher/9.9", "X-Request-Id": detRequestID,
			"Cookie": "skin_session=should-not-be-captured"})
	if rec.Code != http.StatusOK {
		t.Fatalf("authenticate = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	token = fieldOf(t, rec.Body.String(), "accessToken")
	return rec.Code, strings.ReplaceAll(rec.Body.String(), token, "<accessToken>"), token
}

func TestDiagnosticWindowDoesNotAlterTheYggdrasilWire(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

	_, closedSink, closedH := detLoggedAPI(t, false, now)
	closedStatus, closedBody, closedToken := detAuthenticate(t, closedH)
	_, openSink, openH := detLoggedAPI(t, true, now)
	openStatus, openBody, openToken := detAuthenticate(t, openH)

	if closedStatus != openStatus || closedBody != openBody {
		t.Errorf("opening the diagnostic window changed the response: (%d) vs (%d)\n%s\n%s",
			closedStatus, openStatus, closedBody, openBody)
	}

	// Summary rule holds in BOTH runs: the minted accessToken stays out of
	// access_logs (who/detail/ua) — that column set is not the credential's home.
	for _, tc := range []struct {
		name  string
		sink  *memSink
		token string
	}{{"closed", closedSink, closedToken}, {"open", openSink, openToken}} {
		for _, e := range tc.sink.entries {
			if strings.Contains(e.Who, tc.token) || strings.Contains(e.Detail, tc.token) ||
				strings.Contains(e.UA, tc.token) {
				t.Errorf("%s window: accessToken reached the summary row: %+v", tc.name, e)
			}
		}
	}

	// Closed: no capture reaches the sink at all.
	for _, e := range closedSink.entries {
		if e.Capture != nil {
			t.Errorf("closed window recorded a detail capture: %+v", e.Capture)
		}
	}

	// Open: exactly the authenticate line carries it, with the credential the
	// window exists to capture and only the whitelisted headers beside it.
	var got *accesslog.Capture
	for i, e := range openSink.entries {
		if e.Kind != accesslog.YggAuthenticate {
			continue
		}
		got = openSink.entries[i].Capture
	}
	if got == nil {
		t.Fatal("open window produced no detail capture for the authenticate line")
	}
	if got.Credential != openToken {
		t.Errorf("credential_ref source = %q, want the minted accessToken %q", got.Credential, openToken)
	}
	hdrs := got.HeadersJSON()
	if !strings.Contains(hdrs, "TestLauncher/9.9") || !strings.Contains(hdrs, detRequestID) {
		t.Errorf("headers_json lost the whitelisted pair: %s", hdrs)
	}
	for _, ban := range []string{"Cookie", "skin_session", "should-not-be-captured"} {
		if strings.Contains(hdrs, ban) {
			t.Errorf("headers_json carries %q: %s", ban, hdrs)
		}
	}
}
