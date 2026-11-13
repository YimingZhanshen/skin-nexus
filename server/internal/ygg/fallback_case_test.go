package ygg

// fallback_case_test.go — the outbound guard's one comparison must be
// case-insensitive, and must still refuse everything else.
//
// RFC 3986 treats the scheme and host of a URI as case-insensitive, so
// "https://session.invalid" and "https://SESSION.invalid" are one origin.
// The guard in Fallback.get re-checks every URL the builders produce against
// the configured base (defense in depth: user input reaches the path and the
// query, never the authority) — comparing those with != makes the guard
// refuse a case variant of its OWN base, which reads as "fallback is on"
// while every lookup silently degrades to a local miss.
//
// This is a sub-commit of W1-2 on purpose: the comparison lives in the
// outbound SSRF guard, not on the frozen response surface, so it is changed
// on its own with its own evidence rather than folded into the whitelist
// work.

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

// caseProbeTransport records the URLs that reached it and fails the round
// trip, so a test observes only whether the structural check let the request
// through — never a real dial.
type caseProbeTransport struct {
	urls []string
}

func (p *caseProbeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	p.urls = append(p.urls, r.URL.String())
	return nil, errors.New("stopped by the test")
}

func caseProbeFallback(tr *caseProbeTransport) *Fallback {
	return &Fallback{
		BaseURL: "https://session.invalid",
		Client:  newFallbackClient(tr),
		Enabled: func(context.Context) bool { return true },
	}
}

// A case variant of the configured base is the configured base: the request
// must reach the transport instead of being refused by the guard.
func TestFallbackHostCheckIgnoresCase(t *testing.T) {
	tr := &caseProbeTransport{}
	f := caseProbeFallback(tr)
	f.get(context.Background(), "https://SESSION.INVALID/session/minecraft/hasJoined?username=Steve&serverId=00")
	if len(tr.urls) != 1 {
		t.Fatalf("case-variant of the configured origin must reach the transport, got %d requests (%v)", len(tr.urls), tr.urls)
	}
}

// The other half of the same guard: a genuinely different host is still
// refused before anything is dialled. Without this the first test would also
// pass if the comparison were deleted outright.
func TestFallbackHostCheckStillRefusesOtherHosts(t *testing.T) {
	tr := &caseProbeTransport{}
	f := caseProbeFallback(tr)
	f.get(context.Background(), "https://evil.invalid/session/minecraft/hasJoined?username=Steve&serverId=00")
	f.get(context.Background(), "http://session.invalid/session/minecraft/hasJoined?username=Steve&serverId=00")
	if len(tr.urls) != 0 {
		t.Fatalf("a foreign host or a downgraded scheme must never be dialled, got %v", tr.urls)
	}
}
