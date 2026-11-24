//go:build integration

package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

// TestIntegrationServerAccessControlPlane runs the same repository contract on
// whichever disposable engine SKIN_TEST_DSN selects (MySQL/PostgreSQL/SQLite).
func TestIntegrationServerAccessConcurrentQuotas(t *testing.T) {
	m := openTestStore(t)
	t.Run("server tenant quota", func(t *testing.T) {
		saExerciseConcurrentServerQuota(t, m)
	})
	t.Run("whitelist policy quota", func(t *testing.T) {
		saExerciseConcurrentWhitelistQuota(t, m)
	})
}

func TestIntegrationServerAccessControlPlane(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	uid := seedUser(t, m)
	tenant, err := m.CreateSATenant(ctx, "integration", 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.AddSATenantMember(ctx, tenant, uid, "owner"); err != nil {
		t.Fatal(err)
	}
	server, err := m.CreateSAServer(ctx, tenant, "srv_integration", "integration")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	hash := saTestHash("integration-secret")
	cid, err := m.CreateSAServerCredential(ctx, tenant, server.ID, SACredential{
		Mode: "url_secret", SecretHash: hash, Scopes: `["verify:join"]`, Status: "active",
		ExpiresAt: sql.NullTime{Time: now.Add(time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := m.SACredentialBySecretHash(ctx, hash, now)
	if err != nil || credential.ID != cid || credential.TenantID != tenant {
		t.Fatalf("credential lookup = %+v %v", credential, err)
	}
	if err := m.AddSAServerWhitelist(ctx, tenant, server.ID, uid, "integration", uid); err != nil {
		t.Fatal(err)
	}
	if err := m.AddSAServerBan(ctx, tenant, server.ID, uid, "integration", sql.NullTime{}, uid); err != nil {
		t.Fatal(err)
	}
	inviteHash := saTestHash("integration-invite")
	if _, err := m.CreateSATenantInvite(ctx, tenant, "it@example.com", "viewer", inviteHash, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AcceptSATenantInvite(ctx, inviteHash, uid, "IT@example.com", now); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AcceptSATenantInvite(ctx, inviteHash, uid, "it@example.com", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invite replay err = %v", err)
	}
	otherTenant, err := m.CreateSATenant(ctx, "integration-other", 2)
	if err != nil {
		t.Fatal(err)
	}
	otherHash := saTestHash("integration-other-invite")
	if _, err := m.CreateSATenantInvite(ctx, otherTenant, "it@example.com", "viewer", otherHash, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AcceptSATenantInvite(ctx, otherHash, uid, "it@example.com", now); !errors.Is(err, ErrForbidden) {
		t.Fatalf("second active membership err = %v, want forbidden", err)
	}
	enabled := true
	rate := 44
	if err := m.UpdateSAServerOwnerPolicy(ctx, tenant, server.ID, &enabled, nil); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateSAServerPlatformPolicy(ctx, tenant, server.ID, &rate, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	policy, err := m.SAServerPolicy(ctx, tenant, server.ID)
	if err != nil || !policy.WhitelistEnabled || policy.VerifyRateLimit != 44 {
		t.Fatalf("role-specific policy = %+v %v", policy, err)
	}
	if err := m.RevokeSAServerCredential(ctx, tenant, server.ID, cid, now); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SACredentialBySecretHash(ctx, hash, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked credential err = %v", err)
	}
}
