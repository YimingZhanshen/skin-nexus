package serveraccess

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"skin-nexus/server/internal/metrics"
	"skin-nexus/server/internal/store"
	"skin-nexus/server/internal/ygg"
)

func TestServerAccessFallbackCircuitOpensHalfOpenRecoversAndLocalHitBypasses(t *testing.T) {
	f := newVerifyFixture(t)
	f.credential(t, "url_secret")
	serverFallback := true
	limit := 8
	if err := f.st.UpdateSAServerOwnerPolicy(context.Background(), f.tenant, f.server, nil, &serverFallback); err != nil {
		t.Fatal(err)
	}
	if err := f.st.UpdateSAServerPlatformPolicy(context.Background(), f.tenant, f.server, nil, nil, &limit, nil, nil); err != nil {
		t.Fatal(err)
	}
	status := http.StatusServiceUnavailable
	calls := 0
	f.api.SetFallback(&ygg.Fallback{BaseURL: "https://official.example", Enabled: func(context.Context) bool { return true }, Client: &http.Client{Transport: verifyFallback(func(*http.Request) (*http.Response, error) {
		calls++
		return breakerResponse(status), nil
	})}})
	before := metrics.ServerAccessFallbackBreakerOpen.Value()
	for i := 0; i < 5; i++ {
		if rec := f.verify(t, "url_secret", "Unknown", "server-id", ""); rec.Code != http.StatusNoContent {
			t.Fatalf("upstream fault %d = %d %s", i, rec.Code, rec.Body.String())
		}
	}
	if calls != 5 {
		t.Fatalf("upstream calls before open = %d, want 5", calls)
	}
	if rec := f.verify(t, "url_secret", "Unknown", "server-id", ""); rec.Code != http.StatusNoContent || calls != 5 {
		t.Fatalf("open circuit = %d calls=%d", rec.Code, calls)
	}
	if got := metrics.ServerAccessFallbackBreakerOpen.Value(); got != before+1 {
		t.Fatalf("breaker metric = %d, want %d", got, before+1)
	}
	events, err := f.st.ListSAJoinEvents(context.Background(), f.tenant, f.server, f.now.Add(-time.Minute), f.now.Add(time.Minute), 20)
	if err != nil {
		t.Fatal(err)
	}
	reasons := map[string]bool{}
	for _, event := range events {
		reasons[event.ReasonCode] = true
	}
	if !reasons["fallback_unavailable"] || !reasons["fallback_circuit_open"] {
		t.Fatalf("breaker event reasons = %+v", events)
	}

	// Emergency policy and local sessions do not depend on the official provider.
	f.playerAndJoin(t)
	if rec := f.verify(t, "url_secret", "Alice", "server-id", "203.0.113.8"); rec.Code != http.StatusOK || calls != 5 {
		t.Fatalf("local hit while circuit open = %d calls=%d", rec.Code, calls)
	}

	f.now = f.now.Add(31 * time.Second)
	status = http.StatusOK
	if rec := f.verify(t, "url_secret", "Unknown", "server-id", ""); rec.Code != http.StatusOK || calls != 6 {
		t.Fatalf("half-open recovery = %d calls=%d body=%s", rec.Code, calls, rec.Body.String())
	}
	if rec := f.verify(t, "url_secret", "Unknown", "server-id", ""); rec.Code != http.StatusOK || calls != 7 {
		t.Fatalf("closed circuit after recovery = %d calls=%d", rec.Code, calls)
	}
}

