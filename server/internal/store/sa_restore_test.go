package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"skin-nexus/server/internal/dialect"
	"skin-nexus/server/internal/migrate"
)

// TestServerAccessSQLiteRestoreDrill performs a real close/copy/reopen cycle.
// It is deliberately untagged so a normal Go test run exercises persistence,
// not merely migration SQL or an in-memory repository fake.
func TestServerAccessSQLiteRestoreDrill(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.db")
	backupPath := filepath.Join(dir, "backup.db")
	restoredPath := filepath.Join(dir, "restored.db")
	open := func(path string) *Mysql {
		st, err := Open("sqlite", path, nil)
		if err != nil {
			t.Fatalf("open sqlite store: %v", err)
		}
		t.Cleanup(func() { _ = st.Close() })
		if err := migrate.ApplySQLDirTracked(st.DB(), filepath.Join("..", "..", "migrations"), dialect.SQLite); err != nil {
			t.Fatalf("apply sqlite tree: %v", err)
		}
		return st
	}
	copyFile := func(from, to string) {
		data, err := os.ReadFile(from)
		if err != nil {
			t.Fatalf("read %s: %v", filepath.Base(from), err)
		}
		if err := os.WriteFile(to, data, 0o600); err != nil {
			t.Fatalf("write %s: %v", filepath.Base(to), err)
		}
	}

	st := open(sourcePath)
	uid := saSeedUser(t, st)
	tenant, err := st.CreateSATenant(ctx, "restore-drill", 5)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddSATenantMember(ctx, tenant, uid, "owner"); err != nil {
		t.Fatal(err)
	}
	server, err := st.CreateSAServer(ctx, tenant, "srv_restore_drill", "Restore drill")
	if err != nil {
		t.Fatal(err)
	}
	whitelist, fallback := true, false
	rate, burst, concurrent, whitelistLimit, retention := 41, 7, 3, 19, 14
	if err := st.UpdateSAServerOwnerPolicy(ctx, tenant, server.ID, &whitelist, &fallback); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateSAServerPlatformPolicy(ctx, tenant, server.ID, &rate, &burst, &concurrent, &whitelistLimit, &retention); err != nil {
		t.Fatal(err)
	}
	credentialHash := saTestHash("restore-drill-credential")
	credentialID, err := st.CreateSAServerCredential(ctx, tenant, server.ID, SACredential{
		Mode: "header", SecretHash: credentialHash, Scopes: `["verify:join"]`, Status: "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddSAServerWhitelist(ctx, tenant, server.ID, uid, "restore note", uid); err != nil {
		t.Fatal(err)
	}
	banExpiry := time.Date(2031, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := st.AddSAServerBan(ctx, tenant, server.ID, uid, "restore reason", sql.NullTime{Time: banExpiry, Valid: true}, uid); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertSAJoinEvent(ctx, SAJoinEvent{
		TenantID: tenant, ServerID: server.ID, Outcome: "deny", ReasonCode: "not_whitelisted",
		SourceMode: "header", DurationMS: 17, RequestID: "restore-request",
	}); err != nil {
		t.Fatal(err)
	}

	requested := time.Date(2030, 2, 3, 4, 5, 6, 0, time.UTC)
	due := requested.Add(7 * 24 * time.Hour)
	deleting, err := st.RequestSATenantDeletion(ctx, tenant, 7*24*time.Hour, requested)
	if err != nil {
		t.Fatal(err)
	}
	if deleting.Status != "deleting" || deleting.DeletionRequestedAt == nil || deleting.DeletionDueAt == nil ||
		!deleting.DeletionRequestedAt.Equal(requested) || !deleting.DeletionDueAt.Equal(due) {
		t.Fatalf("pre-restore deletion state = %+v", deleting)
	}
	if _, err := st.DB().ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatalf("checkpoint sqlite: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close source sqlite: %v", err)
	}
	copyFile(sourcePath, backupPath)
	for _, path := range []string{sourcePath, sourcePath + "-wal", sourcePath + "-shm"} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("remove original sqlite file: %v", err)
		}
	}
	copyFile(backupPath, restoredPath)
	backupBytes, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	restoredBytes, err := os.ReadFile(restoredPath)
	if err != nil || !bytes.Equal(backupBytes, restoredBytes) {
		t.Fatalf("restored file differs from copied backup: %v", err)
	}

	restored := open(restoredPath)
	tenantRow, err := restored.SATenantByID(ctx, tenant)
	if err != nil {
		t.Fatalf("tenant after restore: %v", err)
	}
	if tenantRow.Status != "deleting" || tenantRow.DeletionRequestedAt == nil || tenantRow.DeletionDueAt == nil ||
		!tenantRow.DeletionRequestedAt.Equal(requested) || !tenantRow.DeletionDueAt.Equal(due) {
		t.Fatalf("restored deletion state = %+v", tenantRow)
	}
	serverRow, err := restored.SAServerByID(ctx, tenant, server.ID)
	if err != nil || serverRow.Status != "suspended" || serverRow.PublicID != "srv_restore_drill" {
		t.Fatalf("restored server = %+v %v", serverRow, err)
	}
	policy, err := restored.SAServerPolicy(ctx, tenant, server.ID)
	if err != nil || !policy.WhitelistEnabled || policy.PremiumFallbackEnabled ||
		policy.VerifyRateLimit != rate || policy.VerifyBurst != burst || policy.MaxConcurrent != concurrent ||
		policy.WhitelistLimit != whitelistLimit || policy.EventRetentionDays != retention {
		t.Fatalf("restored policy = %+v %v", policy, err)
	}
	credentials, err := restored.ListSAServerCredentials(ctx, tenant, server.ID)
	if err != nil || len(credentials) != 1 || credentials[0].ID != credentialID ||
		credentials[0].Mode != "header" || credentials[0].Status != "revoked" {
		t.Fatalf("restored credentials = %+v %v", credentials, err)
	}
	var restoredHash, restoredScopes string
	if err := restored.DB().QueryRowContext(ctx,
		`SELECT secret_hash,scopes FROM sa_server_credentials WHERE id=?`, credentialID).Scan(&restoredHash, &restoredScopes); err != nil {
		t.Fatalf("read restored credential metadata: %v", err)
	}
	if restoredHash != credentialHash || restoredScopes != `["verify:join"]` {
		t.Fatalf("restored credential metadata = hash %q scopes %q", restoredHash, restoredScopes)
	}
	if _, err := restored.SACredentialBySecretHash(ctx, credentialHash, due); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked credential resolved after restore: %v", err)
	}
	whitelistRows, err := restored.ListSAServerWhitelist(ctx, tenant, server.ID)
	if err != nil || len(whitelistRows) != 1 || whitelistRows[0].UID != uid || whitelistRows[0].Note != "restore note" {
		t.Fatalf("restored whitelist = %+v %v", whitelistRows, err)
	}
	bans, err := restored.ListSAServerBans(ctx, tenant, server.ID)
	if err != nil || len(bans) != 1 || bans[0].UID != uid || bans[0].Reason != "restore reason" ||
		!bans[0].ExpiresAt.Valid || !bans[0].ExpiresAt.Time.Equal(banExpiry) {
		t.Fatalf("restored bans = %+v %v", bans, err)
	}
	events, err := restored.ListSAJoinEvents(ctx, tenant, server.ID, time.Now().UTC().Add(-24*time.Hour), time.Now().UTC().Add(24*time.Hour), 10)
	if err != nil || len(events) != 1 || events[0].Outcome != "deny" || events[0].ReasonCode != "not_whitelisted" ||
		events[0].SourceMode != "header" || events[0].DurationMS != 17 || events[0].RequestID != "restore-request" {
		t.Fatalf("restored events = %+v %v", events, err)
	}
	if err := restored.DeleteDueSATenant(ctx, tenant, due.Add(-time.Nanosecond)); !errors.Is(err, ErrDeletionNotDue) {
		t.Fatalf("restored early deletion = %v, want not due", err)
	}
	if _, err := restored.UserByID(ctx, uid); err != nil {
		t.Fatalf("global user after restore: %v", err)
	}
	t.Logf("restore drill: copied_file=true reopened=true tenant_status=%s server_status=%s credentials=%d credential_status=%s whitelist=%d bans=%d events=%d policy=%d/%d/%d/%d/%d credential_lookup=not_found requested_at=%s due_at=%s",
		tenantRow.Status, serverRow.Status, len(credentials), credentials[0].Status, len(whitelistRows), len(bans), len(events),
		policy.VerifyRateLimit, policy.VerifyBurst, policy.MaxConcurrent, policy.WhitelistLimit, policy.EventRetentionDays,
		requested.Format(time.RFC3339), due.Format(time.RFC3339))
}
