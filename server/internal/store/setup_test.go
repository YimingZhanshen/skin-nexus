package store

// The install wizard is a one-way door, but until now the door was checked
// before it was closed: NeedsSetup was an unlocked COUNT(*) and the handler
// created the account afterwards, so two anonymous POSTs racing on a freshly
// exposed site could both pass and both end up with permission=2.

import (
	"context"
	"testing"
)

func TestClaimSetupHasExactlyOneWinner(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()

	spec := SetupSpec{
		Email: "admin@example.com", Nickname: "Notch", PasswordHash: "hash", PlayerLimit: 5,
		Options: map[string]string{"site_name": "Alpha", "site_url": "https://alpha.example"},
	}
	uid, claimed, err := st.ClaimSetup(ctx, spec)
	if err != nil || !claimed || uid <= 0 {
		t.Fatalf("first ClaimSetup = uid %d, claimed %v, err %v", uid, claimed, err)
	}

	// Same content, second caller: it must lose the claim, not error out — the
	// handler turns "lost" into the one-way-door answer.
	_, claimed2, err := st.ClaimSetup(ctx, spec)
	if err != nil {
		t.Fatalf("second ClaimSetup returned an error, want a lost claim: %v", err)
	}
	if claimed2 {
		t.Fatal("two callers must not both claim the same installation")
	}

	var supers int
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE permission >= 2`).Scan(&supers); err != nil {
		t.Fatal(err)
	}
	if supers != 1 {
		t.Fatalf("super admin rows = %d, want 1", supers)
	}

	// The options the wizard collected land in the same transaction.
	for k, want := range spec.Options {
		var got string
		if err := st.DB().QueryRowContext(ctx, `SELECT v FROM options WHERE k = ?`, k).Scan(&got); err != nil {
			t.Fatalf("read option %q: %v", k, err)
		}
		if got != want {
			t.Fatalf("option %q = %q, want %q", k, got, want)
		}
	}
}

// A failed account creation must not burn the marker — a site that answers
// "setup already completed" with nobody able to sign in cannot recover without
// hand-editing the database.
func TestClaimSetupRollsBackWithoutBurningTheMarker(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()

	// Collide on the nickname the setup would use.
	if _, err := st.CreateUser(ctx, "someone@example.com", "Notch", "x", 3, ""); err != nil {
		t.Fatalf("seed a colliding nickname: %v", err)
	}

	_, claimed, err := st.ClaimSetup(ctx, SetupSpec{
		Email: "admin@example.com", Nickname: "Notch", PasswordHash: "hash", PlayerLimit: 3,
		Options: map[string]string{"site_name": "Alpha"},
	})
	if claimed {
		t.Fatal("a setup whose account could not be created must not report success")
	}
	if err == nil {
		t.Fatal("want the collision to surface, got nil")
	}

	var done string
	if err := st.DB().QueryRowContext(ctx, `SELECT v FROM options WHERE k = 'setup_done'`).Scan(&done); err == nil {
		t.Fatalf("setup_done was written even though the account failed (v=%q); the site is now un-installable", done)
	}
	var names int
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM options WHERE k = 'site_name'`).Scan(&names); err != nil {
		t.Fatal(err)
	}
	if names != 0 {
		t.Fatal("the rollback must also undo the wizard's options, not just the marker")
	}

	// Retrying with a free nickname still works.
	uid, claimed, err := st.ClaimSetup(ctx, SetupSpec{
		Email: "admin@example.com", Nickname: "Alex", PasswordHash: "hash", PlayerLimit: 3,
	})
	if err != nil || !claimed || uid <= 0 {
		t.Fatalf("retry after rollback = uid %d, claimed %v, err %v", uid, claimed, err)
	}
}
