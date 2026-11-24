package verifyproxy

import (
	"context"
	"crypto/x509"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"skin-nexus/server/internal/metrics"
)

type proxyRoundTrip func(*http.Request) (*http.Response, error)

func (f proxyRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func proxyTestConfig() Config {
	return Config{PublicID: "srv_0123456789abcdefghijklmnopqrstuvwxyzABCD", Upstream: "https://upstream.example", Credential: testCredential,
		Listen: "127.0.0.1:25565", ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, HeaderTimeout: 5 * time.Second,
		RequestTimeout: 20 * time.Millisecond, MaxBodyBytes: 1024, MaxHeaderBytes: 16384, Retries: 2, RetryBackoff: time.Nanosecond}
}

func proxyResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestProxyRetryMatrix(t *testing.T) {
	for _, tc := range []struct {
		name       string
		steps      []func() (*http.Response, error)
		wantStatus int
		wantCalls  int
	}{
		{name: "5xx then success", steps: []func() (*http.Response, error){func() (*http.Response, error) { return proxyResponse(503, "busy"), nil }, func() (*http.Response, error) {
			return proxyResponse(200, `{"id":"0123456789abcdef0123456789abcdef","name":"Alice"}`), nil
		}}, wantStatus: 200, wantCalls: 2},
		{name: "204 never retried", steps: []func() (*http.Response, error){func() (*http.Response, error) { return proxyResponse(204, ""), nil }}, wantStatus: 204, wantCalls: 1},
		{name: "401 never retried", steps: []func() (*http.Response, error){func() (*http.Response, error) { return proxyResponse(401, testCredential), nil }}, wantStatus: 204, wantCalls: 1},
		{name: "403 never retried", steps: []func() (*http.Response, error){func() (*http.Response, error) { return proxyResponse(403, "denied"), nil }}, wantStatus: 204, wantCalls: 1},
		{name: "404 never retried", steps: []func() (*http.Response, error){func() (*http.Response, error) { return proxyResponse(404, "missing"), nil }}, wantStatus: 204, wantCalls: 1},
		{name: "429 never retried", steps: []func() (*http.Response, error){func() (*http.Response, error) { return proxyResponse(429, "quota"), nil }}, wantStatus: 204, wantCalls: 1},
		{name: "redirect never retried", steps: []func() (*http.Response, error){func() (*http.Response, error) { return proxyResponse(302, "redirect"), nil }}, wantStatus: 204, wantCalls: 1},
		{name: "TLS validation never retried", steps: []func() (*http.Response, error){func() (*http.Response, error) { return nil, x509.UnknownAuthorityError{} }}, wantStatus: 204, wantCalls: 1},
		{name: "connection reset retried", steps: []func() (*http.Response, error){func() (*http.Response, error) { return nil, syscall.ECONNRESET }, func() (*http.Response, error) {
			return proxyResponse(200, `{"id":"0123456789abcdef0123456789abcdef","name":"Alice"}`), nil
		}}, wantStatus: 200, wantCalls: 2},
		{name: "timeout retried", steps: []func() (*http.Response, error){func() (*http.Response, error) { return nil, context.DeadlineExceeded }, func() (*http.Response, error) {
			return proxyResponse(200, `{"id":"0123456789abcdef0123456789abcdef","name":"Alice"}`), nil
		}}, wantStatus: 200, wantCalls: 2},
		{name: "retry cap", steps: []func() (*http.Response, error){func() (*http.Response, error) { return proxyResponse(500, "busy"), nil }, func() (*http.Response, error) { return proxyResponse(500, "busy"), nil }, func() (*http.Response, error) { return proxyResponse(500, "busy"), nil }}, wantStatus: 204, wantCalls: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: proxyRoundTrip(func(*http.Request) (*http.Response, error) {
				step := tc.steps[calls]
				calls++
				return step()
			})}
			handler, err := NewHandler(proxyTestConfig(), client)
			if err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sessionserver/session/minecraft/hasJoined?username=Alice&serverId=server-id", nil))
			if rec.Code != tc.wantStatus || calls != tc.wantCalls {
				t.Fatalf("status=%d calls=%d, want status=%d calls=%d", rec.Code, calls, tc.wantStatus, tc.wantCalls)
			}
			if rec.Body.Len() != 0 && tc.wantStatus == http.StatusNoContent {
				t.Fatalf("denial leaked upstream body: %q", rec.Body.String())
			}
		})
	}
}

func TestProxyBoundsBodiesAndRedactsFailureLogs(t *testing.T) {
	var logs strings.Builder
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	var calls atomic.Int32
	client := &http.Client{Transport: proxyRoundTrip(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, fmt.Errorf("transport failed with %s username=Alice serverId=server-id: %w", testCredential, syscall.ECONNRESET)
	})}
	cfg := proxyTestConfig()
	cfg.MaxBodyBytes = 8
	handler, err := NewHandler(cfg, client)
	if err != nil {
		t.Fatal(err)
	}
	before := metrics.VerifyProxyUpstreamFailures.Value("network")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sessionserver/session/minecraft/hasJoined?username=Alice&serverId=server-id", strings.NewReader("123456789")))
	if rec.Code != http.StatusRequestEntityTooLarge || calls.Load() != 0 {
		t.Fatalf("oversize request = %d calls=%d", rec.Code, calls.Load())
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sessionserver/session/minecraft/hasJoined?username=Alice&serverId=server-id", nil))
	if rec.Code != http.StatusNoContent || calls.Load() != 3 {
		t.Fatalf("network failure = %d calls=%d", rec.Code, calls.Load())
	}
	if got := metrics.VerifyProxyUpstreamFailures.Value("network"); got != before+1 {
		t.Fatalf("network failure metric = %d, want %d", got, before+1)
	}
	text := logs.String()
	for _, sensitive := range []string{testCredential, "username=", "serverId=", "Alice", "transport failed", "upstream.example"} {
		if strings.Contains(text, sensitive) {
			t.Fatalf("proxy log leaked sensitive data: %s", text)
		}
	}
	if !strings.Contains(text, "verify_proxy upstream_failure") || !strings.Contains(text, "network") {
		t.Fatalf("fixed failure label missing: %s", text)
	}
}

func TestProxyRejectsOversizedUpstreamBody(t *testing.T) {
	client := &http.Client{Transport: proxyRoundTrip(func(*http.Request) (*http.Response, error) {
		return proxyResponse(200, strings.Repeat("x", maxProxyResponseBytes+1)), nil
	})}
	handler, err := NewHandler(proxyTestConfig(), client)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sessionserver/session/minecraft/hasJoined?username=Alice&serverId=server-id", nil))
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("oversized upstream = %d bytes=%d", rec.Code, rec.Body.Len())
	}
}
