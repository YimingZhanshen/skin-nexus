package outbound

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Policy bounds one plugin's outbound HTTP surface.
type Policy struct {
	Hosts          []string
	AllowUserHosts bool
	UserHostPaths  []string
	MaxBytes       int64
	Timeout        time.Duration
	// AllowLoopback is test-only. Production hosts must never set it.
	AllowLoopback bool
}

// FetchResult carries a response even for non-2xx statuses so plugins can
// distinguish 404/429 from transport failures.
type FetchResult struct {
	Status      int
	ContentType string
	Body        []byte
	RetryAfter  string
}

// PolicyError is a stable, transport-neutral outbound failure.
type PolicyError struct {
	Code    string
	Message string
}

func (e *PolicyError) Error() string { return e.Code + ": " + e.Message }

// Scoped is a per-plugin outbound GET client. It never follows redirects,
// never sends cookies or Authorization, and only connects to validated
// public addresses (unless AllowLoopback is set in tests).
type Scoped struct {
	policy Policy
	http   *http.Client

	mu   sync.Mutex
	hits map[string][]time.Time
}

func NewScoped(p Policy) *Scoped {
	if p.MaxBytes <= 0 {
		p.MaxBytes = 1 << 20
	}
	if p.MaxBytes > 4<<20 {
		p.MaxBytes = 4 << 20
	}
	if p.Timeout <= 0 {
		p.Timeout = 5 * time.Second
	}
	s := &Scoped{policy: p, hits: map[string][]time.Time{}}
	s.http = &http.Client{
		Timeout: p.Timeout,
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           s.dialContext,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: p.Timeout,
			DisableKeepAlives:     true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return &PolicyError{Code: "net.blocked_redirect", Message: "redirects are not allowed"}
		},
	}
	return s
}

func (s *Scoped) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if s.policy.AllowLoopback {
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}
	return publicDialContext(ctx, network, addr)
}

// Fetch performs a GET after validating host/path policy and rate limits.
func (s *Scoped) Fetch(ctx context.Context, raw string) (FetchResult, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return FetchResult{}, &PolicyError{Code: "net.invalid_url", Message: "invalid URL"}
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return FetchResult{}, &PolicyError{Code: "net.invalid_url", Message: "only http(s) is allowed"}
	}
	if u.User != nil {
		return FetchResult{}, &PolicyError{Code: "net.invalid_url", Message: "userinfo is not allowed"}
	}
	if u.Port() != "" && u.Port() != "80" && u.Port() != "443" && !s.policy.AllowLoopback {
		return FetchResult{}, &PolicyError{Code: "net.invalid_url", Message: "only ports 80/443 are allowed"}
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || strings.HasSuffix(host, ".") || strings.HasSuffix(host, ".local") {
		return FetchResult{}, &PolicyError{Code: "net.blocked_host", Message: "host is not allowed"}
	}
	if net.ParseIP(host) != nil && !s.policy.AllowLoopback {
		return FetchResult{}, &PolicyError{Code: "net.blocked_host", Message: "IP literals are not allowed"}
	}
	staticOK := hostAllowed(host, s.policy.Hosts)
	userOK := s.policy.AllowUserHosts && pathAllowed(u.Path, s.policy.UserHostPaths)
	if !staticOK && !userOK {
		return FetchResult{}, &PolicyError{Code: "net.blocked_host", Message: "host or path is not allowed"}
	}
	if !s.allow(host) {
		return FetchResult{}, &PolicyError{Code: "net.rate_limited", Message: "outbound rate limit exceeded"}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return FetchResult{}, &PolicyError{Code: "net.invalid_url", Message: err.Error()}
	}
	req.Header.Set("User-Agent", "SkinNexus-Plugin/1.0")
	req.Header.Set("Accept", "*/*")
	resp, err := s.http.Do(req)
	if err != nil {
		var pe *PolicyError
		if errors.As(err, &pe) {
			return FetchResult{}, pe
		}
		code := "net.unavailable"
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			code = "net.timeout"
		}
		return FetchResult{}, &PolicyError{Code: code, Message: err.Error()}
	}
	defer resp.Body.Close()
	if resp.ContentLength > s.policy.MaxBytes {
		return FetchResult{}, &PolicyError{Code: "net.too_large", Message: "response exceeds size limit"}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, s.policy.MaxBytes+1))
	if err != nil {
		return FetchResult{}, &PolicyError{Code: "net.unavailable", Message: err.Error()}
	}
	if int64(len(body)) > s.policy.MaxBytes {
		return FetchResult{}, &PolicyError{Code: "net.too_large", Message: "response exceeds size limit"}
	}
	return FetchResult{
		Status: resp.StatusCode, ContentType: resp.Header.Get("Content-Type"),
		Body: body, RetryAfter: resp.Header.Get("Retry-After"),
	}, nil
}

// allow enforces 2 requests/second with a burst of 4 per host.
func (s *Scoped) allow(host string) bool {
	now := time.Now()
	cut := now.Add(-time.Second)
	s.mu.Lock()
	defer s.mu.Unlock()
	hits := s.hits[host]
	keep := hits[:0]
	for _, h := range hits {
		if h.After(cut) {
			keep = append(keep, h)
		}
	}
	if len(keep) >= 4 {
		s.hits[host] = keep
		return false
	}
	s.hits[host] = append(keep, now)
	return true
}

func pathAllowed(p string, prefixes []string) bool {
	if p == "" {
		p = "/"
	}
	for _, prefix := range prefixes {
		if prefix != "" && strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}
