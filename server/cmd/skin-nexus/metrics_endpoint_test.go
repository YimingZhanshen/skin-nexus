package main

// metrics_endpoint_test.go — the probe and the scrape surface.
//
// /metrics is off unless the operator asks for it: the endpoint publishes
// route cardinality and error counts to whoever can reach the port, so the
// default has to be "not registered" rather than "registered but empty".
// /healthz is the container probe and now also the one place a human can ask
// which build this is and how long it has been up, without a shell.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"skin-nexus/server/internal/metrics"
)

func TestMetricsSwitchIsOffByDefault(t *testing.T) {
	t.Setenv("SKIN_METRICS", "")
	if metricsEnabled() {
		t.Fatal("SKIN_METRICS unset must leave the scrape endpoint unregistered")
	}
	t.Setenv("SKIN_METRICS", "1")
	if !metricsEnabled() {
		t.Fatal("SKIN_METRICS=1 must enable the scrape endpoint")
	}
	t.Setenv("SKIN_METRICS", "true")
	if !metricsEnabled() {
		t.Fatal("SKIN_METRICS=true must enable the scrape endpoint (the repo reads booleans as true/false)")
	}
	t.Setenv("SKIN_METRICS", "nope")
	if metricsEnabled() {
		t.Fatal("an unparseable SKIN_METRICS must not expose the endpoint")
	}
}

func TestMetricsHandlerServesTheExposition(t *testing.T) {
	metrics.HTTPRequests.Inc("probe", "200")
	rec := httptest.NewRecorder()
	metricsHandler(true).ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "# TYPE http_requests_total counter") {
		t.Fatalf("no TYPE line for the request counter:\n%s", body)
	}
	if !strings.Contains(body, `http_requests_total{method="probe",status="200"}`) {
		t.Fatalf("the increment made before this scrape is missing:\n%s", body)
	}
	if !strings.Contains(body, "# TYPE process_uptime_seconds gauge") {
		t.Fatalf("uptime must be scrapeable too:\n%s", body)
	}
}

// stubHealth lets the probe be tested without a database: the shape of the
// answer is what the gate and the operator read, and the round-trip itself is
// store business.
type stubHealth struct{ err error }

func (s stubHealth) Health(context.Context) error { return s.err }

func TestHealthzReportsVersionAndUptime(t *testing.T) {
	// The process is young, so a frozen-at-zero uptime would otherwise look
	// exactly like a real one: wait out a whole second first, then the probe
	// must report at least one.
	time.Sleep(1100 * time.Millisecond)
	rec := httptest.NewRecorder()
	healthzHandler(stubHealth{}).ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("healthz is not JSON: %v (%s)", err, rec.Body.String())
	}
	if body["ok"] != true {
		t.Fatalf("ok = %v, want true", body["ok"])
	}
	if v, _ := body["version"].(string); v != version {
		t.Fatalf("version = %v, want %q — the probe is how a human asks which build is running", body["version"], version)
	}
	u, ok := body["uptime_seconds"].(float64)
	if !ok || u < 1 {
		t.Fatalf("uptime_seconds = %v, want at least 1 after waiting a second (a value pinned at 0 is not uptime)", body["uptime_seconds"])
	}
}

func TestHealthzStillFailsWhenTheStoreIsDown(t *testing.T) {
	rec := httptest.NewRecorder()
	healthzHandler(stubHealth{err: errStubUnhealthy}).ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (the container gate reads the status, not the body)", rec.Code)
	}
}

var errStubUnhealthy = errors.New("store unreachable")
