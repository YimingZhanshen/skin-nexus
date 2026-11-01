//go:build integration

// email_verify_integration_test.go: round-trip coverage for the verification
// token store and for the rule that makes the join gate trustworthy — an
// address change takes the verified flag (and any live link) away with it.
// See integration_test.go for the harness.
package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestIntegrationEmailVerifyTokenLifecycle(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	uid := seedUser(t, m)

	hash := strings.Repeat("a", 64)
	if err := m.CreateEmailVerifyToken(ctx, uid, hash, 30*time.Minute); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := m.ConsumeEmailVerifyToken(ctx, hash)
	if got != uid || err != nil {
		t.Fatalf("consume = (%d, %v), want (%d, nil)", got, err, uid)
	}
	if _, err := m.ConsumeEmailVerifyToken(ctx, hash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("replay: %v, want ErrNotFound", err)
	}
}

func TestIntegrationEmailVerifyUnknownAndExpired(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	uid := seedUser(t, m)

	if _, err := m.ConsumeEmailVerifyToken(ctx, strings.Repeat("f", 64)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown digest: %v, want ErrNotFound", err)
	}

	stale := strings.Repeat("b", 64)
	if err := m.CreateEmailVerifyToken(ctx, uid, stale, time.Hour); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Rewind with the same clock the validator reads (the store's clock), so no
	// engine's date arithmetic is involved.
	if _, err := m.db.ExecContext(ctx,
		`UPDATE email_verify_tokens SET expires_at = ? WHERE token_hash = ?`,
		time.Now().UTC().Add(-time.Minute), stale); err != nil {
		t.Fatalf("rewind: %v", err)
	}
	if _, err := m.ConsumeEmailVerifyToken(ctx, stale); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired consume: %v, want ErrNotFound", err)
	}
}

func TestIntegrationEmailVerifyLatestWins(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	uid := seedUser(t, m)

	first := strings.Repeat("1", 64)
	second := strings.Repeat("2", 64)
	for _, h := range []string{first, second} {
		if err := m.CreateEmailVerifyToken(ctx, uid, h, time.Hour); err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	if _, err := m.ConsumeEmailVerifyToken(ctx, first); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale link still live: %v, want ErrNotFound", err)
	}
	if _, err := m.ConsumeEmailVerifyToken(ctx, second); err != nil {
		t.Fatalf("fresh link: %v, want nil", err)
	}
}

// The mail-failure path deletes the token it just minted (a link nobody
// received must not stay live in the DB); this is that primitive.
func TestIntegrationEmailVerifyTokenDeletedAfterSendFailure(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	uid := seedUser(t, m)

	hash := strings.Repeat("9", 64)
	if err := m.CreateEmailVerifyToken(ctx, uid, hash, time.Hour); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := m.DeleteEmailVerifyToken(ctx, hash); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := m.ConsumeEmailVerifyToken(ctx, hash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted token still consumable: %v, want ErrNotFound", err)
	}
	// Deleting twice is not an error (nothing to match), and it must not touch
	// another uid's live row.
	if err := m.DeleteEmailVerifyToken(ctx, hash); err != nil {
		t.Fatalf("second delete: %v", err)
	}
	keep := strings.Repeat("8", 64)
	if err := m.CreateEmailVerifyToken(ctx, uid, keep, time.Hour); err != nil {
		t.Fatalf("create keep: %v", err)
	}
	if err := m.DeleteEmailVerifyToken(ctx, strings.Repeat("7", 64)); err != nil {
		t.Fatalf("delete unrelated digest: %v", err)
	}
	if _, err := m.ConsumeEmailVerifyToken(ctx, keep); err != nil {
		t.Fatalf("unrelated delete touched a live token: %v", err)
	}
}

// The address is the thing being proved, so changing it must invalidate both
// the flag and any link still in flight — otherwise a link mailed to the old
// mailbox verifies the new address.
func TestIntegrationUpdateUserEmailClearsVerification(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	uid := seedUser(t, m)

	brief, err := m.UserBrief(ctx, uid)
	if err != nil {
		t.Fatalf("brief: %v", err)
	}
	if brief.EmailVerified {
		t.Fatal("seeded user should start unverified")
	}
	if err := m.SetEmailVerified(ctx, uid, true); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if b, err := m.UserBrief(ctx, uid); err != nil || !b.EmailVerified {
		t.Fatalf("after verify: %+v %v, want verified", b, err)
	}

	live := strings.Repeat("c", 64)
	if err := m.CreateEmailVerifyToken(ctx, uid, live, time.Hour); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := m.UpdateUserEmail(ctx, uid, "changed-a2@example.com"); err != nil {
		t.Fatalf("update email: %v", err)
	}
	b, err := m.UserBrief(ctx, uid)
	if err != nil {
		t.Fatalf("brief after change: %v", err)
	}
	if b.EmailVerified {
		t.Error("address change left email_verified set — the gate would trust an unproven mailbox")
	}
	if _, err := m.ConsumeEmailVerifyToken(ctx, live); !errors.Is(err, ErrNotFound) {
		t.Fatalf("link to the old address still live: %v, want ErrNotFound", err)
	}
}

// Saving the profile without changing the address must not silently log the
// player out of the gate they already passed.
func TestIntegrationUpdateUserEmailNoChangeKeepsFlag(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	uid := seedUser(t, m)

	brief, err := m.UserBrief(ctx, uid)
	if err != nil || brief.Email == "" {
		t.Fatalf("seed user has no email: %+v %v", brief, err)
	}
	if err := m.SetEmailVerified(ctx, uid, true); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := m.UpdateUserEmail(ctx, uid, brief.Email); err != nil {
		t.Fatalf("no-op update: %v", err)
	}
	b, err := m.UserBrief(ctx, uid)
	if err != nil {
		t.Fatalf("brief: %v", err)
	}
	if !b.EmailVerified {
		t.Error("same address written back cleared the verified flag")
	}
}
