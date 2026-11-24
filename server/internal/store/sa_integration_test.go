//go:build integration

package store

import (
	"context"
	"database/sql"
	"errors"
	"sync"
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

func TestIntegrationServerAccessVerificationPersistence(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	tenant, err := m.CreateSATenant(ctx, "verify-events", 2)
	if err != nil {
		t.Fatal(err)
	}
	other, err := m.CreateSATenant(ctx, "verify-events-other", 2)
	if err != nil {
		t.Fatal(err)
	}
	server, err := m.CreateSAServer(ctx, tenant, "srv_verify_events", "verify events")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Minute)
	event := SAJoinEvent{TenantID: tenant, ServerID: server.ID, JoinSessionID: sql.NullInt64{Int64: 4242, Valid: true},
		UUID: "0123456789abcdef0123456789abcdef", PlayerName: "Alice", Outcome: "allow", ReasonCode: "local_session",
		SourceMode: "url_secret", DurationMS: 3, RequestID: "integration-request"}
	if err := m.InsertSAJoinEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := m.InsertSAJoinEvent(ctx, event); err != nil {
		t.Fatalf("event replay: %v", err)
	}
	events, err := m.ListSAJoinEvents(ctx, tenant, server.ID, now.Add(-time.Minute), now.Add(time.Minute), 20)
	if err != nil || len(events) != 1 || events[0].TrustLevel() != "possessed_credential" {
		t.Fatalf("events = %+v, %v", events, err)
	}
	if err := m.InsertSAJoinEvent(ctx, SAJoinEvent{TenantID: other, ServerID: server.ID, PlayerName: "Cross", Outcome: "deny", ReasonCode: "test", SourceMode: "header"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant event err = %v, want not found", err)
	}
	const workers = 6
	start := make(chan struct{})
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = m.IncrementSAQuota(ctx, server.ID, now, "verify", now.Add(time.Minute))
		}(i)
	}
	close(start)
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got, err := m.ReadSAQuota(ctx, server.ID, now, "verify"); err != nil || got != workers {
		t.Fatalf("quota = %d, %v, want %d", got, err, workers)
	}
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
