package union

// verify_janitor_test.go — the nonce replay cache is process-local and, before
// this, StartJanitor had no caller: a quiet member site would keep expired
// nonces in memory forever (cleanup only ran lazily on writes). The boot path
// now starts the janitor; this pins that the janitor actually sweeps.

import (
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"
)

func janitorKey(t *testing.T) *rsa.PublicKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return &k.PublicKey
}

func TestJanitorSweepsExpiredNonces(t *testing.T) {
	v := newTestVerifier(t, janitorKey(t), nil)
	base := time.Now()
	v.now = func() time.Time { return base }

	v.remember("nonce-a")
	v.remember("nonce-b")
	if !v.seen("nonce-a") {
		t.Fatal("fresh nonce must be seen")
	}

	// Age both nonces past the TTL, then let the janitor run.
	v.now = func() time.Time { return base.Add(NonceTTL + time.Second) }
	done := make(chan struct{})
	defer close(done)
	v.StartJanitor(done, 5*time.Millisecond)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		v.mu.Lock()
		n := len(v.nonces)
		v.mu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("janitor did not sweep expired nonces within 2s")
}

func TestJanitorDisabledIsNoop(t *testing.T) {
	v := newTestVerifier(t, janitorKey(t), nil)
	v.now = func() time.Time { return time.Now() }
	v.remember("nonce-x")
	done := make(chan struct{})
	close(done)
	// interval <= 0 must not start a goroutine nor panic.
	v.StartJanitor(done, 0)
	if !v.seen("nonce-x") {
		t.Fatal("a disabled janitor must not touch the cache")
	}
}
