// Package verifyproxy implements the optional, database-free hasJoined proxy.
// It is never assembled by the main server and cannot enable S3/S4 policy.
package verifyproxy

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"skin-nexus/server/internal/metrics"
)

type proxy struct {
	cfg    Config
	client *http.Client
}

func NewHandler(cfg Config, client *http.Client) (http.Handler, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if client == nil {
		var err error
		client, err = NewHTTPClient(cfg)
		if err != nil {
			return nil, err
		}
	}
	p := &proxy{cfg: cfg, client: client}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "ok\n")
	})
	mux.Handle("GET /metrics", metricsHandler())
	mux.Handle("GET /sessionserver/session/minecraft/hasJoined", http.HandlerFunc(p.hasJoined))
	mux.Handle("GET /api/yggdrasil/sessionserver/session/minecraft/hasJoined", http.HandlerFunc(p.hasJoined))
	return securityHeaders(mux), nil
}

func NewServer(cfg Config, client *http.Client) (*http.Server, error) {
	handler, err := NewHandler(cfg, client)
	if err != nil {
		return nil, err
	}
	return &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		ReadHeaderTimeout: cfg.HeaderTimeout,
		MaxHeaderBytes:    cfg.MaxHeaderBytes,
	}, nil
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

func (p *proxy) hasJoined(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength > int64(p.cfg.MaxBodyBytes) {
		http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		return
	}
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, int64(p.cfg.MaxBodyBytes))
		body, err := io.ReadAll(r.Body)
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			} else {
				http.Error(w, "invalid request body", http.StatusBadRequest)
			}
			return
		}
		if len(body) != 0 {
			http.Error(w, "request body is not accepted", http.StatusBadRequest)
			return
		}
	}
	query := r.URL.Query()
	username, serverID := query.Get("username"), query.Get("serverId")
	if username == "" || serverID == "" || len(username) > 64 || len(serverID) > 128 {
		http.Error(w, "invalid hasJoined arguments", http.StatusBadRequest)
		return
	}
	forward := url.Values{}
	forward.Set("username", username)
	forward.Set("serverId", serverID)
	if ip := query.Get("ip"); ip != "" {
		forward.Set("ip", ip)
	}
	requestID := newRequestID()
	w.Header().Set("X-Request-Id", requestID)
	status, body, failure := p.forward(r.Context(), forward, requestID)
	if failure != "" && failure != "canceled" {
		slog.Error("verify_proxy upstream_failure", "reason", failure)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if status == http.StatusOK {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
		return
	}
	// Upstream policy/auth failures and healthy misses all remain the standard
	// empty hasJoined denial. They are never retried.
	w.WriteHeader(http.StatusNoContent)
}

type proxyAttempt struct {
	status int
	body   []byte
	retry  bool
	reason string
}

func (p *proxy) forward(ctx context.Context, query url.Values, requestID string) (int, []byte, string) {
	target := strings.TrimRight(p.cfg.Upstream, "/") + "/api/serveraccess/v1/servers/" + url.PathEscape(p.cfg.PublicID) + "/hasJoined?" + query.Encode()
	for attempt := 0; attempt <= p.cfg.Retries; attempt++ {
		result := p.doAttempt(ctx, target, requestID)
		if result.reason == "" {
			return result.status, result.body, ""
		}
		if result.reason == "canceled" || errors.Is(ctx.Err(), context.Canceled) {
			return 0, nil, "canceled"
		}
		if !result.retry || attempt == p.cfg.Retries || ctx.Err() != nil {
			metrics.VerifyProxyUpstreamFailures.Inc(result.reason)
			return 0, nil, result.reason
		}
		delay := p.cfg.RetryBackoff * time.Duration(1<<attempt)
		if delay > 2*time.Second {
			delay = 2 * time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return 0, nil, "canceled"
		case <-timer.C:
		}
	}
	return 0, nil, "request_failed"
}

func (p *proxy) doAttempt(parent context.Context, target, requestID string) proxyAttempt {
	ctx, cancel := context.WithTimeout(parent, p.cfg.RequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return proxyAttempt{reason: "invalid_request"}
	}
	req.Header.Set("Authorization", "Bearer "+p.cfg.Credential)
	req.Header.Set("X-Request-Id", requestID)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "SkinNexus verify-proxy")
	resp, err := p.client.Do(req)
	if err != nil {
		reason, retry := classifyProxyError(err)
		return proxyAttempt{retry: retry, reason: reason}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxProxyResponseBytes))
		return proxyAttempt{status: resp.StatusCode, retry: true, reason: "upstream_status"}
	}
	if resp.StatusCode == http.StatusNoContent {
		return proxyAttempt{status: resp.StatusCode}
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxProxyResponseBytes))
		return proxyAttempt{status: resp.StatusCode, reason: "upstream_status"}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxProxyResponseBytes+1))
	if len(body) > maxProxyResponseBytes {
		return proxyAttempt{reason: "response_too_large"}
	}
	if err != nil {
		reason, retry := classifyProxyError(err)
		return proxyAttempt{retry: retry, reason: reason}
	}
	return proxyAttempt{status: resp.StatusCode, body: body}
}

func classifyProxyError(err error) (string, bool) {
	if errors.Is(err, context.Canceled) {
		return "canceled", false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout", true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout", true
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNABORTED) || errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, syscall.ENETUNREACH) ||
		errors.Is(err, io.ErrUnexpectedEOF) {
		return "network", true
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.Temporary() {
		return "network", true
	}
	return "network", false
}

func newRequestID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "unavailable"
	}
	return base64.RawURLEncoding.EncodeToString(buf[:])
}

func metricsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_ = metrics.Default.WriteText(w)
	})
}
