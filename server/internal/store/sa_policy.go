package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"skin-nexus/server/internal/dbx"
	"skin-nexus/server/internal/dialect"
)

const saPolicySelect = `SELECT p.server_id,p.whitelist_enabled,p.premium_fallback_enabled,
	p.verify_rate_limit,p.verify_burst,p.max_concurrent,p.whitelist_limit,p.event_retention_days
	FROM sa_server_policies p JOIN sa_servers s ON s.id=p.server_id`

func scanSAPolicy(s rowScanner) (SAServerPolicy, error) {
	var p SAServerPolicy
	err := s.Scan(&p.ServerID, &p.WhitelistEnabled, &p.PremiumFallbackEnabled,
		&p.VerifyRateLimit, &p.VerifyBurst, &p.MaxConcurrent, &p.WhitelistLimit, &p.EventRetentionDays)
	if errors.Is(err, sql.ErrNoRows) {
		return SAServerPolicy{}, ErrNotFound
	}
	return p, err
}

func (m *Mysql) SAServerPolicy(ctx context.Context, tenantID, serverID int64) (SAServerPolicy, error) {
	return scanSAPolicy(m.db.QueryRowContext(ctx,
		saPolicySelect+` WHERE s.tenant_id=? AND p.server_id=?`, tenantID, serverID))
}

