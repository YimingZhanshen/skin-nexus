package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"skin-nexus/server/internal/dbx"
)

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
