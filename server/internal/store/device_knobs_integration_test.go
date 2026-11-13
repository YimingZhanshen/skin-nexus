//go:build integration

package store

import (
	"context"
	"testing"
	"time"
)

// The device-code knobs are panel settings, option-first and read LIVE by the
// provider on every device_authorization request — what the panel saves must
// be what the provider serves, with no restart (unlike the restart-flagged
// keys). The defaults are the contract the panel's fallback column shows, so
// the same two constants back both sides. Garbage must fall back to the
// default, never to zero: a zero lifetime would let the OP mint a code that
// is already expired.
func TestIntegrationOIDCDeviceKnobsReadPanelOptions(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()

	life, poll := m.OIDCDeviceKnobs(ctx)
	if life != DefaultDeviceCodeLifetime || poll != DefaultDeviceCodePoll {
		t.Fatalf("defaults = %v / %d, want %v / %d", life, poll, DefaultDeviceCodeLifetime, DefaultDeviceCodePoll)
	}

	if err := m.SetOption(ctx, "oidc_device_lifetime", "123"); err != nil {
		t.Fatalf("set lifetime: %v", err)
	}
	if err := m.SetOption(ctx, "oidc_device_poll_secs", "9"); err != nil {
		t.Fatalf("set poll: %v", err)
	}
	life, poll = m.OIDCDeviceKnobs(ctx)
	if life != 123*time.Second || poll != 9 {
		t.Fatalf("after save = %v / %d, want 123s / 9 — the panel write is what the provider must read", life, poll)
	}

	if err := m.SetOption(ctx, "oidc_device_lifetime", "not-a-number"); err != nil {
		t.Fatalf("set garbage: %v", err)
	}
	if life, _ := m.OIDCDeviceKnobs(ctx); life != DefaultDeviceCodeLifetime {
		t.Fatalf("garbage lifetime = %v, want the default back (zero would mint expired codes)", life)
	}
}
