package store

import (
	"context"
	"time"

	"skin-nexus/server/internal/dbx"
	"skin-nexus/server/internal/dialect"
)

const DefaultSATenantDeletionGrace = 7 * 24 * time.Hour

// RequestSATenantDeletion freezes the grace deadline and suspends every server
// and credential in one transaction. A replay returns the original deadline and
// never extends it.
func (m *Mysql) RequestSATenantDeletion(ctx context.Context, tenantID int64, grace time.Duration, now time.Time) (SATenant, error) {
	if grace <= 0 {
		grace = DefaultSATenantDeletionGrace
	}
	now = now.UTC()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return SATenant{}, err
	}
	defer tx.Rollback()
	tenant, err := saLockTenantLifecycleRow(ctx, tx, tenantID)
	if err != nil {
		return SATenant{}, err
	}
	if tenant.Status == "deleting" {
		if tenant.DeletionRequestedAt == nil || tenant.DeletionDueAt == nil {
			return SATenant{}, ErrDeletionNotDue
		}
		return tenant, nil
	}
	if tenant.Status != "active" && tenant.Status != "suspended" {
		return SATenant{}, ErrForbidden
	}
	due := now.Add(grace)
	if _, err := tx.ExecContext(ctx,
		`UPDATE sa_tenants SET status='deleting',deletion_requested_at=?,deletion_due_at=? WHERE id=?`,
		now, due, tenantID); err != nil {
		return SATenant{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE sa_servers SET status='suspended' WHERE tenant_id=?`, tenantID); err != nil {
		return SATenant{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE sa_server_credentials SET status='revoked'
		 WHERE server_id IN (SELECT id FROM sa_servers WHERE tenant_id=?)`, tenantID); err != nil {
		return SATenant{}, err
	}
	if err := tx.Commit(); err != nil {
		return SATenant{}, err
	}
	tenant.Status = "deleting"
	requested := now
	tenant.DeletionRequestedAt = &requested
	tenant.DeletionDueAt = &due
	return tenant, nil
}

// DeleteDueSATenant serializes the grace check with the physical delete. The
// shared delete helper preserves the existing sa_* cascade and never targets a
// global identity table.
func (m *Mysql) DeleteDueSATenant(ctx context.Context, tenantID int64, now time.Time) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tenant, err := saLockTenantLifecycleRow(ctx, tx, tenantID)
	if err != nil {
		return err
	}
	if tenant.Status != "deleting" || tenant.DeletionDueAt == nil || now.UTC().Before(*tenant.DeletionDueAt) {
		return ErrDeletionNotDue
	}
	if err := deleteSATenantTx(ctx, tx, tenantID); err != nil {
		return err
	}
	return tx.Commit()
}

func saLockTenantLifecycleRow(ctx context.Context, tx *dbx.Tx, tenantID int64) (SATenant, error) {
	if tx.Engine() == dialect.SQLite {
		if _, err := tx.ExecContext(ctx,
			`UPDATE sa_tenants SET name=name WHERE id=? AND deleted_at IS NULL`, tenantID); err != nil {
			return SATenant{}, err
		}
	}
	return scanSATenant(tx.QueryRowContext(ctx,
		`SELECT id,name,status,server_limit,created_at,deleted_at,deletion_requested_at,deletion_due_at
		 FROM sa_tenants WHERE id=? AND deleted_at IS NULL`+tx.Engine().LockSQL(), tenantID))
}

func deleteSATenantTx(ctx context.Context, tx *dbx.Tx, tenantID int64) error {
	res, err := tx.ExecContext(ctx, `DELETE FROM sa_tenants WHERE id=?`, tenantID)
	return saAffected(res, err)
}
