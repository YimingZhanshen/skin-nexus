package main

// landing_cache_test.go — the landing page recomputed three COUNTs plus a
// likes-ordered library query on EVERY request (S23 measured the plan: a temp
// B-tree sort whose cost grows with the whole public library). The stats are
// decorative, so a short TTL is the honest fix: same numbers for a minute,
// one computation per minute instead of one per visitor.

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLandingStatsCacheTTL(t *testing.T) {
	old := landingStatsTTL
	landingStatsTTL = 40 * time.Millisecond
	defer func() { landingStatsTTL = old }()

	var c landingStatsCache
	var calls int32
	fetch := func(context.Context) (landingStats, error) {
		atomic.AddInt32(&calls, 1)
		return landingStats{Users: 7, Players: 3, Textures: 11}, nil
	}
	ctx := context.Background()
	a, err := c.get(ctx, fetch)
	if err != nil || a.Users != 7 || a.Players != 3 || a.Textures != 11 {
		t.Fatalf("first get = %+v err=%v, want the fetched stats", a, err)
	}
	b, err := c.get(ctx, fetch)
	if err != nil || b.Users != 7 {
		t.Fatalf("second get = %+v err=%v, want the cached stats", b, err)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("fetch ran %d times inside the TTL, want 1", n)
	}
	time.Sleep(60 * time.Millisecond)
	if _, err := c.get(ctx, fetch); err != nil {
		t.Fatalf("get after TTL: %v", err)
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Fatalf("fetch ran %d times after the TTL lapsed, want 2", n)
	}
}

func TestLandingStatsCacheSingleFlight(t *testing.T) {
	old := landingStatsTTL
	landingStatsTTL = time.Minute
	defer func() { landingStatsTTL = old }()

	var c landingStatsCache
	var calls int32
	fetch := func(context.Context) (landingStats, error) {
		atomic.AddInt32(&calls, 1)
		time.Sleep(30 * time.Millisecond)
		return landingStats{Users: 1}, nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.get(context.Background(), fetch); err != nil {
				t.Errorf("concurrent get: %v", err)
			}
		}()
	}
	wg.Wait()
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("16 concurrent cold gets ran %d fetches, want 1 (single-flight)", n)
	}
}

func TestLandingStatsCacheStaleOnError(t *testing.T) {
	old := landingStatsTTL
	landingStatsTTL = 40 * time.Millisecond
	defer func() { landingStatsTTL = old }()

	var c landingStatsCache
	ok := func(context.Context) (landingStats, error) { return landingStats{Users: 5}, nil }
	bad := func(context.Context) (landingStats, error) { return landingStats{}, errors.New("db down") }
	ctx := context.Background()
	if _, err := c.get(ctx, ok); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	// A transient DB failure must not blank the landing page: serve the last
	// good numbers rather than zeros.
	got, err := c.get(ctx, bad)
	if err != nil || got.Users != 5 {
		t.Fatalf("get on fetch error = %+v err=%v, want the stale stats and no error", got, err)
	}
}
