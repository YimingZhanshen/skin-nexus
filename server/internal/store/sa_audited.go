package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"skin-nexus/server/internal/dbx"
)

// CreateSATenantWithOwnerAudited publishes the tenant and first owner only when
// their create audit can be committed in the same transaction.
func (m *Mysql) CreateSATenantWithOwnerAudited(ctx context.Context, name string, serverLimit int, ownerUID, actorUID int64) (SATenant, SATenantMember, error) {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return SATenant{}, SATenantMember{}, err
	}
	defer tx.Rollback()
	tenant, owner, err := m.createSATenantWithOwnerTx(ctx, tx, name, serverLimit, ownerUID)
	if err != nil {
		return SATenant{}, SATenantMember{}, err
	}
	if err := insertAuditTx(ctx, tx, actorUID, "serveraccess_tenant_create",
		map[string]any{"tenantId": tenant.ID, "ownerUid": owner.UID, "serverLimit": tenant.ServerLimit}, m.now().UTC()); err != nil {
		return SATenant{}, SATenantMember{}, err
	}
	if err := tx.Commit(); err != nil {
		return SATenant{}, SATenantMember{}, err
	}
	return tenant, owner, nil
}

// UpdateSAServerOwnerAudited applies sparse server and owner-policy fields and
// appends the existing serveraccess_server_update audit in one transaction.
// Locks are always acquired tenant -> server -> policy. A requested status
// transition cannot overwrite a platform suspension; name-only and policy-only
// patches retain the established suspended-server behavior.
func (m *Mysql) UpdateSAServerOwnerAudited(ctx context.Context, tenantID, serverID int64, patch SAOwnerServerPatch, actorUID int64) (SAServer, error) {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return SAServer{}, err
	}
	defer tx.Rollback()
	tenantStatus, _, err := saLockTenantRow(ctx, tx, tenantID)
	if err != nil {
		return SAServer{}, err
	}
	if tenantStatus == "deleting" {
		return SAServer{}, ErrDeletionInProgress
	}
	if tenantStatus != "active" {
		return SAServer{}, ErrNotFound
	}
	server, err := saLockSAServerRow(ctx, tx, tenantID, serverID)
	if err != nil {
		return SAServer{}, err
	}
	if patch.Status != nil && server.Status == "suspended" {
		return SAServer{}, ErrForbidden
	}
	if _, err := saLockPolicyRow(ctx, tx, tenantID, serverID); err != nil {
		return SAServer{}, err
	}
	serverSets, serverArgs := saOwnerServerFields(patch)
	if len(serverSets) > 0 {
		query := `UPDATE sa_servers SET ` + strings.Join(serverSets, ",") + ` WHERE tenant_id=? AND id=?`
		args := append(serverArgs, tenantID, serverID)
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return SAServer{}, err
		}
	}
	policySets, policyArgs := saOwnerPolicyFields(patch)
	if len(policySets) > 0 {
		query := `UPDATE sa_server_policies SET ` + strings.Join(policySets, ",") + ` WHERE server_id=?`
		args := append(policyArgs, serverID)
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return SAServer{}, err
		}
	}
	server, err = scanSAServer(tx.QueryRowContext(ctx,
		`SELECT id,tenant_id,public_id,name,status,source_mode,created_at FROM sa_servers WHERE tenant_id=? AND id=?`,
		tenantID, serverID))
	if err != nil {
		return SAServer{}, err
	}
	if err := insertAuditTx(ctx, tx, actorUID, "serveraccess_server_update",
		map[string]any{"tenantId": tenantID, "serverId": serverID, "status": server.Status}, m.now().UTC()); err != nil {
		return SAServer{}, err
	}
	if err := tx.Commit(); err != nil {
		return SAServer{}, err
	}
	return server, nil
}

func saOwnerServerFields(patch SAOwnerServerPatch) ([]string, []any) {
	var sets []string
	var args []any
	if patch.Name != nil {
		sets = append(sets, `name=?`)
		args = append(args, *patch.Name)
	}
	if patch.Status != nil {
		sets = append(sets, `status=?`)
		args = append(args, *patch.Status)
	}
	return sets, args
}

