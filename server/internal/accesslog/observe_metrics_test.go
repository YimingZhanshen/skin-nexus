package accesslog

// observe_metrics_test.go — the request counter must be recorded by the same
// middleware that already watches every request, or it will drift from the
// access log the moment somebody adds a route that skips it.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"skin-nexus/server/internal/metrics"
)

func TestObserveCountsEveryRequest(t *testing.T) {
	const method, status = "GET", "418"
	before := metrics.HTTPRequests.Value(method, status)

	h := Observe(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot) // I'm a teapot: an odd code proves we read the status, not an assumed 200
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, "/probe", nil))

	if got := metrics.HTTPRequests.Value(method, status); got != before+1 {
		t.Fatalf("counter = %d, want %d — the request was served but not counted", got, before+1)
	}
	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want 418 (the wrapper must not change the response)", rec.Code)
	}
}

// A handler that never writes a header still ends the response with the
// implicit 200 (net/http semantics), and the counter must see that code too.
func TestObserveCountsImplicitOK(t *testing.T) {
	const method, status = "HEAD", "200"
	before := metrics.HTTPRequests.Value(method, status)

	h := Observe(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, "/probe", nil))

	if got := metrics.HTTPRequests.Value(method, status); got != before+1 {
		t.Fatalf("counter = %d, want %d — an unwritten status still completes as 200", got, before+1)
	}
}
