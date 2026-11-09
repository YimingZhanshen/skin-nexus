package union

// Leg-one self-observation for the diagnose report (2026-11-10, slice D2).
//
// The hub's diagnose payload describes the hub's own probe of US (its GnuTLS
// trace, the status it got back from our inbound endpoint). Nothing in it
// describes the connection WE made, so today the modal can only report a
// verdict for leg one and then paste the hub's diary. The probe adds the two
// things only this process can know about its own outbound call:
//
//   - what TLS we actually negotiated with the hub, and when its certificate
//     dies — an expired or expiring hub cert is a real, dated failure that
//     currently surfaces as an unreadable handshake dump;
//   - how far the hub's clock is from ours, read off its Date header, judged
//     against the inbound signature window this site enforces. A hub more
//     than 30s ahead or 10s behind gets its callbacks rejected by us, and
//     nothing in the panel says so today.
//
// Expectations come from the test server's own certificate and from the
// Date header the test writes, not from the code under test.

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func healthyHub(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"healthy": true, "delay": 0.003})
}

// newTLSHub runs a real TLS fake hub so resp.TLS is a genuine connection
// state rather than a fixture the code could not have produced.
func newTLSHub(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func diagnoseWith(t *testing.T, srv *httptest.Server) *DiagnoseReport {
	t.Helper()
	o, err := NewOutbound(Config{Enabled: true, ApiRoot: srv.URL, MemberKey: "key-1"}, srv.Client())
	if err != nil {
		t.Fatalf("NewOutbound: %v", err)
	}
	rep, err := o.Diagnose(context.Background())
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	return rep
}

// pinnedTLSClient speaks TLS 1.2 only. The pin goes on the client because
// httptest builds its tls.Config inside StartTLS, so writing to srv.TLS
// afterwards changes nothing. The version is fully determined by the client
// side; the suite is not (it is the server's pick from what we offer), so the
// assertion on it is about shape — a name, not a raw code point — and the
// exact suite is left to the pair that actually negotiated it.
func pinnedTLSClient(t *testing.T, srv *httptest.Server) *http.Client {
	t.Helper()
	tr, ok := srv.Client().Transport.(*http.Transport)
	if !ok {
		t.Fatalf("httptest client transport is %T, want *http.Transport", srv.Client().Transport)
	}
	ct := tr.Clone()
	if ct.TLSClientConfig == nil {
		ct.TLSClientConfig = &tls.Config{}
	}
	ct.TLSClientConfig.MinVersion = tls.VersionTLS12
	ct.TLSClientConfig.MaxVersion = tls.VersionTLS12
	cli := &http.Client{Transport: ct}
	t.Cleanup(cli.CloseIdleConnections)
	return cli
}

func TestDiagnoseProbeReportsOurOwnTLSNegotiation(t *testing.T) {
	srv := newTLSHub(t, healthyHub)
	o, err := NewOutbound(Config{Enabled: true, ApiRoot: srv.URL, MemberKey: "key-1"}, pinnedTLSClient(t, srv))
	if err != nil {
		t.Fatalf("NewOutbound: %v", err)
	}
	rep, err := o.Diagnose(context.Background())
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if rep.Probe == nil || rep.Probe.TLS == nil {
		t.Fatalf("probe.tls missing on a TLS leg: %+v", rep.Probe)
	}
	got := rep.Probe.TLS
	if got.Version != "TLS 1.2" {
		t.Errorf("tls.version = %q, want TLS 1.2 (the client was pinned to it)", got.Version)
	}
	if got.Cipher == "" || !strings.HasPrefix(got.Cipher, "TLS_") {
		t.Errorf("tls.cipher = %q, want a suite name such as TLS_ECDHE_..., not a raw code point", got.Cipher)
	}
	leaf := srv.Certificate()
	if got.CertSubject != leaf.Subject.CommonName {
		t.Errorf("tls.cert_subject = %q, want the served leaf CN %q", got.CertSubject, leaf.Subject.CommonName)
	}
	if got.CertIssuer != leaf.Issuer.CommonName {
		t.Errorf("tls.cert_issuer = %q, want the served leaf issuer %q", got.CertIssuer, leaf.Issuer.CommonName)
	}
	// Whole-day truncation on both sides can straddle a boundary by one; the
	// certificate itself is the source of truth.
	wantDays := int(time.Until(leaf.NotAfter).Hours() / 24)
	if d := got.CertDaysLeft - wantDays; d > 1 || d < -1 {
		t.Errorf("tls.cert_days_left = %d, want %d (±1) from the served certificate", got.CertDaysLeft, wantDays)
	}
	if got.CertNotAfter == "" {
		t.Error("tls.cert_not_after empty — operators need the date, not only a countdown")
	}
	if _, err := time.Parse(time.RFC3339, got.CertNotAfter); err != nil {
		t.Errorf("tls.cert_not_after = %q, not RFC3339: %v", got.CertNotAfter, err)
	}
	if want := leaf.NotAfter.UTC().Format(time.RFC3339); got.CertNotAfter != want {
		t.Errorf("tls.cert_not_after = %q, want %q", got.CertNotAfter, want)
	}
}

func TestDiagnoseProbeOmitsTLSForPlainHTTPHub(t *testing.T) {
	// A http:// hub has no TLS state. Reporting an empty snapshot there would
	// read as "the certificate is broken" instead of "there is no certificate".
	srv := httptest.NewServer(http.HandlerFunc(healthyHub))
	t.Cleanup(srv.Close)
	rep := diagnoseWith(t, srv)
	if rep.Probe != nil && rep.Probe.TLS != nil {
		t.Errorf("probe.tls = %+v on a plain HTTP leg, want absent", rep.Probe.TLS)
	}
}

func TestDiagnoseProbeClockOffsetAgainstSignatureWindow(t *testing.T) {
	// The window this site enforces on hub-signed requests (config.go).
	// offset = hub clock - our clock.
	cases := []struct {
		name       string
		drift      time.Duration
		wantWithin bool
	}{
		{"hub in step", 0, true},
		{"hub 20s ahead, still inside the +30s future allowance", 20 * time.Second, true},
		{"hub 5s behind, still inside the -10s past allowance", -5 * time.Second, true},
		{"hub 120s ahead: its signed requests land in our far future and get rejected", 120 * time.Second, false},
		{"hub 60s behind: its signed requests look expired to us", -60 * time.Second, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			drift := tc.drift
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// net/http only fills Date when the handler has not set it.
				w.Header().Set("Date", time.Now().Add(drift).UTC().Format(http.TimeFormat))
				healthyHub(w, r)
			}))
			t.Cleanup(srv.Close)
			rep := diagnoseWith(t, srv)
			if rep.Probe == nil || rep.Probe.Clock == nil {
				t.Fatalf("probe.clock missing: %+v", rep.Probe)
			}
			c := rep.Probe.Clock
			if c.WithinWindow != tc.wantWithin {
				t.Errorf("within_window = %v, want %v for offset %v (got %vs)",
					c.WithinWindow, tc.wantWithin, drift, c.OffsetSeconds)
			}
			// Tolerance covers test latency, not the drift itself.
			if d := c.OffsetSeconds - drift.Seconds(); d > 3 || d < -3 {
				t.Errorf("offset_seconds = %.1f, want %.1f (±3s)", c.OffsetSeconds, drift.Seconds())
			}
			if c.HubClock == "" {
				t.Error("hub_clock missing — the raw header is the evidence behind the verdict")
			}
		})
	}
}

