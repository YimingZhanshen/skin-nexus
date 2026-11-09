package union

// Diagnose cause attribution (2026-11-10 round, slice D1).
//
// The legacy plugin collapses every hub-side or transport failure into one
// free-text `exception` row, so an operator reads "失败" and nothing about
// why. The two failures that actually happen in the field are opposite in
// remedy — a wrong member key (hub answered 401/403: fix the key in the panel)
// versus an unreachable hub (fix DNS/firewall) — and today both land in the
// same string. `cause` is a stable machine-readable token the UI labels.
//
// Vocabulary (contract with web/src/components/admin/UnionView.vue):
//
//	dns refused tls timeout tcp unknown          — transport, per leg one
//	http_key http_path http_busy http_5xx http_other — hub answered non-2xx
//	disabled                                     — union module switched off
//	""                                           — nothing failed (success)
//
// Expectations are written from the wire, not from the implementation: each
// case names the error a real *http.Client produces in that situation, wrapped
// the way net/http wraps it.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"syscall"
	"testing"
	"time"
)

// netTimeoutError is what the transport hands back for a socket-level read
// timeout: a net.Error whose Timeout() is true, with no context involved.
type netTimeoutError struct{}

func (netTimeoutError) Error() string   { return "read tcp: i/o timeout" }
func (netTimeoutError) Timeout() bool   { return true }
func (netTimeoutError) Temporary() bool { return true }