func TestServerAccessFallbackCircuitIsPerServerAndHealthyMissDoesNotOpen(t *testing.T) {
	f := newVerifyFixture(t)
	f.credential(t, "url_secret")
	on := true
	if err := f.st.UpdateSAServerOwnerPolicy(context.Background(), f.tenant, f.server, nil, &on); err != nil {
		t.Fatal(err)
	}
	status := http.StatusServiceUnavailable
	calls := 0
	f.api.SetFallback(&ygg.Fallback{BaseURL: "https://official.example", Enabled: func(context.Context) bool { return true }, Client: &http.Client{Transport: verifyFallback(func(*http.Request) (*http.Response, error) {
		calls++
		return breakerResponse(status), nil
	})}})
	for i := 0; i < 5; i++ {
		f.verify(t, "url_secret", "Unknown", "server-id", "")
	}
	other, err := f.st.CreateSAServer(context.Background(), f.tenant, "srv_other", "other")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.UpdateSAServerOwnerPolicy(context.Background(), f.tenant, other.ID, nil, &on); err != nil {
		t.Fatal(err)
	}
	originalSecret := f.secret
	f.secret = "other-server-only-secret-0123456789abcdef"
	if _, err := f.st.CreateSAServerCredential(context.Background(), f.tenant, other.ID, store.SACredential{
		Mode: "header", SecretHash: saHash(f.secret), Scopes: `["verify:join"]`, Status: "active",
	}); err != nil {
		t.Fatal(err)
	}
	status = http.StatusOK
	req := httptest.NewRequest(http.MethodGet, "/api/serveraccess/v1/servers/srv_other/hasJoined?username=Unknown&serverId=server-id", nil)
	req.Header.Set("Authorization", "Bearer "+f.secret)
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || calls != 6 {
		t.Fatalf("isolated server fallback = %d calls=%d", rec.Code, calls)
	}
	f.secret = originalSecret
	if rec := f.verify(t, "url_secret", "Unknown", "server-id", ""); rec.Code != http.StatusNoContent || calls != 6 {
		t.Fatalf("original open circuit = %d calls=%d", rec.Code, calls)
	}

	// A normal official 204 is a healthy miss, not five infrastructure faults.
	healthy := newVerifyFixture(t)
	healthy.credential(t, "url_secret")
	if err := healthy.st.UpdateSAServerOwnerPolicy(context.Background(), healthy.tenant, healthy.server, nil, &on); err != nil {
		t.Fatal(err)
	}
	healthyCalls := 0
	healthy.api.SetFallback(&ygg.Fallback{BaseURL: "https://official.example", Enabled: func(context.Context) bool { return true }, Client: &http.Client{Transport: verifyFallback(func(*http.Request) (*http.Response, error) {
		healthyCalls++
		return breakerResponse(http.StatusNoContent), nil
	})}})
	before := metrics.ServerAccessFallbackBreakerOpen.Value()
	for i := 0; i < 6; i++ {
		if rec := healthy.verify(t, "url_secret", "Unknown", "server-id", ""); rec.Code != http.StatusNoContent {
			t.Fatalf("healthy miss %d = %d", i, rec.Code)
		}
	}
	if healthyCalls != 6 || metrics.ServerAccessFallbackBreakerOpen.Value() != before {
		t.Fatalf("healthy misses opened circuit: calls=%d", healthyCalls)
	}
}

func TestServerAccessFallbackHalfOpenAllowsOnlyOneProbe(t *testing.T) {
	f := newVerifyFixture(t)
	f.credential(t, "url_secret")
	on := true
	limit := 4
	if err := f.st.UpdateSAServerOwnerPolicy(context.Background(), f.tenant, f.server, nil, &on); err != nil {
		t.Fatal(err)
	}
	if err := f.st.UpdateSAServerPlatformPolicy(context.Background(), f.tenant, f.server, nil, nil, &limit, nil, nil); err != nil {
		t.Fatal(err)
	}
	status := http.StatusBadGateway
	calls := 0
	f.api.SetFallback(&ygg.Fallback{BaseURL: "https://official.example", Enabled: func(context.Context) bool { return true }, Client: &http.Client{Transport: verifyFallback(func(*http.Request) (*http.Response, error) {
		calls++
		return breakerResponse(status), nil
	})}})
	for i := 0; i < 5; i++ {
		f.verify(t, "url_secret", "Unknown", "server-id", "")
	}
	f.now = f.now.Add(31 * time.Second)
	started := make(chan struct{})
	var startedOnce sync.Once
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseProbe := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseProbe()
	status = http.StatusOK
	f.api.SetFallback(&ygg.Fallback{BaseURL: "https://official.example", Enabled: func(context.Context) bool { return true }, Client: &http.Client{Transport: verifyFallback(func(*http.Request) (*http.Response, error) {
		calls++
		startedOnce.Do(func() { close(started) })
		<-release
		return breakerResponse(status), nil
	})}})
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- f.verify(t, "url_secret", "Unknown", "server-id", "") }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("half-open probe did not reach upstream")
	}
	secondDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { secondDone <- f.verify(t, "url_secret", "Unknown", "server-id", "") }()
	select {
	case rec := <-secondDone:
		if rec.Code != http.StatusNoContent || calls != 6 {
			t.Fatalf("second half-open request = %d calls=%d", rec.Code, calls)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("second half-open request was not rejected promptly")
	}
	releaseProbe()
	if rec := <-done; rec.Code != http.StatusOK {
		t.Fatalf("half-open probe = %d %s", rec.Code, rec.Body.String())
	}
}

