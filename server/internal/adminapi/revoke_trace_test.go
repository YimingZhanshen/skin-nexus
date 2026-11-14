package adminapi

import (
	"context"
	"os"
	"strings"
	"testing"
)

// The revoke is best-effort to the caller, but a failure must leave an audit
// trace. Force a real failure by dropping the tokens table under the store.
func TestRevokeFailureLeavesAnAuditTrace(t *testing.T) {
	b := newDiagBackend(t)
	if _, err := b.Store.DB().Exec(`DROP TABLE ygg_tokens`); err != nil {
		t.Fatalf("drop ygg_tokens: %v", err)
	}
	b.revokeUserTokens(context.Background(), 7, 42, "admin_user_password_reset_token_revoke_failed", nil)
	var actor int64
	var action string
	row := b.Store.DB().QueryRow(`SELECT actor_uid, action FROM audit_log WHERE action = ?`,
		"admin_user_password_reset_token_revoke_failed")
	if err := row.Scan(&actor, &action); err != nil {
		t.Fatalf("revocation failure left no audit trace: %v", err)
	}
	if actor != 7 {
		t.Fatalf("audit actor = %d, want 7", actor)
	}
}

// Every call site must route through the traced helper; a bare `_ =` swallow
// is exactly the silent-failure shape this round exists to kill.
func TestRevokeWiringHasNoSilentSwallows(t *testing.T) {
	total := 0
	for _, name := range []string{"usermgmt.go", "playermgmt.go"} {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(src), "_ = b.Store.InvalidateUserTokens") {
			t.Errorf("%s still swallows a revocation error silently", name)
		}
		total += strings.Count(string(src), "b.revokeUserTokens(")
	}
	if total != 3 {
		t.Fatalf("revokeUserTokens call sites = %d, want 3 (usermgmt x2 + playermgmt x1)", total)
	}
}
