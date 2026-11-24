package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const saCredentialSelect = `SELECT c.id,s.tenant_id,c.server_id,c.mode,c.secret_hash,c.scopes,c.status,c.expires_at,c.last_used_at,c.created_at
	FROM sa_server_credentials c JOIN sa_servers s ON s.id=c.server_id`

func scanSACredential(s rowScanner) (SACredential, error) {
	var v SACredential
	err := s.Scan(&v.ID, &v.TenantID, &v.ServerID, &v.Mode, &v.SecretHash, &v.Scopes, &v.Status, &v.ExpiresAt, &v.LastUsedAt, &v.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return SACredential{}, ErrNotFound
	}
	return v, err
}

func (m *Mysql) CreateSAServerCredential(ctx context.Context, tenantID, serverID int64, c SACredential) (int64, error) {
	id, err := m.db.InsertReturningID(ctx,
		`INSERT INTO sa_server_credentials (server_id,mode,secret_hash,scopes,status,expires_at,created_at)
		 SELECT ?,?,?,?,?,?,? FROM sa_servers WHERE id=? AND tenant_id=?`,
		serverID, c.Mode, c.SecretHash, c.Scopes, c.Status, c.ExpiresAt, m.now().UTC(), serverID, tenantID)
	if id == 0 && err == nil {
		return 0, ErrNotFound
	}
	if isDupKey(err) {
		return 0, ErrDuplicate
	}
	return id, err
}

func (m *Mysql) ListSAServerCredentials(ctx context.Context, tenantID, serverID int64) ([]SACredential, error) {
	rows, err := m.db.QueryContext(ctx,
		saCredentialSelect+` WHERE s.tenant_id=? AND c.server_id=? ORDER BY c.id DESC`,
		tenantID, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SACredential, 0)
	for rows.Next() {
		v, err := scanSACredential(rows)
		if err != nil {
			return nil, err
		}
		v.SecretHash = ""
		v.TenantID = tenantID
		out = append(out, v)
	}
	return out, rows.Err()
}

// SACredentialBySecretHash is the machine-authentication lookup, not an owner
// API. Tenant/server are derived from the credential row and active registry.
func (m *Mysql) SACredentialBySecretHash(ctx context.Context, secretHash string, now time.Time) (SACredential, error) {
	v, err := scanSACredential(m.db.QueryRowContext(ctx,
		saCredentialSelect+` JOIN sa_tenants t ON t.id=s.tenant_id
		 WHERE c.secret_hash=? AND c.status='active' AND (c.expires_at IS NULL OR c.expires_at>?)
		   AND s.status='active' AND t.status='active' AND t.deleted_at IS NULL LIMIT 1`,
		secretHash, now.UTC()))
	if err != nil {
		return SACredential{}, err
	}
	return v, nil
}

func (m *Mysql) RotateSAServerCredential(ctx context.Context, tenantID, serverID, oldID int64, fresh SACredential, overlap time.Duration, now time.Time) (int64, error) {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`UPDATE sa_server_credentials SET expires_at=CASE
		   WHEN expires_at IS NULL OR expires_at>? THEN ? ELSE expires_at END
		 WHERE id=? AND server_id=? AND status='active'
		   AND server_id IN (SELECT id FROM sa_servers WHERE id=? AND tenant_id=?)`,
		now.Add(overlap).UTC(), now.Add(overlap).UTC(), oldID, serverID, serverID, tenantID)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return 0, ErrNotFound
	}
	id, err := tx.InsertReturningID(ctx,
		`INSERT INTO sa_server_credentials (server_id,mode,secret_hash,scopes,status,expires_at,created_at)
		 SELECT ?,?,?,?,?,?,? FROM sa_servers WHERE id=? AND tenant_id=?`,
		serverID, fresh.Mode, fresh.SecretHash, fresh.Scopes, fresh.Status, fresh.ExpiresAt, now.UTC(), serverID, tenantID)
	if id == 0 && err == nil {
		return 0, ErrNotFound
	}
	if isDupKey(err) {
		return 0, ErrDuplicate
	}
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

func (m *Mysql) RevokeSAServerCredential(ctx context.Context, tenantID, serverID, credentialID int64, now time.Time) error {
	res, err := m.db.ExecContext(ctx,
		`UPDATE sa_server_credentials SET status='revoked' WHERE id=? AND server_id=?
		   AND server_id IN (SELECT id FROM sa_servers WHERE id=? AND tenant_id=?)`,
		credentialID, serverID, serverID, tenantID)
	return saUpdateAffected(res, err, func() (bool, error) {
		return m.saExists(ctx, `SELECT 1 FROM sa_server_credentials WHERE id=? AND server_id=? AND server_id IN (SELECT id FROM sa_servers WHERE id=? AND tenant_id=?)`, credentialID, serverID, serverID, tenantID)
	})
}

func (m *Mysql) SetSAServerCredentialStatus(ctx context.Context, tenantID, serverID, credentialID int64, status string) error {
	res, err := m.db.ExecContext(ctx,
		`UPDATE sa_server_credentials SET status=? WHERE id=? AND server_id=?
		   AND server_id IN (SELECT id FROM sa_servers WHERE id=? AND tenant_id=?)`,
		status, credentialID, serverID, serverID, tenantID)
	return saUpdateAffected(res, err, func() (bool, error) {
		return m.saExists(ctx, `SELECT 1 FROM sa_server_credentials WHERE id=? AND server_id=? AND server_id IN (SELECT id FROM sa_servers WHERE id=? AND tenant_id=?)`, credentialID, serverID, serverID, tenantID)
	})
}

func (m *Mysql) TouchSACredential(ctx context.Context, tenantID, serverID, credentialID int64, now time.Time) error {
	res, err := m.db.ExecContext(ctx,
		`UPDATE sa_server_credentials SET last_used_at=?
		 WHERE id=? AND server_id=? AND status='active' AND (expires_at IS NULL OR expires_at>?)
		   AND server_id IN (
		     SELECT s.id FROM sa_servers s JOIN sa_tenants t ON t.id=s.tenant_id
		      WHERE s.id=? AND s.tenant_id=? AND s.status='active' AND t.status='active' AND t.deleted_at IS NULL
		   )`,
		now.UTC(), credentialID, serverID, now.UTC(), serverID, tenantID)
	return saUpdateAffected(res, err, func() (bool, error) {
		return m.saExists(ctx, `SELECT 1 FROM sa_server_credentials c
		  JOIN sa_servers s ON s.id=c.server_id JOIN sa_tenants t ON t.id=s.tenant_id
		 WHERE c.id=? AND c.server_id=? AND s.tenant_id=? AND c.status='active'
		   AND (c.expires_at IS NULL OR c.expires_at>?) AND s.status='active' AND t.status='active' AND t.deleted_at IS NULL`,
			credentialID, serverID, tenantID, now.UTC())
	})
}