// A hub behind a proxy that mangles Date must not produce a zero offset —
// that would claim the two clocks agree. Note the header cannot simply be
// deleted: net/http re-adds a correct Date at write time when the handler
// leaves it absent, so the realistic broken case is a value that will not
// parse.
func TestDiagnoseProbeClockAbsentWhenHubDateIsUnusable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Date", "the day the hub came back")
		healthyHub(w, r)
	}))
	t.Cleanup(srv.Close)
	rep := diagnoseWith(t, srv)
	if rep.Probe != nil && rep.Probe.Clock != nil {
		t.Errorf("probe.clock = %+v with an unparseable Date, want absent", rep.Probe.Clock)
	}
}

// The ten-second diagnose budget is only real if nothing clamps it. Diagnose
// used to wrap the context in 10s and then call a transport that wrapped it
// again in the 5s default — the inner deadline always won, so a hub taking
// six seconds to answer its own reverse probe was reported to the operator as
// a transport failure on a hub that was merely slow. RequestTimeout is set
// absurdly small here so the two budgets are distinguishable in milliseconds.
func TestDiagnoseKeepsItsOwnLongerBudget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(600 * time.Millisecond)
		healthyHub(w, r)
	}))
	t.Cleanup(srv.Close)

	o, err := NewOutbound(Config{
		Enabled: true, ApiRoot: srv.URL, MemberKey: "key-1",
		RequestTimeout: 200 * time.Millisecond,
	}, srv.Client())
	if err != nil {
		t.Fatalf("NewOutbound: %v", err)
	}
	ctx := context.Background()

	// Control: the ordinary 5s-class budget really is 200ms here.
	if _, err := o.ServerList(ctx); err == nil {
		t.Fatal("ServerList should have hit the 200ms request timeout — the fixture proves nothing without this")
	}

	rep, err := o.Diagnose(ctx)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if rep.Status != "ok" {
		t.Errorf("status = %q cause = %q, want ok on a hub that answered in 600ms", rep.Status, rep.Cause)
	}
}

// A hub that answers 403 still completed the handshake: the TLS snapshot is
// the evidence that the problem is the member key and not the transport, so
// the probe must survive a failed application layer.
func TestDiagnoseProbePresentWhenHubAnswers403OverTLS(t *testing.T) {
	srv := newTLSHub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"bad member key"}`))
	})
	rep := diagnoseWith(t, srv)
	if rep.Status != "error" || rep.Cause != "http_key" {
		t.Fatalf("status/cause = %s/%s, want error/http_key", rep.Status, rep.Cause)
	}
	if rep.Probe == nil || rep.Probe.TLS == nil {
		t.Fatalf("probe.tls missing although the handshake succeeded: %+v", rep.Probe)
	}
	if rep.Probe.TLS.CertDaysLeft == 0 && rep.Probe.TLS.CertNotAfter == "" {
		t.Error("empty certificate evidence on a rejected-key leg")
	}
}

// No connection, nothing observed. The cause token carries this case.
func TestDiagnoseProbeAbsentWhenNoConnectionWasMade(t *testing.T) {
	srv, _ := newRecordingHub(t, nil)
	srv.Close()
	o := newTestOutbound(t, srv.URL, "key-1")
	rep, err := o.Diagnose(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Probe != nil {
		t.Errorf("probe = %+v with no connection at all, want nil", rep.Probe)
	}
}
