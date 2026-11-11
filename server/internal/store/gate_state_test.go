package store

// gate_state_test.go — the web gate decides from one read.
//
// The panel has to answer "is this account still allowed to be used?" on every
// authenticated request. Three separate reads (flag, clock, permission) would
// let a verification land between them and produce a request that is both
// "just verified" and "was blocked" — an answer nobody can debug. So there is
// one accessor, and this test pins that it actually returns all three columns
// rather than a zero value that happens to look like "not pending".

import (
	"context"
	"testing"
)

func TestGateStateReadsAllThreeFacts(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()

	uid, err := st.CreateUser(ctx, "pending@example.test", "pending", "hash", 5, "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	got, err := st.GateState(ctx, uid)
	if err != nil {
		t.Fatalf("GateState: %v", err)
	}
	if got.EmailVerified {
		t.Fatal("a fresh account must read as unverified — the gate would open itself")
	}
	if got.Email != "pending@example.test" {
		t.Fatalf("email = %q, want the address the verification screen has to name", got.Email)
	}
	if got.CreatedAt.IsZero() {
		t.Fatal("created_at came back zero: the grace window would be skipped and every new account blocked instantly")
	}
	if got.Permission != 0 {
		t.Fatalf("permission = %d for a plain registrant, want 0", got.Permission)
	}

	// The two columns the gate flips during its own lifetime must be visible
	// through this accessor, otherwise the middleware decides from stale facts.
	if err := st.SetEmailVerified(ctx, uid, true); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := st.SetUserPermission(ctx, uid, 2); err != nil {
		t.Fatalf("grant super admin: %v", err)
	}
	after, err := st.GateState(ctx, uid)
	if err != nil {
		t.Fatalf("GateState after the flip: %v", err)
	}
	if !after.EmailVerified || after.Permission != 2 || after.Email != "pending@example.test" {
		t.Fatalf("GateState = %+v, must track email_verified and permission", after)
	}

	if _, err := st.GateState(ctx, uid+9999); err != ErrNotFound {
		t.Fatalf("missing account = %v, want ErrNotFound so the caller can fail open to the normal 401", err)
	}
}

func TestUnverifiedAccountsCountsTheBlastRadius(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()

	// Empty site: zero, not an error — the panel row has to render before the
	// first registration exists.
	if n, err := st.UnverifiedAccounts(ctx); err != nil || n != 0 {
		t.Fatalf("empty site = (%d, %v), want (0, nil)", n, err)
	}
	for i, nick := range []string{"one", "two", "three"} {
		uid, err := st.CreateUser(ctx, nick+"@example.test", nick, "hash", 5, "")
		if err != nil {
			t.Fatalf("create %s: %v", nick, err)
		}
		if i == 2 {
			if err := st.SetEmailVerified(ctx, uid, true); err != nil {
				t.Fatalf("verify third: %v", err)
			}
		}
	}
	n, err := st.UnverifiedAccounts(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Fatalf("unverified = %d, want 2 — the count the operator reads before flipping the gate", n)
	}
}
