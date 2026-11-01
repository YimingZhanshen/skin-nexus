//go:build integration

// password_reset_integration_test.go: round-trip coverage for the A1
// 找回密码 token store — single-use consume, TTL, latest-email-wins,
// enumeration-safe email lookup. See integration_test.go for the harness.
package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestIntegrationPasswordResetTokenLifecycle(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	uid := seedUser(t, m)

	hash := strings.Repeat("a", 64)
	if err := m.CreatePasswordResetToken(ctx, uid, hash, 30*time.Minute); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := m.ConsumePasswordResetToken(ctx, hash)
	if err != nil || got != uid {
		t.Fatalf("consume = (%d, %v); want (%d, nil)", got, err, uid)
	}
	// Single-use: an immediate replay must find nothing.
	if _, err := m.ConsumePasswordResetToken(ctx, hash); !errors.Is(err, ErrNotFound) {
		t.Errorf("replay consume: %v; want ErrNotFound", err)
	}
}

func TestIntegrationPasswordResetUnknownAndExpired(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	uid := seedUser(t, m)

	if _, err := m.ConsumePasswordResetToken(ctx, strings.Repeat("f", 64)); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown digest: %v; want ErrNotFound", err)
	}

	// Expired row: write a token then rewind its deadline past (same DB
	// clock the validator uses — no Go-side time injected anywhere).
	expired := strings.Repeat("b", 64)
	if err := m.CreatePasswordResetToken(ctx, uid, expired, 30*time.Minute); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := m.db.ExecContext(ctx,
		`UPDATE password_reset_tokens SET expires_at = ? WHERE token_hash = ?`,
		time.Now().UTC().Add(-time.Minute), expired); err != nil {
		t.Fatalf("rewind: %v", err)
	}
	if _, err := m.ConsumePasswordResetToken(ctx, expired); !errors.Is(err, ErrNotFound) {
		t.Errorf("expired consume: %v; want ErrNotFound", err)
	}
}

func TestIntegrationPasswordResetLatestWins(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	uid := seedUser(t, m)

	first := strings.Repeat("1", 64)
	second := strings.Repeat("2", 64)
	if err := m.CreatePasswordResetToken(ctx, uid, first, 30*time.Minute); err != nil {
		t.Fatalf("create first: %v", err)
	}
	if err := m.CreatePasswordResetToken(ctx, uid, second, 30*time.Minute); err != nil {
		t.Fatalf("create second: %v", err)
	}
	// The newer request invalidates the older token (latest email wins).
	if _, err := m.ConsumePasswordResetToken(ctx, first); !errors.Is(err, ErrNotFound) {
		t.Errorf("stale token still live: %v; want ErrNotFound", err)
	}
	if got, err := m.ConsumePasswordResetToken(ctx, second); err != nil || got != uid {
		t.Errorf("fresh consume = (%d, %v); want (%d, nil)", got, err, uid)
	}
}

func TestIntegrationUserByEmail(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	uid := seedUserAs(t, m, "ResetCase@example.com", "reset_case_user")

	// Case-insensitive match on the address only.
	got, err := m.UserByEmail(ctx, "resetcase@example.com")
	if err != nil || got != uid {
		t.Fatalf("UserByEmail = (%d, %v); want (%d, nil)", got, err, uid)
	}
	// A nickname is NOT an email — the forgot flow must never resolve it.
	if _, err := m.UserByEmail(ctx, "reset_case_user"); !errors.Is(err, ErrNotFound) {
		t.Errorf("nickname as email: %v; want ErrNotFound", err)
	}
	if _, err := m.UserByEmail(ctx, "nobody@example.com"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown email: %v; want ErrNotFound", err)
	}
}
