// Package outbound is the host's minimal market download client. It only
// performs GET, refuses non-public destinations, never follows redirects, and
// caps the response. It is deliberately not exposed to plugin JavaScript.
package outbound

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	AllowedHosts []string
	Timeout      time.Duration
	MaxBytes     int64
	HTTP         *http.Client
}

func New(allowed []string, timeout time.Duration, maxBytes int64) *Client {
	if timeout <= 0 {
		timeout = 4 * time.Second
	}
	if maxBytes <= 0 {
		maxBytes = 1 << 20
	}
	c := &Client{AllowedHosts: allowed, Timeout: timeout, MaxBytes: maxBytes}
	c.HTTP = &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           publicDialContext,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: timeout,
			DisableKeepAlives:     true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("outbound: redirects are not allowed")
		},
	}
	return c
}

// Get fetches an allowlisted URL and returns at most MaxBytes.
func (c *Client) Get(ctx context.Context, raw string) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, fmt.Errorf("outbound: scheme %q not allowed", u.Scheme)
	}
	if u.User != nil {
		return nil, errors.New("outbound: userinfo not allowed")
	}
	if u.Port() != "" && u.Port() != "80" && u.Port() != "443" {
		return nil, errors.New("outbound: non-standard port not allowed")
	}
	if !hostAllowed(u.Hostname(), c.AllowedHosts) {
		return nil, fmt.Errorf("outbound: host %q not allowed", u.Hostname())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("outbound: status %d", resp.StatusCode)
	}
	if resp.ContentLength > c.MaxBytes {
		return nil, fmt.Errorf("outbound: response exceeds %d bytes", c.MaxBytes)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, c.MaxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > c.MaxBytes {
		return nil, fmt.Errorf("outbound: response exceeds %d bytes", c.MaxBytes)
	}
	return body, nil
}

func hostAllowed(host string, allow []string) bool {
	host = strings.ToLower(host)
	for _, raw := range allow {
		raw = strings.ToLower(strings.TrimSpace(raw))
		if raw == "" {
			continue
		}
		if strings.HasPrefix(raw, "*.") {
			suffix := raw[1:] // ".example.com"
			if strings.HasSuffix(host, suffix) && len(host) > len(suffix) {
				return true
			}
			continue
		}
		if host == raw {
			return true
		}
	}
	return false
}

// publicDialContext resolves and dials only globally routable addresses,
// mirroring the ygg fallback guard without touching that frozen package.
func publicDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	var dialer net.Dialer
	var lastErr error
	for _, ipa := range ips {
		if !isPublicIP(ipa.IP) {
			lastErr = fmt.Errorf("outbound: non-public address %s refused for %s", ipa.IP, host)
			continue
		}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ipa.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("outbound: no public address for %s", host)
	}
	return nil, lastErr
}

// sixToFour and teredo are the two transition ranges that can carry a private
// destination through a "global" address: 2002:10.0.0.1:: embeds 10.0.0.1, and
// Teredo encodes the peer inside the address. Both only deliver if the host has
// tunnelling enabled, which is exactly the assumption an SSRF check must not
// make about somebody else's machine.
var (
	sixToFour = &net.IPNet{IP: net.ParseIP("2002::"), Mask: net.CIDRMask(16, 128)}
	teredo    = &net.IPNet{IP: net.ParseIP("2001::"), Mask: net.CIDRMask(32, 128)}
)

func isPublicIP(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsPrivate() {
		return false
	}
	if sixToFour.Contains(ip) || teredo.Contains(ip) {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil {
		if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
			return false
		}
	}
	return ip.IsGlobalUnicast()
}
