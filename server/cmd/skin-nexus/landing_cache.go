package main

// landing_cache.go — the landing page's aggregate stats (three COUNTs plus the
// likes-ordered featured library) are decorative and were recomputed on every
// request; S23 measured the likes query sorting the whole public library per
// hit. A short TTL with single-flight turns that into one computation per
// window. On a fetch failure the last good numbers are served: a blank landing
// page is worse than minute-old counts.

import (
	"context"
	"sync"
	"time"
)

// landingStatsTTL is a var so tests can shrink the window.
var landingStatsTTL = 60 * time.Second

type landingStats struct {
	Users    int
	Players  int
	Textures int
	Featured []landingTexture
}

type landingStatsCache struct {
	mu      sync.Mutex
	fetchMu sync.Mutex // serialises cold fetches so a burst computes once
	val     *landingStats
	at      time.Time
}

func (c *landingStatsCache) get(ctx context.Context, fetch func(context.Context) (landingStats, error)) (landingStats, error) {
	c.mu.Lock()
	if c.val != nil && time.Since(c.at) < landingStatsTTL {
		v := *c.val
		c.mu.Unlock()
		return v, nil
	}
	c.mu.Unlock()

	c.fetchMu.Lock()
	defer c.fetchMu.Unlock()
	// Another goroutine may have refreshed while we waited for the fetch lock.
	c.mu.Lock()
	if c.val != nil && time.Since(c.at) < landingStatsTTL {
		v := *c.val
		c.mu.Unlock()
		return v, nil
	}
	c.mu.Unlock()

	s, err := fetch(ctx)
	if err != nil {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.val != nil {
			return *c.val, nil
		}
		return landingStats{}, err
	}
	c.mu.Lock()
	c.val, c.at = &s, time.Now()
	c.mu.Unlock()
	return s, nil
}

var landingCache landingStatsCache
