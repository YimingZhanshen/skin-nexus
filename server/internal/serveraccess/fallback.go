package serveraccess

import (
	"log/slog"
	"sync"
	"time"

	"skin-nexus/server/internal/metrics"
	"skin-nexus/server/internal/ygg"
)

const (
	fallbackFailureThreshold = 5
	fallbackCooldown         = 30 * time.Second
)

type breakerState uint8

const (
	breakerClosed breakerState = iota
	breakerOpen
	breakerHalfOpen
)

// acquireFallback uses the same per-server registry and MaxConcurrent policy as
// local verification, with a separate official-provider counter. The circuit
// only gates fallback; local sessions and emergency bans never depend on it.
func (v *verifier) acquireFallback(serverID int64, limit int) (ygg.FallbackPermit, bool) {
	if limit <= 0 {
		return ygg.FallbackPermit{ReasonCode: "fallback_concurrent_limit"}, false
	}
	v.mu.Lock()
	entry := v.concurrency[serverID]
	if entry == nil {
		entry = &serverConcurrency{}
		v.concurrency[serverID] = entry
	}
	v.mu.Unlock()

	entry.mu.Lock()
	now := v.now()
	if entry.fallbackActive >= limit {
		entry.mu.Unlock()
		return ygg.FallbackPermit{ReasonCode: "fallback_concurrent_limit"}, false
	}
	if entry.breaker == breakerOpen {
		if now.Sub(entry.openedAt) < fallbackCooldown {
			entry.mu.Unlock()
			return ygg.FallbackPermit{ReasonCode: "fallback_circuit_open"}, false
		}
		entry.breaker = breakerHalfOpen
	}
	// A half-open circuit admits exactly one official-provider probe. Local
	// verification may continue alongside it under the ordinary policy cap.
	if entry.breaker == breakerHalfOpen && entry.probeActive {
		entry.mu.Unlock()
		return ygg.FallbackPermit{ReasonCode: "fallback_circuit_open"}, false
	}
	if entry.breaker == breakerHalfOpen {
		entry.probeActive = true
	}
	entry.fallbackActive++
	state := entry.breaker
	entry.mu.Unlock()

	var once sync.Once
	return ygg.FallbackPermit{Release: func(healthy bool) {
		once.Do(func() { v.releaseFallback(serverID, entry, state, healthy) })
	}}, true
}

func (v *verifier) releaseFallback(serverID int64, entry *serverConcurrency, state breakerState, healthy bool) {
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.fallbackActive > 0 {
		entry.fallbackActive--
	}
	if state == breakerHalfOpen {
		entry.probeActive = false
	}
	now := v.now()
	switch state {
	case breakerHalfOpen:
		if healthy && entry.breaker == breakerHalfOpen {
			entry.breaker, entry.failures, entry.openedAt = breakerClosed, 0, time.Time{}
			return
		}
		if !healthy && entry.breaker == breakerHalfOpen {
			v.openFallback(serverID, entry, now)
		}
	case breakerClosed:
		// A request that began closed may finish after another request opened
		// the circuit. It must not close or re-open that newer state.
		if entry.breaker != breakerClosed {
			return
		}
		if healthy {
			entry.failures = 0
			return
		}
		entry.failures++
		if entry.failures >= fallbackFailureThreshold {
			v.openFallback(serverID, entry, now)
		}
	}
}

func (v *verifier) openFallback(serverID int64, entry *serverConcurrency, now time.Time) {
	entry.breaker, entry.openedAt = breakerOpen, now
	metrics.ServerAccessFallbackBreakerOpen.Inc()
	slog.Warn("serveraccess.fallback breaker_open", "server_id", serverID)
}
