package oidc

import (
	"context"
	"errors"
	"testing"
)

// TestResolverOpaqueTokenSentinel pins the ErrNotJOSE sentinel for opaque
// password-flow tokens: the main.go dual-state wrapper keys its store
// fallback on this exact error, so it must not change shape silently.
func TestResolverOpaqueTokenSentinel(t *testing.T) {
	resolve := NewYggTokenResolver(nil, "https://x.test", nil, TokenPolicy{})
	_, err := resolve(context.Background(), "deadbeefcafebabe")
	if !errors.Is(err, ErrNotJOSE) {
		t.Fatalf("opaque token err = %v (want ErrNotJOSE sentinel)", err)
	}
	_, err = resolve(context.Background(), "a.b.c")
	if err == nil {
		t.Fatal("malformed JWT must not resolve")
	}
}