func TestClassifyError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil means nothing failed", nil, ""},
		{"module switched off mid-request", ErrDisabled, "disabled"},
		{
			"dns lookup, wrapped the way net/http wraps it",
			&url.Error{Op: "Post", URL: "https://hub.example/api/union/diagnose",
				Err: &net.OpError{Op: "dial", Net: "tcp",
					Err: &net.DNSError{Err: "no such host", Name: "hub.example"}}},
			"dns",
		},
		{
			"connection refused (key fine, hub port closed)",
			&url.Error{Op: "Post", URL: "https://127.0.0.1:1/diagnose",
				Err: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}},
			"refused",
		},
		{
			"deadline exceeded from the per-call context",
			&url.Error{Op: "Post", URL: "https://hub.example/api/union/diagnose",
				Err: context.DeadlineExceeded},
			"timeout",
		},
		{
			"socket read timeout: net.Error with Timeout(), no context",
			&url.Error{Op: "Post", URL: "https://hub.example/api/union/diagnose",
				Err: netTimeoutError{}},
			"timeout",
		},
		{
			"os.ErrDeadlineExceeded surfaced by the transport",
			&url.Error{Op: "Post", URL: "https://hub.example/api/union/diagnose",
				Err: fmt.Errorf("read tcp: %w", os.ErrDeadlineExceeded)},
			"timeout",
		},
		{
			// The shape net/http really produces: a deadline inside a
			// *net.OpError inside a *url.Error. Without ordering discipline
			// this one collapses into the generic TCP fallback.
			"socket deadline wrapped in OpError, as the transport does it",
			&url.Error{Op: "Post", URL: "https://hub.example/api/union/diagnose",
				Err: &net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded}},
			"timeout",
		},
		{
			"untrusted hub certificate (Go 1.20+ wrapper)",
			&url.Error{Op: "Post", URL: "https://hub.example/api/union/diagnose",
				Err: &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}},
			"tls",
		},
		{
			"untrusted hub certificate, bare x509 error (pre-1.20 shape)",
			&url.Error{Op: "Post", URL: "https://hub.example/api/union/diagnose",
				Err: x509.UnknownAuthorityError{}},
			"tls",
		},
		{
			"hostname mismatch on the hub certificate",
			&url.Error{Op: "Post", URL: "https://hub.example/api/union/diagnose",
				Err: x509.HostnameError{Host: "hub.example"}},
			"tls",
		},
		{
			"tls protocol alert",
			&url.Error{Op: "Post", URL: "https://hub.example/api/union/diagnose",
				Err: tls.AlertError(40)},
			"tls",
		},
		{
			"tcp reset, nothing more specific",
			&url.Error{Op: "Post", URL: "https://hub.example/api/union/diagnose",
				Err: &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset by peer")}},
			"tcp",
		},
		{
			"hub rejected the member key: 401",
			&APIError{Method: http.MethodPost, Path: "/diagnose", StatusCode: 401},
			"http_key",
		},
		{"hub rejected the member key: 403", &APIError{StatusCode: 403}, "http_key"},
		{"api root points at the wrong place: 404", &APIError{StatusCode: 404}, "http_path"},
		{"hub is busy: 408", &APIError{StatusCode: 408}, "http_busy"},
		{"hub rate-limits us: 429", &APIError{StatusCode: 429}, "http_busy"},
		{"hub service unavailable: 503", &APIError{StatusCode: 503}, "http_busy"},
		{"hub broke: 500", &APIError{StatusCode: 500}, "http_5xx"},
		{"hub broke: 502", &APIError{StatusCode: 502}, "http_5xx"},
		{"hub answered something else: 418", &APIError{StatusCode: 418}, "http_other"},
		{"a failure we do not model", errors.New("union: post /diagnose: banana"), "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyError(tc.err); got != tc.want {
				t.Errorf("classifyError() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDiagnoseReportsCauseOnHubHTTPErrors(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{http.StatusUnauthorized, "http_key"},
		{http.StatusForbidden, "http_key"},
		{http.StatusNotFound, "http_path"},
		{http.StatusTooManyRequests, "http_busy"},
		{http.StatusInternalServerError, "http_5xx"},
	}
	for _, tc := range cases {
		status, want := tc.status, tc.want
		t.Run(fmt.Sprintf("hub answers %d", status), func(t *testing.T) {
			srv, _ := newRecordingHub(t, func(w http.ResponseWriter, _ hubCall) {
				jsonRespond(w, status, map[string]string{"error": "denied"})
			})
			o := newTestOutbound(t, srv.URL, "key-1")
			rep, err := o.Diagnose(context.Background())
			if err != nil {
				t.Fatalf("a failed hub answer is still a result, not an error: %v", err)
			}
			if rep.Status != "error" {
				t.Fatalf("status = %q, want \"error\" for hub %d", rep.Status, status)
			}
			if rep.Cause != want {
				t.Errorf("cause = %q, want %q (hub answered %d)", rep.Cause, want, status)
			}
			// The evidence rows the legacy modal showed must survive: cause is
			// an addition, not a replacement.
			var data map[string]any
			if err := json.Unmarshal(rep.Data, &data); err != nil {
				t.Fatalf("data is not an object: %v", err)
			}
			if code, _ := data["status_code"].(float64); int(code) != status {
				t.Errorf("data.status_code = %v, want %d", data["status_code"], status)
			}
		})
	}
}

func TestDiagnoseReportsCauseOnTransportFailure(t *testing.T) {
	// A port nothing listens on. Which of refused/tcp/dns shows up is
	// platform-dependent, so the assertion is a set — but what must never
	// happen again is the legacy behaviour: a verdict with no cause at all.
	srv, _ := newRecordingHub(t, nil)
	srv.Close()
	o := newTestOutbound(t, srv.URL, "key-1")
	rep, err := o.Diagnose(context.Background())
	if err != nil {
		t.Fatalf("transport failure must still be a report: %v", err)
	}
	switch rep.Cause {
	case "refused", "tcp", "dns":
	default:
		t.Errorf("cause = %q, want one of refused|tcp|dns for a dead port", rep.Cause)
	}
	var data map[string]any
	if err := json.Unmarshal(rep.Data, &data); err != nil {
		t.Fatalf("data is not an object: %v", err)
	}
	if s, _ := data["exception"].(string); s == "" {
		t.Error("exception text dropped — operators lose the original message")
	}
}

func TestDiagnoseHasNoCauseWhenHubAnswered(t *testing.T) {
	srv, _ := newRecordingHub(t, func(w http.ResponseWriter, _ hubCall) {
		jsonRespond(w, http.StatusOK, map[string]any{"healthy": true, "delay": 0.003})
	})
	o := newTestOutbound(t, srv.URL, "key-1")
	rep, err := o.Diagnose(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != "ok" {
		t.Fatalf("status = %q, want ok", rep.Status)
	}
	if rep.Cause != "" {
		t.Errorf("cause = %q, want empty on a good leg so the UI can tell absence from a verdict", rep.Cause)
	}
}

// The wire shape itself is frozen (铁律 6). This is here because the cause work
// reaches into the transport, and a drift in the request shape would otherwise
// surface only as a hub that mysteriously stops answering.
func TestDiagnoseWireShapeUnchanged(t *testing.T) {
	srv, calls := newRecordingHub(t, func(w http.ResponseWriter, _ hubCall) {
		jsonRespond(w, http.StatusOK, map[string]any{"healthy": true})
	})
	o := newTestOutbound(t, srv.URL, "key-1")
	if _, err := o.Diagnose(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(*calls))
	}
	c := (*calls)[0]
	if c.method != http.MethodPost || c.path != "/diagnose" {
		t.Errorf("request = %s %s, want POST /diagnose", c.method, c.path)
	}
	if c.body != "{}" {
		t.Errorf("body = %q, want {}", c.body)
	}
	if c.memberKey != "key-1" {
		t.Errorf("member key header = %q, want key-1", c.memberKey)
	}
}

// The 10s hub budget is the contract the panel's own fetch is built around
// (web/src/api.js default timeoutMS = 15000). If this ever grows past that
// budget the browser aborts first and the operator gets a transport cause for
// a hub that is actually fine.
func TestDiagnoseTimeoutStaysBelowFrontendBudget(t *testing.T) {
	if diagnoseTimeout >= 15*time.Second {
		t.Errorf("diagnoseTimeout = %v, must stay under the 15s frontend fetch budget", diagnoseTimeout)
	}
}
