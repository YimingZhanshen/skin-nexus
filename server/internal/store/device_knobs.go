package store

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// Device-code knobs (panel settings, option-first). The provider reads these
// LIVE on every device_authorization request — no restart, unlike the
// restart-flagged keys — so this is the one place the values are resolved.
// The defaults back both the provider's fallback and the panel's
// SettingsDefaults column: one pair of constants so the two sides can never
// show different numbers. A stored value that is not a positive integer
// falls back to the default; zero would make the OP mint a code that is
// already expired.
const (
	DefaultDeviceCodeLifetime = 600 * time.Second // Janus parity: expires_in 600
	DefaultDeviceCodePoll     = 5                 // seconds between client polls
)

func (m *Mysql) OIDCDeviceKnobs(ctx context.Context) (time.Duration, int) {
	life := DefaultDeviceCodeLifetime
	poll := DefaultDeviceCodePoll
	if v, ok, err := m.GetOption(ctx, "oidc_device_lifetime"); err == nil && ok {
		if n, aerr := strconv.Atoi(strings.TrimSpace(v)); aerr == nil && n > 0 {
			life = time.Duration(n) * time.Second
		}
	}
	if v, ok, err := m.GetOption(ctx, "oidc_device_poll_secs"); err == nil && ok {
		if n, aerr := strconv.Atoi(strings.TrimSpace(v)); aerr == nil && n > 0 {
			poll = n
		}
	}
	return life, poll
}
