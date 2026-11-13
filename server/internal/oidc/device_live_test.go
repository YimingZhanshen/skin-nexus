package oidc

// The device knobs must come from the LIVE source when one is wired: zitadel
// bakes its config into op.Provider at construction and reads it back through
// DeviceAuthorization() on every device_authorization request, so this
// override is the entire mechanism behind "a panel save takes effect with no
// restart". The baked base is captured as a value, so the non-knob half of
// the shape (UserCode charset, form path) must survive the override untouched,
// and with no knobs wired the baked config must pass through as-is.
import (
	"testing"
	"time"

	"github.com/zitadel/oidc/v3/pkg/op"
)

func bakedShape() op.DeviceAuthorizationConfig {
	return op.DeviceAuthorizationConfig{
		Lifetime:     10 * time.Minute,
		PollInterval: 5 * time.Second,
		UserFormPath: "/device",
	}
}

func TestLiveDeviceKnobsOverrideBakedConfig(t *testing.T) {
	owner := &Provider{DeviceKnobs: func() (time.Duration, int) {
		return 123 * time.Second, 9
	}}
	live := &liveDevice{owner: owner, baked: bakedShape()}
	cfg := live.DeviceAuthorization()
	if cfg.Lifetime != 123*time.Second {
		t.Fatalf("Lifetime = %v, want 123s — the panel value must reach the response AND the stored expiry", cfg.Lifetime)
	}
	if cfg.PollInterval != 9*time.Second {
		t.Fatalf("PollInterval = %v, want 9s", cfg.PollInterval)
	}
	if cfg.UserFormPath != "/device" {
		t.Fatalf("UserFormPath = %q, want the baked shape preserved", cfg.UserFormPath)
	}
}

func TestLiveDeviceWithoutKnobsKeepsBakedConfig(t *testing.T) {
	baked := bakedShape()
	live := &liveDevice{baked: baked}
	got := live.DeviceAuthorization()
	if got.Lifetime != baked.Lifetime || got.PollInterval != baked.PollInterval || got.UserFormPath != baked.UserFormPath {
		t.Fatalf("nil knobs altered the baked config: %+v vs %+v", got, baked)
	}
}
