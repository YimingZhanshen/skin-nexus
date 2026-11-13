package union

// metrics_test.go — a refused request is the signal an operator actually
// wants from this surface: which check stopped it. The reason tokens are the
// same ones the audit log records, so a spike in one of them names the
// misconfiguration (clock skew, a hub key rotation, a replayed nonce) without
// a log dive.
//
// ErrDisabled is deliberately not counted: a station that turned the module
// off is not failing verification, and counting it would read as an attack
// the moment somebody flips the switch.

import (
	"errors"
	"testing"

	"skin-nexus/server/internal/metrics"
)

func TestVerifyRefusalsAreCountedByReason(t *testing.T) {
	key := genRSA(t, 2048)
	v := newTestVerifier(t, &key.PublicKey, nil)
	before := metrics.SignatureFailures.Value("missing_header")
	if err := v.Verify(nil, "", "", ""); err == nil {
		t.Fatal("an empty signature must be refused")
	}
	if got := metrics.SignatureFailures.Value("missing_header"); got != before+1 {
		t.Fatalf("missing_header counter = %d, want %d", got, before+1)
	}
}

func TestDisabledVerifierCountsNothing(t *testing.T) {
	v, err := NewVerifier(Config{Enabled: false}, func(string, map[string]any) {})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	before := metrics.SignatureFailures.Value("disabled")
	if err := v.Verify(nil, "sig", "1", "n"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("err = %v, want ErrDisabled", err)
	}
	if got := metrics.SignatureFailures.Value("disabled"); got != before {
		t.Fatalf("a module that is simply off must not report failures: %d != %d", got, before)
	}
}