func TestServerAccessFallbackCancellationDoesNotOpenCircuit(t *testing.T) {
	f := newVerifyFixture(t)
	f.credential(t, "url_secret")
	on := true
	if err := f.st.UpdateSAServerOwnerPolicy(context.Background(), f.tenant, f.server, nil, &on); err != nil {
		t.Fatal(err)
	}
	calls := 0
	f.api.SetFallback(&ygg.Fallback{BaseURL: "https://official.example", Enabled: func(context.Context) bool { return true }, Client: &http.Client{Transport: verifyFallback(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, fmt.Errorf("local request canceled: %w", context.Canceled)
	})}})
	before := metrics.ServerAccessFallbackBreakerOpen.Value()
	for i := 0; i < fallbackFailureThreshold+1; i++ {
		if rec := f.verify(t, "url_secret", "Unknown", "server-id", ""); rec.Code != http.StatusNoContent {
			t.Fatalf("canceled fallback %d = %d %s", i, rec.Code, rec.Body.String())
		}
	}
	if calls != fallbackFailureThreshold+1 {
		t.Fatalf("canceled upstream calls = %d, want %d", calls, fallbackFailureThreshold+1)
	}
	if got := metrics.ServerAccessFallbackBreakerOpen.Value(); got != before {
		t.Fatalf("local cancellations opened circuit: metric = %d, want %d", got, before)
	}
	events, err := f.st.ListSAJoinEvents(context.Background(), f.tenant, f.server, f.now.Add(-time.Minute), f.now.Add(time.Minute), 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.ReasonCode == "fallback_unavailable" {
			t.Fatalf("local cancellation recorded as upstream failure: %+v", event)
		}
	}

	f.api.SetFallback(&ygg.Fallback{BaseURL: "https://official.example", Enabled: func(context.Context) bool { return true }, Client: &http.Client{Transport: verifyFallback(func(*http.Request) (*http.Response, error) {
		calls++
		return breakerResponse(http.StatusServiceUnavailable), nil
	})}})
	for i := 0; i < fallbackFailureThreshold; i++ {
		f.verify(t, "url_secret", "Unknown", "server-id", "")
	}
	if got := metrics.ServerAccessFallbackBreakerOpen.Value(); got != before+1 {
		t.Fatalf("real upstream faults did not open circuit: metric = %d, want %d", got, before+1)
	}
}

func TestServerAccessFallbackBodyCancellationDoesNotCountAsFailure(t *testing.T) {
	f := newVerifyFixture(t)
	f.credential(t, "url_secret")
	on := true
	if err := f.st.UpdateSAServerOwnerPolicy(context.Background(), f.tenant, f.server, nil, &on); err != nil {
		t.Fatal(err)
	}
	calls := 0
	f.api.SetFallback(&ygg.Fallback{BaseURL: "https://official.example", Enabled: func(context.Context) bool { return true }, Client: &http.Client{Transport: verifyFallback(func(*http.Request) (*http.Response, error) {
		calls++
		response := breakerResponse(http.StatusOK)
		response.Body = io.NopCloser(verifyFallbackErrorReader{err: fmt.Errorf("response body read canceled: %w", context.Canceled)})
		return response, nil
	})}})
	before := metrics.ServerAccessFallbackBreakerOpen.Value()
	for i := 0; i < fallbackFailureThreshold+1; i++ {
		if rec := f.verify(t, "url_secret", "Unknown", "server-id", ""); rec.Code != http.StatusNoContent {
			t.Fatalf("body-canceled fallback %d = %d %s", i, rec.Code, rec.Body.String())
		}
	}
	if calls != fallbackFailureThreshold+1 {
		t.Fatalf("body-canceled upstream calls = %d, want %d", calls, fallbackFailureThreshold+1)
	}
	if got := metrics.ServerAccessFallbackBreakerOpen.Value(); got != before {
		t.Fatalf("body cancellations opened circuit: metric = %d, want %d", got, before)
	}
	events, err := f.st.ListSAJoinEvents(context.Background(), f.tenant, f.server, f.now.Add(-time.Minute), f.now.Add(time.Minute), 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.ReasonCode == "fallback_unavailable" || event.ReasonCode == "fallback_circuit_open" {
			t.Fatalf("body cancellation recorded as upstream failure: %+v", event)
		}
	}

	f.api.SetFallback(&ygg.Fallback{BaseURL: "https://official.example", Enabled: func(context.Context) bool { return true }, Client: &http.Client{Transport: verifyFallback(func(*http.Request) (*http.Response, error) {
		calls++
		return breakerResponse(http.StatusServiceUnavailable), nil
	})}})
	for i := 0; i < fallbackFailureThreshold-1; i++ {
		f.verify(t, "url_secret", "Unknown", "server-id", "")
	}
	if got := metrics.ServerAccessFallbackBreakerOpen.Value(); got != before {
		t.Fatalf("body cancellations counted toward circuit threshold: metric = %d, want %d", got, before)
	}
	f.verify(t, "url_secret", "Unknown", "server-id", "")
	if got := metrics.ServerAccessFallbackBreakerOpen.Value(); got != before+1 {
		t.Fatalf("real upstream faults did not open circuit: metric = %d, want %d", got, before+1)
	}
}

type verifyFallbackErrorReader struct {
	err error
}

func (r verifyFallbackErrorReader) Read([]byte) (int, error) { return 0, r.err }

func breakerResponse(status int) *http.Response {
	body := `{"id":"0123456789abcdef0123456789abcdef","name":"Unknown"}`
	if status != http.StatusOK {
		body = "upstream unavailable"
	}
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
