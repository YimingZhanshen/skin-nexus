package store

import (
	"context"
	"database/sql"
	"errors"
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

func (m *Mysql) UpdateSAServerPolicy(ctx context.Context, tenantID, serverID int64, p SAServerPolicy) error {
	res, err := m.db.ExecContext(ctx,
		`UPDATE sa_server_policies SET whitelist_enabled=?,premium_fallback_enabled=?,
		 verify_rate_limit=?,verify_burst=?,max_concurrent=?,whitelist_limit=?,event_retention_days=?
		 WHERE server_id=? AND server_id IN (SELECT id FROM sa_servers WHERE id=? AND tenant_id=?)`,
		p.WhitelistEnabled, p.PremiumFallbackEnabled, p.VerifyRateLimit, p.VerifyBurst,
		p.MaxConcurrent, p.WhitelistLimit, p.EventRetentionDays, serverID, serverID, tenantID)
	return saUpdateAffected(res, err, func() (bool, error) {
		return m.saExists(ctx, `SELECT 1 FROM sa_server_policies WHERE server_id=? AND server_id IN (SELECT id FROM sa_servers WHERE id=? AND tenant_id=?)`, serverID, serverID, tenantID)
	})
}

func (m *Mysql) AddSAServerWhitelist(ctx context.Context, tenantID, serverID, uid int64, note string, createdBy int64) error {
	res, err := m.db.ExecContext(ctx,
		`INSERT INTO sa_server_whitelist (server_id,uid,note,created_by,created_at)
		 SELECT ?,?,?,?,? FROM sa_servers s JOIN sa_server_policies p ON p.server_id=s.id
		 WHERE s.id=? AND s.tenant_id=? AND (SELECT COUNT(*) FROM sa_server_whitelist WHERE server_id=s.id)<p.whitelist_limit`,
		serverID, uid, note, createdBy, m.now().UTC(), serverID, tenantID)
	if err != nil {
		if isDupKey(err) {
			return ErrDuplicate
		}
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		var one int
		if scanErr := m.db.QueryRowContext(ctx,
			`SELECT 1 FROM sa_server_whitelist w JOIN sa_servers s ON s.id=w.server_id
			 WHERE w.server_id=? AND w.uid=? AND s.tenant_id=?`, serverID, uid, tenantID).Scan(&one); scanErr == nil {
			return ErrDuplicate
		} else if !errors.Is(scanErr, sql.ErrNoRows) {
			return scanErr
		}
		if scanErr := m.db.QueryRowContext(ctx,
			`SELECT 1 FROM sa_servers WHERE id=? AND tenant_id=?`, serverID, tenantID).Scan(&one); scanErr != nil {
			if errors.Is(scanErr, sql.ErrNoRows) {
				return ErrNotFound
			}
			return scanErr
		}
		return ErrQuota
	}
	return nil
}

func (m *Mysql) ListSAServerWhitelist(ctx context.Context, tenantID, serverID int64) ([]SAServerWhitelist, error) {
	rows, err := m.db.QueryContext(ctx,
		`SELECT w.id,w.server_id,w.uid,w.note,w.created_by,w.created_at
		 FROM sa_server_whitelist w JOIN sa_servers s ON s.id=w.server_id
		 WHERE s.tenant_id=? AND w.server_id=? ORDER BY w.id DESC LIMIT 5000`, tenantID, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SAServerWhitelist, 0)
	for rows.Next() {
		var v SAServerWhitelist
		if err := rows.Scan(&v.ID, &v.ServerID, &v.UID, &v.Note, &v.CreatedBy, &v.CreatedAt); err != nil {
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

func (m *Mysql) ListSAServerBans(ctx context.Context, tenantID, serverID int64) ([]SAServerBan, error) {
	rows, err := m.db.QueryContext(ctx,
		`SELECT b.id,b.server_id,b.uid,b.reason,b.expires_at,b.created_by,b.created_at
		 FROM sa_server_bans b JOIN sa_servers s ON s.id=b.server_id
		 WHERE s.tenant_id=? AND b.server_id=? ORDER BY b.id DESC LIMIT 5000`, tenantID, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SAServerBan, 0)
	for rows.Next() {
		var v SAServerBan
		if err := rows.Scan(&v.ID, &v.ServerID, &v.UID, &v.Reason, &v.ExpiresAt, &v.CreatedBy, &v.CreatedAt); err != nil {
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
