package ygg

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Mojang official sessionserver fallback (A2, 2026-10-26).
//
// Scope (competitor-analysis A2 最小版): profile/{uuid} and hasJoined that
// MISS locally are offered to Mojang's official sessionserver before the
// 204 goes out, so servers pointing their authlib-injector at this site do
// not kick players who authenticate with official accounts. Local hits are
// untouched — the wire only extends the previously-empty case (opt-out via
// the `ygg_mojang_fallback` panel key).
//
// Threat model (element-skin 逃逸路径教训 + 本站证据铁律):
//   - the target origin is a compile-time constant; user input only reaches
//     the path (strictly validated 32-hex UUID — NormalizeUUID alone only
//     lowercases/strips hyphens, it does NOT prove hex) or the query
//     (url.Values-escaped, username further restricted to the official
//     name charset so CJK site-local names never leave the box);
//   - redirects are NOT followed (ErrUseLastResponse) — a hijacked edge
//     cannot bounce the fetch inward;
//   - the dialer refuses non-public addresses (loopback/RFC1918/link-local/
//     CGNAT/unspecified): even a poisoned resolver cannot point the socket
//     at the inside of the host or the school LAN;
//   - responses are capped at 1 MiB and must parse as a profile JSON
//     (captive-portal HTML dies here), under a hard 4s deadline.
//
// Every failure collapses to "no fallback" — an empty 204 beats wrong data.

const (
	mojangSessionBase = "https://sessionserver.mojang.com"
	fallbackMaxBody   = 1 << 20 // 1 MiB — real profiles are ~2-4 KiB
	fallbackTimeout   = 4 * time.Second
)

// officialNameRe is Mojang's account-name charset; a name outside it can
// never exist there, so the round trip is skipped (saves a timeout per
// site-local CJK name).
var officialNameRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,16}$`)

// Fallback queries Mojang's official sessionserver for locally-missed
// profiles. BaseURL/Client are plain fields so tests can point at a stub;
// NewFallback builds the guarded production client.
type Fallback struct {
	BaseURL string
	Client  *http.Client
	// Enabled resolves the panel toggle per request (nil = off).
	Enabled func(ctx context.Context) bool
}

// NewFallback builds the production client: public-only dialer, no redirect
// following, hard deadline.
func NewFallback(enabled func(ctx context.Context) bool) *Fallback {
	return &Fallback{
		BaseURL: mojangSessionBase,
		Client:  newFallbackClient(nil),
		Enabled: enabled,
	}
}

// newFallbackClient fixes the client policy shared by production and tests:
// a hard deadline and NO redirect following (ErrUseLastResponse). transport
// nil installs the guarded public-only dialer; tests pass a plain transport
// against their loopback stub — the policy under test stays identical.
func newFallbackClient(transport http.RoundTripper) *http.Client {
	c := &http.Client{
		Timeout: fallbackTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	if transport != nil {
		c.Transport = transport
	} else {
		c.Transport = &http.Transport{
			DialContext:         publicDialContext,
			MaxIdleConnsPerHost: 4,
			IdleConnTimeout:     30 * time.Second,
		}
	}
	return c
}

func (f *Fallback) on(ctx context.Context) bool {
	return f != nil && f.Enabled != nil && f.Enabled(ctx)
}

// profile fetches the official profile for uuid (any wire spelling). Returns
// the raw JSON body — passed through verbatim so the response stays
// byte-compatible with Mojang's own sessionserver.
func (f *Fallback) profile(ctx context.Context, uuid string, withSig bool) ([]byte, bool) {
	if !f.on(ctx) {
		return nil, false
	}
	// Strict gate BEFORE URL assembly: the normalized value becomes a path
	// segment, so it must be exactly 32 lowercase hex characters.
	norm := NormalizeUUID(uuid)
	if len(norm) != 32 || !isLowerHex(norm) {
		return nil, false
	}
	raw := f.BaseURL + "/session/profile/" + norm
	if !withSig {
		// Mojang ships signed properties by default; unsigned=true strips
		// them (our !withSig callers asked for the stripped form).
		raw += "?unsigned=true"
	}
	return f.get(ctx, raw)
}

// hasJoined forwards a missed join verification to Mojang's hasJoined.
// Returns the official profile body when Mojang confirms the join.
func (f *Fallback) hasJoined(ctx context.Context, username, serverID, ip string) ([]byte, bool) {
	if !f.on(ctx) {
		return nil, false
	}
	if !officialNameRe.MatchString(username) {
		return nil, false // site-local name: can never exist upstream
	}
	if len(serverID) > 128 {
		return nil, false // pathological input: Mojang would 400 anyway
	}
	q := url.Values{}
	q.Set("username", username)
	q.Set("serverId", serverID)
	// Forward the IP hint only when it parses — a malformed value would
	// make Mojang 400 a join that might otherwise verify.
	if ip != "" && net.ParseIP(ip) != nil {
		q.Set("ip", ip)
	}
	return f.get(ctx, f.BaseURL+"/session/minecraft/hasJoined?"+q.Encode())
}

// get performs the guarded fetch: exactly 200 + a plausible profile JSON
// under the size cap. Everything else (4xx/5xx/redirect/garbage/oversize)
// is a miss.
func (f *Fallback) get(ctx context.Context, rawURL string) ([]byte, bool) {
	// Structural re-check: whatever the builders produced, the request may
	// only ever target the configured origin (defense in depth — the base
	// is constant in production, but tests and future editors get the same
	// guarantee).
	base, err := url.Parse(f.BaseURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, false
	}
	// Scheme and host are case-insensitive (RFC 3986 §3.2.2 / §3.2.1), so a
	// base typed in one case and a builder-produced URL in the other are the
	// same origin. Comparing with != would make the guard refuse a case
	// variant of its own base: the panel reads "fallback on" while every
	// lookup quietly degrades to a local miss. EqualFold rather than
	// lowercasing both sides — this is the guard's only comparison and it
	// must keep seeing the strings as they are.
	if !strings.EqualFold(req.URL.Scheme, base.Scheme) || !strings.EqualFold(req.URL.Host, base.Host) {
		return nil, false
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "SkinNexus session-fallback")
	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, fallbackMaxBody+1))
	if err != nil || len(body) > fallbackMaxBody {
		return nil, false
	}
	// Plausibility gate: a profile is a JSON object carrying id + name —
	// a captive portal's HTML or an error document never passes.
	var probe struct {
		ID   json.RawMessage `json:"id"`
		Name string          `json:"name"`
	}
	if err := json.Unmarshal(body, &probe); err != nil || len(probe.ID) == 0 || probe.Name == "" {
		return nil, false
	}
	return body, true
}

// isLowerHex reports whether s is all [0-9a-f] (length checked by callers).
func isLowerHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// isPublicIP admits only globally-routable unicast addresses. Go's
// IsPrivate covers RFC1918 + fc00::/7 but NOT CGNAT 100.64/10, which is
// checked explicitly; IsGlobalUnicast then drops 0/8, multicast and
// link-local leftovers.
func isPublicIP(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsPrivate() {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil {
		if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 { // 100.64.0.0/10
			return false
		}
	}
	return ip.IsGlobalUnicast()
}

// publicDialContext resolves addr and refuses non-public targets — the
// last line of defense against a poisoned resolver (the production base URL
// is a fixed public host; this makes "fixed" enforceable at socket level).
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
			lastErr = fmt.Errorf("session-fallback: non-public address %s refused for %s", ipa.IP, host)
			continue
		}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ipa.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("session-fallback: no public address for %s", host)
	}
	return nil, lastErr
}