func (m *Mysql) platformSAServerPolicies(ctx context.Context, serverIDs []int64) (map[int64]SAServerPolicySummary, error) {
	placeholders, args := saIDPlaceholders(serverIDs)
	rows, err := m.db.QueryContext(ctx, saPolicySelect+` WHERE p.server_id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]SAServerPolicySummary, len(serverIDs))
	for rows.Next() {
		policy, err := scanSAPolicy(rows)
		if err != nil {
			return nil, err
		}
		out[policy.ServerID] = SAServerPolicySummary{
			WhitelistEnabled: policy.WhitelistEnabled, PremiumFallbackEnabled: policy.PremiumFallbackEnabled,
			VerifyRateLimit: policy.VerifyRateLimit, VerifyBurst: policy.VerifyBurst, MaxConcurrent: policy.MaxConcurrent,
			WhitelistLimit: policy.WhitelistLimit, EventRetentionDays: policy.EventRetentionDays,
		}
	}
	return out, rows.Err()
}

func saIDPlaceholders(ids []int64) (string, []any) {
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	return strings.Join(placeholders, ","), args
}

// UpdateSAServerOwnerPolicy writes only owner-controlled switches. Nil fields
// are omitted, so neither a stale read nor a partial request can reset platform
// quotas or the other owner switch.
func (m *Mysql) UpdateSAServerOwnerPolicy(ctx context.Context, tenantID, serverID int64, whitelistEnabled, premiumFallbackEnabled *bool) error {
	var sets []string
	var args []any
	if whitelistEnabled != nil {
		sets = append(sets, `whitelist_enabled=?`)
		args = append(args, *whitelistEnabled)
	}
	if premiumFallbackEnabled != nil {
		sets = append(sets, `premium_fallback_enabled=?`)
		args = append(args, *premiumFallbackEnabled)
	}
	return m.updateSAPolicyFields(ctx, tenantID, serverID, sets, args)
}

// UpdateSAServerPlatformPolicy writes only explicitly requested quota fields.
// Owner switches are deliberately absent from this method's surface.
func (m *Mysql) UpdateSAServerPlatformPolicy(ctx context.Context, tenantID, serverID int64,
	verifyRateLimit, verifyBurst, maxConcurrent, whitelistLimit, eventRetentionDays *int) error {
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
	return m.updateSAPolicyFields(ctx, tenantID, serverID, sets, args)
}

func (m *Mysql) updateSAPolicyFields(ctx context.Context, tenantID, serverID int64, sets []string, args []any) error {
	if len(sets) == 0 {
		return nil
	}
	query := `UPDATE sa_server_policies SET ` + strings.Join(sets, ",") +
		` WHERE server_id=? AND server_id IN (SELECT id FROM sa_servers WHERE id=? AND tenant_id=?)`
	args = append(args, serverID, serverID, tenantID)
	res, err := m.db.ExecContext(ctx, query, args...)
	return saUpdateAffected(res, err, func() (bool, error) {
		return m.saExists(ctx, `SELECT 1 FROM sa_server_policies WHERE server_id=? AND server_id IN (SELECT id FROM sa_servers WHERE id=? AND tenant_id=?)`, serverID, serverID, tenantID)
	})
}

// saLockPolicyRow locks the scoped policy row that owns whitelist_limit. The
// SQLite no-op write is the equivalent of FOR UPDATE under its single-writer
// model, and keeps the quota read and insert in one serialized transaction.
func saLockPolicyRow(ctx context.Context, tx *dbx.Tx, tenantID, serverID int64) (int, error) {
	if tx.Engine() == dialect.SQLite {
		if _, err := tx.ExecContext(ctx,
			`UPDATE sa_server_policies SET whitelist_enabled=whitelist_enabled
			 WHERE server_id=? AND server_id IN (SELECT id FROM sa_servers WHERE id=? AND tenant_id=?)`,
			serverID, serverID, tenantID); err != nil {
			return 0, err
		}
	}
	var limit int
	err := tx.QueryRowContext(ctx,
		`SELECT p.whitelist_limit FROM sa_server_policies p JOIN sa_servers s ON s.id=p.server_id
		 WHERE s.tenant_id=? AND p.server_id=?`+tx.Engine().LockSQL(), tenantID, serverID).Scan(&limit)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return limit, err
}

func (m *Mysql) SAWhitelistMember(ctx context.Context, tenantID, serverID, uid int64) (bool, error) {
	var one int
	err := m.db.QueryRowContext(ctx,
		`SELECT 1 FROM sa_server_whitelist w JOIN sa_servers s ON s.id=w.server_id
		 WHERE s.tenant_id=? AND w.server_id=? AND w.uid=?`, tenantID, serverID, uid).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (m *Mysql) SAActiveBan(ctx context.Context, tenantID, serverID, uid int64, now time.Time) (bool, error) {
	var one int
	err := m.db.QueryRowContext(ctx,
		`SELECT 1 FROM sa_server_bans b JOIN sa_servers s ON s.id=b.server_id
		 WHERE s.tenant_id=? AND b.server_id=? AND b.uid=?
		   AND (b.expires_at IS NULL OR b.expires_at>?)`, tenantID, serverID, uid, now.UTC()).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (m *Mysql) AddSAServerWhitelist(ctx context.Context, tenantID, serverID, uid int64, note string, createdBy int64) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	limit, err := saLockPolicyRow(ctx, tx, tenantID, serverID)
	if err != nil {
		return err
	}
	var one int
	err = tx.QueryRowContext(ctx,
		`SELECT 1 FROM sa_server_whitelist w JOIN sa_servers s ON s.id=w.server_id
		 WHERE w.server_id=? AND w.uid=? AND s.tenant_id=?`, serverID, uid, tenantID).Scan(&one)
	if err == nil {
		return ErrDuplicate
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var count int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sa_server_whitelist WHERE server_id=?`, serverID).Scan(&count); err != nil {
		return err
	}
	if count >= limit {
		return ErrQuota
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO sa_server_whitelist (server_id,uid,note,created_by,created_at) VALUES (?,?,?,?,?)`,
		serverID, uid, note, createdBy, m.now().UTC())
	if err != nil {
		if isDupKey(err) {
			return ErrDuplicate
		}
		return err
	}
	return tx.Commit()
}

func scanSAServerWhitelist(s rowScanner) (SAServerWhitelist, error) {
	var v SAServerWhitelist
	err := s.Scan(&v.ID, &v.ServerID, &v.UID, &v.Note, &v.CreatedBy, &v.CreatedAt)
	return v, err
}

func (m *Mysql) ListSAServerWhitelist(ctx context.Context, tenantID, serverID int64) ([]SAServerWhitelist, error) {
	return m.listSAServerWhitelist(ctx, tenantID, serverID, 5000)
}

func (m *Mysql) listSAServerWhitelist(ctx context.Context, tenantID, serverID int64, limit int) ([]SAServerWhitelist, error) {
	q := `SELECT w.id,w.server_id,w.uid,w.note,w.created_by,w.created_at
		 FROM sa_server_whitelist w JOIN sa_servers s ON s.id=w.server_id
		 WHERE s.tenant_id=? AND w.server_id=? ORDER BY w.id DESC`
	args := []any{tenantID, serverID}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := m.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SAServerWhitelist, 0)
	for rows.Next() {
		v, err := scanSAServerWhitelist(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (m *Mysql) RemoveSAServerWhitelist(ctx context.Context, tenantID, serverID, uid int64) error {
	res, err := m.db.ExecContext(ctx,
		`DELETE FROM sa_server_whitelist WHERE server_id=? AND uid=?
		   AND server_id IN (SELECT id FROM sa_servers WHERE id=? AND tenant_id=?)`,
		serverID, uid, serverID, tenantID)
	return saAffected(res, err)
}

func (m *Mysql) AddSAServerBan(ctx context.Context, tenantID, serverID, uid int64, reason string, expiresAt sql.NullTime, createdBy int64) error {
	res, err := m.db.ExecContext(ctx,
		`INSERT INTO sa_server_bans (server_id,uid,reason,expires_at,created_by,created_at)
		 SELECT ?,?,?,?,?,? FROM sa_servers WHERE id=? AND tenant_id=?`,
		serverID, uid, reason, expiresAt, createdBy, m.now().UTC(), serverID, tenantID)
	if err != nil {
		if isDupKey(err) {
			return ErrDuplicate
		}
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}

func scanSAServerBan(s rowScanner) (SAServerBan, error) {
	var v SAServerBan
	err := s.Scan(&v.ID, &v.ServerID, &v.UID, &v.Reason, &v.ExpiresAt, &v.CreatedBy, &v.CreatedAt)
	return v, err
}

func (m *Mysql) ListSAServerBans(ctx context.Context, tenantID, serverID int64) ([]SAServerBan, error) {
	return m.listSAServerBans(ctx, tenantID, serverID, 5000)
}

func (m *Mysql) listSAServerBans(ctx context.Context, tenantID, serverID int64, limit int) ([]SAServerBan, error) {
	q := `SELECT b.id,b.server_id,b.uid,b.reason,b.expires_at,b.created_by,b.created_at
		 FROM sa_server_bans b JOIN sa_servers s ON s.id=b.server_id
		 WHERE s.tenant_id=? AND b.server_id=? ORDER BY b.id DESC`
	args := []any{tenantID, serverID}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := m.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SAServerBan, 0)
	for rows.Next() {
		v, err := scanSAServerBan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (m *Mysql) RemoveSAServerBan(ctx context.Context, tenantID, serverID, uid int64) error {
	res, err := m.db.ExecContext(ctx,
		`DELETE FROM sa_server_bans WHERE server_id=? AND uid=?
		   AND server_id IN (SELECT id FROM sa_servers WHERE id=? AND tenant_id=?)`,
		serverID, uid, serverID, tenantID)
	return saAffected(res, err)
}
