//go:build integration

// register_collision_integration_test.go covers what makes "which half is
// taken?" answerable: the insert maps a unique violation onto one portable
// error, and the probe reads the nickname the way login reads it (folded), so
// the name a player could log in with is the name reported as occupied.

package store

import (
	"context"
	"errors"
	"testing"
)

func TestIntegrationCreateUserCollisionIsErrDuplicate(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	seedUserAs(t, m, "collision-a@example.com", "CollisionNick")

	_, err := m.CreateUser(ctx, "collision-b@example.com", "CollisionNick", "x", 3, "")
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("nickname collision err = %v, want ErrDuplicate", err)
	}
	_, err = m.CreateUser(ctx, "collision-a@example.com", "CollisionNickTwo", "x", 3, "")
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("email collision err = %v, want ErrDuplicate", err)
	}
}

func TestIntegrationNicknameTakenProbe(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	seedUserAs(t, m, "probe-a@example.com", "ProbeNick")

	// Folded, matching UserByIdentifier: the name that would log in is the name
	// reported taken, on every engine (MySQL/SQLite fold by collation, the
	// probe folds explicitly).
	for _, name := range []string{"ProbeNick", "probenick"} {
		taken, err := m.NicknameTaken(ctx, name)
		if err != nil {
			t.Fatalf("NicknameTaken(%q): %v", name, err)
		}
		if !taken {
			t.Errorf("NicknameTaken(%q) = false, want true", name)
		}
	}
	taken, err := m.NicknameTaken(ctx, "NobodyHere")
	if err != nil {
		t.Fatalf("NicknameTaken(absent): %v", err)
	}
	if taken {
		t.Error("an unused nickname must not read as taken")
	}
}