func saOwnerPolicyFields(patch SAOwnerServerPatch) ([]string, []any) {
	var sets []string
	var args []any
	if patch.WhitelistEnabled != nil {
		sets = append(sets, `whitelist_enabled=?`)
		args = append(args, *patch.WhitelistEnabled)
	}
	if patch.PremiumFallbackEnabled != nil {
		sets = append(sets, `premium_fallback_enabled=?`)
		args = append(args, *patch.PremiumFallbackEnabled)
	}
	return sets, args
}

func (m *Mysql) UpdateSATenantPlatformAudited(ctx context.Context, tenantID int64, status string, serverLimit *int, actorUID int64) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := m.now().UTC()
	if status != "" {
		if err := updateSATenantStatusTx(ctx, tx, tenantID, status); err != nil {
			return err
		}
		if err := insertAuditTx(ctx, tx, actorUID, "serveraccess_tenant_status_update",
			map[string]any{"tenantId": tenantID, "status": status}, now); err != nil {
			return err
		}
	}
	if serverLimit != nil {
		res, err := tx.ExecContext(ctx, `UPDATE sa_tenants SET server_limit=? WHERE id=? AND deleted_at IS NULL`, *serverLimit, tenantID)
		if err := saUpdateAffected(res, err, func() (bool, error) {
			return saTxExists(ctx, tx, `SELECT 1 FROM sa_tenants WHERE id=? AND deleted_at IS NULL`, tenantID)
		}); err != nil {
			return err
		}
		if err := insertAuditTx(ctx, tx, actorUID, "serveraccess_tenant_quota_update",
			map[string]any{"tenantId": tenantID, "serverLimit": *serverLimit}, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (m *Mysql) UpdateSAServerPlatformAudited(ctx context.Context, tenantID, serverID int64, name, status string,
	verifyRateLimit, verifyBurst, maxConcurrent, whitelistLimit, eventRetentionDays *int, actorUID int64) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := m.now().UTC()
	if status != "" {
		if err := updateSAServerTx(ctx, tx, tenantID, serverID, name, status); err != nil {
			return err
		}
		if err := insertAuditTx(ctx, tx, actorUID, "serveraccess_server_status_update",
			map[string]any{"tenantId": tenantID, "serverId": serverID, "status": status}, now); err != nil {
			return err
		}
	}
	sets, args := saPlatformPolicyFields(verifyRateLimit, verifyBurst, maxConcurrent, whitelistLimit, eventRetentionDays)
	if len(sets) > 0 {
		query := `UPDATE sa_server_policies SET ` + strings.Join(sets, ",") +
			` WHERE server_id=? AND server_id IN (SELECT id FROM sa_servers WHERE id=? AND tenant_id=?)`
		args = append(args, serverID, serverID, tenantID)
		res, err := tx.ExecContext(ctx, query, args...)
		if err := saUpdateAffected(res, err, func() (bool, error) {
			return saTxExists(ctx, tx, `SELECT 1 FROM sa_server_policies WHERE server_id=? AND server_id IN (SELECT id FROM sa_servers WHERE id=? AND tenant_id=?)`, serverID, serverID, tenantID)
		}); err != nil {
			return err
		}
		if err := insertAuditTx(ctx, tx, actorUID, "serveraccess_server_quota_update",
			map[string]any{"tenantId": tenantID, "serverId": serverID}, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (m *Mysql) SetOptionAudited(ctx context.Context, key, value string, actorUID int64, action string, detail any) error {
	return m.withSAAudit(ctx, actorUID, action, detail, func(tx *dbx.Tx) error {
		query, err := m.db.Dialect().Upsert("options", []string{"k", "v"}, []string{"k"}, []string{"v"})
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, query, key, value)
		return err
	})
}

func saPlatformPolicyFields(verifyRateLimit, verifyBurst, maxConcurrent, whitelistLimit, eventRetentionDays *int) ([]string, []any) {
	var sets []string
	var args []any
	for _, field := range []struct {
		name  string
		value *int
	}{
		{"verify_rate_limit", verifyRateLimit},
		{"verify_burst", verifyBurst},
		{"max_concurrent", maxConcurrent},
		{"whitelist_limit", whitelistLimit},
		{"event_retention_days", eventRetentionDays},
	} {
		if field.value != nil {
			sets = append(sets, field.name+`=?`)
			args = append(args, *field.value)
		}
	}
	return sets, args
}

func saTxExists(ctx context.Context, tx *dbx.Tx, query string, args ...any) (bool, error) {
	var one int
	err := tx.QueryRowContext(ctx, query, args...).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
