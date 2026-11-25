//go:build integration

package store

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"skin-nexus/server/internal/dialect"
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

func TestIntegrationServerAccessAtomicTenantOwner(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	uid := seedUser(t, m)
	tenant, owner, err := m.CreateSATenantWithOwner(ctx, "atomic-integration", 7, uid)
	if err != nil {
		t.Fatal(err)
	}
	if tenant.Status != "active" || tenant.ServerLimit != 7 || owner.TenantID != tenant.ID || owner.UID != uid || owner.Role != "owner" {
		t.Fatalf("atomic creation = %+v owner=%+v", tenant, owner)
	}
	_, beforeTotal, err := m.PlatformSATenants(ctx, "", 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.CreateSATenantWithOwner(ctx, "duplicate-integration", 2, uid); !errors.Is(err, ErrForbidden) {
		t.Fatalf("second active owner err = %v, want forbidden", err)
	}
	if _, _, err := m.CreateSATenantWithOwner(ctx, "missing-integration", 2, 999999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid owner err = %v, want not found", err)
	}
	_, afterTotal, err := m.PlatformSATenants(ctx, "", 1, 20)
	if err != nil || afterTotal != beforeTotal {
		t.Fatalf("tenant total after rollbacks = %d, before=%d, err=%v", afterTotal, beforeTotal, err)
	}
}

func TestIntegrationServerAccessAuditedOwnerUpdate(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	actor := seedUser(t, m)
	tenant, err := m.CreateSATenant(ctx, "audited-owner-update", 2)
	if err != nil {
		t.Fatal(err)
	}
	server, err := m.CreateSAServer(ctx, tenant, "srv_audited_owner_update", "before")
	if err != nil {
		t.Fatal(err)
	}
	name, status, whitelist, fallback := "after", "disabled", true, false
	got, err := m.UpdateSAServerOwnerAudited(ctx, tenant, server.ID, SAOwnerServerPatch{
		Name: &name, Status: &status, WhitelistEnabled: &whitelist, PremiumFallbackEnabled: &fallback,
	}, actor)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != server.ID || got.TenantID != tenant || got.Name != "after" || got.Status != "disabled" {
		t.Fatalf("audited owner update = %+v", got)
	}
	policy, err := m.SAServerPolicy(ctx, tenant, server.ID)
	if err != nil || !policy.WhitelistEnabled || policy.PremiumFallbackEnabled {
		t.Fatalf("audited owner policy = %+v %v", policy, err)
	}
	audit, total, err := m.PlatformServerAccessAudit(ctx, "serveraccess_server_update", 1, 20)
	if err != nil || total != 1 || len(audit) != 1 || audit[0]["actorUid"] != actor {
		t.Fatalf("owner update audit = %+v total=%d err=%v", audit, total, err)
	}
}

func TestIntegrationServerAccessAuditedOwnerUpdateRollsBackAuditFailure(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	actor := seedUser(t, m)
	tenant, err := m.CreateSATenant(ctx, "audited-owner-rollback", 2)
	if err != nil {
		t.Fatal(err)
	}
	server, err := m.CreateSAServer(ctx, tenant, "srv_audited_owner_rollback", "before")
	if err != nil {
		t.Fatal(err)
	}
	saFailAuditAction(t, m, "serveraccess_server_update")
	name, status, whitelist := "after", "disabled", true
	if _, err := m.UpdateSAServerOwnerAudited(ctx, tenant, server.ID, SAOwnerServerPatch{
		Name: &name, Status: &status, WhitelistEnabled: &whitelist,
	}, actor); err == nil {
		t.Fatal("owner update unexpectedly succeeded without audit")
	}
	got, err := m.SAServerByID(ctx, tenant, server.ID)
	if err != nil || got.Name != "before" || got.Status != "active" {
		t.Fatalf("server survived audit failure: %+v %v", got, err)
	}
	policy, err := m.SAServerPolicy(ctx, tenant, server.ID)
	if err != nil || policy.WhitelistEnabled {
		t.Fatalf("policy survived audit failure: %+v %v", policy, err)
	}
	_, total, err := m.PlatformServerAccessAudit(ctx, "serveraccess_server_update", 1, 20)
	if err != nil || total != 0 {
		t.Fatalf("failed owner audit rows = %d, err=%v", total, err)
	}
}

func TestIntegrationServerAccessAuditedTenantCreateRollsBackAuditFailure(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	actor := seedUser(t, m)
	ownerUID := seedUser(t, m)
	saFailAuditAction(t, m, "serveraccess_tenant_create")
	if _, _, err := m.CreateSATenantWithOwnerAudited(ctx, "unaudited-tenant", 7, ownerUID, actor); err == nil {
		t.Fatal("tenant creation unexpectedly succeeded without audit")
	}
	rows, total, err := m.PlatformSATenants(ctx, "", 1, 20)
	if err != nil || total != 0 || len(rows) != 0 {
		t.Fatalf("tenant survived audit failure: %+v total=%d err=%v", rows, total, err)
	}
	var memberships int
	if err := m.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sa_tenant_members WHERE uid=?`, ownerUID).Scan(&memberships); err != nil {
		t.Fatal(err)
	}
	if memberships != 0 {
		t.Fatalf("owner membership survived audit failure: %d", memberships)
	}
	_, auditTotal, err := m.PlatformServerAccessAudit(ctx, "serveraccess_tenant_create", 1, 20)
	if err != nil || auditTotal != 0 {
		t.Fatalf("failed tenant audit rows = %d, err=%v", auditTotal, err)
	}
}

func saFailAuditAction(t *testing.T, m *Mysql, action string) {
	t.Helper()
	var statements []string
	switch m.Engine() {
	case dialect.SQLite:
		statements = []string{`CREATE TRIGGER fail_audit_action BEFORE INSERT ON audit_log WHEN NEW.action='` + action + `'
			BEGIN SELECT RAISE(ABORT,'forced audit failure'); END`}
	case dialect.MySQL:
		statements = []string{`CREATE TRIGGER fail_audit_action BEFORE INSERT ON audit_log FOR EACH ROW
			BEGIN IF NEW.action='` + action + `' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='forced audit failure'; END IF; END`}
	case dialect.Postgres:
		statements = []string{
			`CREATE OR REPLACE FUNCTION fail_audit_action() RETURNS trigger LANGUAGE plpgsql AS $$
				BEGIN IF NEW.action='` + action + `' THEN RAISE EXCEPTION 'forced audit failure'; END IF; RETURN NEW; END; $$`,
			`CREATE TRIGGER fail_audit_action BEFORE INSERT ON audit_log FOR EACH ROW EXECUTE FUNCTION fail_audit_action()`,
		}
	default:
		t.Fatalf("unsupported integration engine %q", m.Engine())
	}
	for _, statement := range statements {
		if _, err := m.DB().ExecContext(context.Background(), statement); err != nil {
			t.Fatal(err)
		}
	}
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
			_, _, errs[i] = m.AdmitSAQuota(ctx, server.ID, now, "verify", workers, now)
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
