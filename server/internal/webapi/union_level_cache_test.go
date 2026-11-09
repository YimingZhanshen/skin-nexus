package webapi

// The security level costs two hub calls to read (member key -> code -> level),
// and the reference reads it on every page view. On this side the read is
// cached, so the promise worth pinning is the cache's own: repeated opens are
// one pair of calls, a stale entry re-reads, and a refused read retries soon
// without turning every page open into a fresh timeout.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"skin-nexus/server/internal/union"
)

func levelHub(t *testing.T, body string, status int) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	calls := &atomic.Int64{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/code":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":"WZBC"}`))
		case "/backend/WZBC/security/level":
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, calls
}

func TestSecurityLevelIsReadOncePerWindow(t *testing.T) {
	srv, calls := levelHub(t, "3", http.StatusOK)
	out, err := union.NewOutbound(union.Config{Enabled: true, ApiRoot: srv.URL, MemberKey: "mk-1"}, nil)
	if err != nil {
		t.Fatalf("outbound: %v", err)
	}
	c := &unionLevelCache{}
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		level, err := c.get(ctx, out)
		if err != nil {
			t.Fatalf("get %d: %v", i, err)
		}
		if level != 3 {
			t.Fatalf("get %d level = %d, want 3", i, level)
		}
	}
	if got := int(calls.Load()); got != 2 {
		t.Fatalf("hub calls = %d, want 2 (one two-hop read for three page opens)", got)
	}

	// Expire it the way a ten-minute-old entry expires, and the next read pays.
	c.mu.Lock()
	c.at = time.Now().Add(-unionLevelTTL - time.Second)
	c.mu.Unlock()
	if _, err := c.get(ctx, out); err != nil {
		t.Fatalf("get after expiry: %v", err)
	}
	if got := int(calls.Load()); got != 4 {
		t.Fatalf("hub calls = %d, want 4 after the window closed", got)
	}
}

// A hub that refuses is re-asked sooner than a hub that answers, but not once
// per request: the retry window exists so a page opened by ten players during an
// outage is ten requests in a minute, not ten timeouts each.
func TestSecurityLevelRefusedReadRetriesOnItsOwnClock(t *testing.T) {
	srv, calls := levelHub(t, "nope", http.StatusInternalServerError)
	out, err := union.NewOutbound(union.Config{Enabled: true, ApiRoot: srv.URL, MemberKey: "mk-1"}, nil)
	if err != nil {
		t.Fatalf("outbound: %v", err)
	}
	c := &unionLevelCache{}
	ctx := context.Background()

	if _, err := c.get(ctx, out); err == nil {
		t.Fatal("hub 500 accepted as a level")
	}
	if _, err := c.get(ctx, out); err == nil {
		t.Fatal("second read accepted after a refusal")
	}
	if got := int(calls.Load()); got != 2 {
		t.Fatalf("hub calls = %d, want 2 (the refusal is negative-cached)", got)
	}

	c.mu.Lock()
	c.at = time.Now().Add(-unionLevelRetryTTL - time.Second)
	c.mu.Unlock()
	if _, err := c.get(ctx, out); err == nil {
		t.Fatal("retry after the negative window accepted a failure")
	}
	if got := int(calls.Load()); got != 4 {
		t.Fatalf("hub calls = %d, want 4 after the retry window closed", got)
	}
}
