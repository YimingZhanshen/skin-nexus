//go:build integration

package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestIntegrationServerAccessDeletionSerializesCredentialWrites(t *testing.T) {
	saExerciseDeletionCredentialRace(t, openTestStore(t))
}

func TestIntegrationServerAccessDeletionReplayRejectsCredentialWrites(t *testing.T) {
	saExerciseDeletionCredentialReplay(t, openTestStore(t))
}

func TestIntegrationServerAccessLifecycleMigrationAndScopedCascade(t *testing.T) {
	m := openTestStore(t)
	ctx := context.Background()
	uid := seedUser(t, m)
	const tenantName = "lifecycle-integration"
	tenant, err := m.CreateSATenant(ctx, tenantName, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.AddSATenantMember(ctx, tenant, uid, "owner"); err != nil {
		t.Fatal(err)
	}
	server, err := m.CreateSAServer(ctx, tenant, "srv_lifecycle_integration", "lifecycle")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := m.CreateSAServerCredential(ctx, tenant, server.ID, SACredential{
		Mode: "url_secret", SecretHash: saTestHash("lifecycle-secret"), Scopes: `["verify:join"]`, Status: "active",
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.AddSAServerWhitelist(ctx, tenant, server.ID, uid, "note", uid); err != nil {
		t.Fatal(err)
	}
	if err := m.AddSAServerBan(ctx, tenant, server.ID, uid, "reason", sql.NullTime{}, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateSATenantInvite(ctx, tenant, "lifecycle@example.com", "viewer", saTestHash("lifecycle-invite"), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := m.InsertSAJoinEvent(ctx, SAJoinEvent{
		TenantID: tenant, ServerID: server.ID, Outcome: "allow", ReasonCode: "local_session",
		SourceMode: "url_secret", RequestID: "lifecycle-request",
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.AdmitSAQuota(ctx, server.ID, now, "verify", 10, now); err != nil {
		t.Fatal(err)
	}
	playerID, _, err := m.CreatePlayer(ctx, uid, "LifecyclePlayer")
	if err != nil {
		t.Fatal(err)
	}
	textureID, err := m.AddTexture(ctx, uid, "skin", saTestHash("lifecycle-texture"), "default", TextureMeta{})
	if err != nil {
		t.Fatal(err)
	}
	due := now.Add(7 * 24 * time.Hour)
	if _, err := m.RequestSATenantDeletionConfirmed(ctx, tenant, tenantName+" ", 7*24*time.Hour, now); !errors.Is(err, ErrConfirmationMismatch) {
		t.Fatalf("mismatched tenant deletion request = %v, want mismatch", err)
	}
	if _, err := m.RequestSATenantDeletionConfirmed(ctx, tenant, tenantName, 7*24*time.Hour, now); err != nil {
		t.Fatalf("request tenant deletion: %v", err)
	}
	if _, err := m.RequestSATenantDeletionConfirmed(ctx, tenant, "", time.Hour, now.Add(time.Hour)); !errors.Is(err, ErrConfirmationMismatch) {
		t.Fatalf("mismatched delete request replay = %v, want mismatch", err)
	}
	replay, err := m.RequestSATenantDeletionConfirmed(ctx, tenant, tenantName, time.Hour, now.Add(time.Hour))
	if err != nil || replay.DeletionDueAt == nil || !replay.DeletionDueAt.Equal(due) {
		t.Fatalf("delete request replay = %+v %v, want original due %s", replay, err, due.Format(time.RFC3339))
	}
	const action = "serveraccess_tenant_delete"
	detail := map[string]any{"tenantId": tenant}
	if err := m.DeleteDueSATenantConfirmedAudited(ctx, tenant, "Lifecycle-integration", due.Add(-time.Nanosecond), uid, action, detail); !errors.Is(err, ErrConfirmationMismatch) {
		t.Fatalf("mismatched early tenant delete = %v, want mismatch", err)
	}
	if err := m.DeleteDueSATenantConfirmedAudited(ctx, tenant, tenantName, due.Add(-time.Nanosecond), uid, action, detail); !errors.Is(err, ErrDeletionNotDue) {
		t.Fatalf("early tenant delete = %v, want not due", err)
	}
	if err := m.DeleteDueSATenantConfirmedAudited(ctx, tenant, tenantName+" ", due, uid, action, detail); !errors.Is(err, ErrConfirmationMismatch) {
		t.Fatalf("mismatched due tenant delete = %v, want mismatch", err)
	}
	if _, err := m.SATenantByID(ctx, tenant); err != nil {
		t.Fatalf("mismatched delete removed tenant: %v", err)
	}
	if err := m.DeleteDueSATenantConfirmedAudited(ctx, tenant, tenantName, due, uid, action, detail); err != nil {
		t.Fatalf("delete due tenant: %v", err)
	}
	for _, query := range []struct {
		table string
		where string
		arg   any
	}{
		{"sa_tenants", "id=?", tenant},
		{"sa_tenant_members", "tenant_id=?", tenant},
		{"sa_tenant_invites", "tenant_id=?", tenant},
		{"sa_servers", "tenant_id=?", tenant},
		{"sa_server_policies", "server_id=?", server.ID},
		{"sa_server_credentials", "server_id=?", server.ID},
		{"sa_server_whitelist", "server_id=?", server.ID},
		{"sa_server_bans", "server_id=?", server.ID},
		{"sa_join_events", "tenant_id=?", tenant},
		{"sa_quota_usage", "server_id=?", server.ID},
	} {
		var count int
		if err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+query.table+` WHERE `+query.where, query.arg).Scan(&count); err != nil || count != 0 {
			t.Errorf("%s rows after tenant delete = %d, err %v; want 0", query.table, count, err)
		}
	}
	if _, err := m.UserByID(ctx, uid); err != nil {
		t.Fatalf("global user removed: %v", err)
	}
	if _, err := m.PlayerByID(ctx, playerID); err != nil {
		t.Fatalf("global player removed: %v", err)
	}
	if _, _, _, err := m.TextureByTID(ctx, textureID); err != nil {
		t.Fatalf("global texture removed: %v", err)
	}
}
