package ygg

import (
	"context"
	"errors"
	"testing"
	"time"
)

// failRevokeStore simulates a DB blip on the rotation step only.
type failRevokeStore struct{ *memStore }

func (f *failRevokeStore) InvalidateToken(context.Context, string) error {
	return errors.New("db down")
}

// A refresh must rotate the token; if the old one cannot be invalidated the
// refresh still succeeds, but the failure has to leave a trace — a silent
// swallow leaves the old token alive while the caller believes it rotated.
func TestRefreshRevokeFailureLeavesATrace(t *testing.T) {
	st := newMemStore()
	svc := NewService(&failRevokeStore{st}, nil)
	var actions []int64
	var names []string
	svc.Audit = func(_ context.Context, actorUID int64, action string, _ any) {
		actions = append(actions, actorUID)
		names = append(names, action)
	}
	if err := st.SaveToken(context.Background(), Token{
		AccessToken: "old", ClientToken: "ct", UserID: 5,
		CreatedAt: time.Now().Add(-time.Minute), ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	tok, _, err := svc.Refresh(context.Background(), "old", "ct", 0)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if tok.AccessToken == "" || tok.AccessToken == "old" {
		t.Fatalf("refresh did not rotate the token: %q", tok.AccessToken)
	}
	if len(names) != 1 || names[0] != "ygg_refresh_token_revoke_failed" {
		t.Fatalf("audit actions = %v, want one ygg_refresh_token_revoke_failed", names)
	}
	if actions[0] != 5 {
		t.Fatalf("audit actor = %d, want 5", actions[0])
	}
}
