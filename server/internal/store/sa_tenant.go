package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"skin-nexus/server/internal/dbx"
	"skin-nexus/server/internal/dialect"
)

func (m *Mysql) CreateSATenant(ctx context.Context, name string, serverLimit int) (int64, error) {
	if serverLimit < 1 {
		serverLimit = 10
	}
	id, err := m.db.InsertReturningID(ctx,
		`INSERT INTO sa_tenants (name,status,server_limit,created_at) VALUES (?,'active',?,?)`,
		name, serverLimit, m.now().UTC())
	if err != nil {
		return 0, err
	}
	return id, nil
}

func (m *Mysql) SATenantByID(ctx context.Context, tenantID int64) (SATenant, error) {
	return scanSATenant(m.db.QueryRowContext(ctx,
		`SELECT id,name,status,server_limit,created_at,deleted_at FROM sa_tenants
		 WHERE id=? AND deleted_at IS NULL`, tenantID))
}

func scanSATenant(s rowScanner) (SATenant, error) {
	var t SATenant
	err := s.Scan(&t.ID, &t.Name, &t.Status, &t.ServerLimit, &t.CreatedAt, &t.DeletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return SATenant{}, ErrNotFound
	}
	return t, err
}

// PlatformSATenants is deliberately platform-only: owner handlers never call
// an unscoped tenant list.
func (m *Mysql) PlatformSATenants(ctx context.Context, q string, page, size int) ([]SATenant, int, error) {
	where, args := saSearch(`name`, q)
	var total int
	if err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sa_tenants WHERE deleted_at IS NULL`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := m.db.QueryContext(ctx,
		`SELECT id,name,status,server_limit,created_at,deleted_at FROM sa_tenants
		 WHERE deleted_at IS NULL`+where+` ORDER BY id DESC LIMIT ? OFFSET ?`,
		append(args, size, dbx.Offset(page, size))...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]SATenant, 0)
	for rows.Next() {
		t, err := scanSATenant(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, t)
	}
	return out, total, rows.Err()
}

// DeleteSATenant is a platform lifecycle primitive. Its tenant id is the
// explicit resource scope; sa_* foreign keys cascade, global users do not.
func (m *Mysql) DeleteSATenant(ctx context.Context, tenantID int64) error {
	res, err := m.db.ExecContext(ctx, `DELETE FROM sa_tenants WHERE id=?`, tenantID)
	return saAffected(res, err)
}

func (m *Mysql) UpdateSATenantStatus(ctx context.Context, tenantID int64, status string) error {
	res, err := m.db.ExecContext(ctx,
		`UPDATE sa_tenants SET status=? WHERE id=? AND deleted_at IS NULL`, status, tenantID)
	return saUpdateAffected(res, err, func() (bool, error) {
		return m.saExists(ctx, `SELECT 1 FROM sa_tenants WHERE id=? AND deleted_at IS NULL`, tenantID)
	})
}

func (m *Mysql) UpdateSATenantQuota(ctx context.Context, tenantID int64, serverLimit int) error {
	res, err := m.db.ExecContext(ctx,
		`UPDATE sa_tenants SET server_limit=? WHERE id=? AND deleted_at IS NULL`, serverLimit, tenantID)
	return saUpdateAffected(res, err, func() (bool, error) {
		return m.saExists(ctx, `SELECT 1 FROM sa_tenants WHERE id=? AND deleted_at IS NULL`, tenantID)
	})
}

func (m *Mysql) AddSATenantMember(ctx context.Context, tenantID, uid int64, role string) error {
	_, err := m.db.ExecContext(ctx,
		`INSERT INTO sa_tenant_members (tenant_id,uid,role,created_at) VALUES (?,?,?,?)`,
		tenantID, uid, role, m.now().UTC())
	if isDupKey(err) {
		return ErrDuplicate
	}
	return err
}

func (m *Mysql) UpdateSATenantMemberRole(ctx context.Context, tenantID, uid int64, role string) error {
	res, err := m.db.ExecContext(ctx,
		`UPDATE sa_tenant_members SET role=? WHERE tenant_id=? AND uid=?`, role, tenantID, uid)
	return saUpdateAffected(res, err, func() (bool, error) {
		return m.saExists(ctx, `SELECT 1 FROM sa_tenant_members WHERE tenant_id=? AND uid=?`, tenantID, uid)
	})
}

func (m *Mysql) RemoveSATenantMember(ctx context.Context, tenantID, uid int64) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Serialize owner-removal checks on MySQL/PostgreSQL. SQLite's single
	// writer provides the equivalent exclusion; LockSQL emits nothing there.
	var tenantLock int
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM sa_tenants WHERE id=?`+m.db.Dialect().LockSQL(), tenantID).Scan(&tenantLock); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	var role string
	if err := tx.QueryRowContext(ctx,
		`SELECT role FROM sa_tenant_members WHERE tenant_id=? AND uid=?`, tenantID, uid).Scan(&role); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if role == "owner" {
		var owners int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sa_tenant_members WHERE tenant_id=? AND role='owner'`, tenantID).Scan(&owners); err != nil {
			return err
		}
		if owners <= 1 {
			return ErrLastOwner
		}
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM sa_tenant_members WHERE tenant_id=? AND uid=?`, tenantID, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return tx.Commit()
}

func (m *Mysql) ListSATenantMembers(ctx context.Context, tenantID int64) ([]SATenantMember, error) {
	rows, err := m.db.QueryContext(ctx,
		`SELECT tm.tenant_id,tm.uid,tm.role,tm.created_at,COALESCE(u.nickname,'')
		 FROM sa_tenant_members tm LEFT JOIN users u ON u.id=tm.uid
		 WHERE tm.tenant_id=? ORDER BY tm.created_at,tm.uid`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SATenantMember, 0)
	for rows.Next() {
		var v SATenantMember
		if err := rows.Scan(&v.TenantID, &v.UID, &v.Role, &v.CreatedAt, &v.Nickname); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// SATenantMemberByUID is the sole user-to-tenant derivation query. It returns
// the oldest active membership and never accepts a tenant id from a client.
func (m *Mysql) SATenantMemberByUID(ctx context.Context, uid int64) (SATenantMember, bool, error) {
	var v SATenantMember
	err := m.db.QueryRowContext(ctx,
		`SELECT tm.tenant_id,tm.uid,tm.role,tm.created_at,COALESCE(u.nickname,'')
		 FROM sa_tenant_members tm JOIN sa_tenants t ON t.id=tm.tenant_id
		 LEFT JOIN users u ON u.id=tm.uid
		 WHERE tm.uid=? AND t.status='active' AND t.deleted_at IS NULL
		 ORDER BY tm.created_at,tm.tenant_id LIMIT 1`, uid).
		Scan(&v.TenantID, &v.UID, &v.Role, &v.CreatedAt, &v.Nickname)
	if errors.Is(err, sql.ErrNoRows) {
		return SATenantMember{}, false, nil
	}
	return v, err == nil, err
}

func (m *Mysql) SAUserEmail(ctx context.Context, uid int64) (string, error) {
	var email string
	err := m.db.QueryRowContext(ctx, `SELECT email FROM users WHERE id=?`, uid).Scan(&email)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return email, err
}

func (m *Mysql) CreateSATenantInvite(ctx context.Context, tenantID int64, email, role, tokenHash string, expiresAt time.Time) (int64, error) {
	return m.db.InsertReturningID(ctx,
		`INSERT INTO sa_tenant_invites (tenant_id,email,role,token_hash,expires_at,created_at) VALUES (?,?,?,?,?,?)`,
		tenantID, strings.ToLower(strings.TrimSpace(email)), role, tokenHash, expiresAt.UTC(), m.now().UTC())
}

func (m *Mysql) ListSATenantInvites(ctx context.Context, tenantID int64) ([]SATenantInvite, error) {
	rows, err := m.db.QueryContext(ctx,
		`SELECT id,tenant_id,email,role,expires_at,accepted_at,created_at FROM sa_tenant_invites
		 WHERE tenant_id=? ORDER BY id DESC LIMIT 200`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SATenantInvite, 0)
	for rows.Next() {
		var v SATenantInvite
		if err := rows.Scan(&v.ID, &v.TenantID, &v.Email, &v.Role, &v.ExpiresAt, &v.AcceptedAt, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (m *Mysql) RevokeSATenantInvite(ctx context.Context, tenantID, inviteID int64) error {
	res, err := m.db.ExecContext(ctx,
		`DELETE FROM sa_tenant_invites WHERE tenant_id=? AND id=?`, tenantID, inviteID)
	return saAffected(res, err)
}

// AcceptSATenantInvite consumes the token and adds the member in one
// transaction. The conditional UPDATE is the single-use arbiter: exactly one
// concurrent caller can observe RowsAffected==1.
func (m *Mysql) AcceptSATenantInvite(ctx context.Context, tokenHash string, uid int64, email string, now time.Time) (int64, error) {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`UPDATE sa_tenant_invites SET accepted_at=?
		 WHERE token_hash=? AND accepted_at IS NULL AND expires_at>? AND `+m.db.Dialect().CIEquals("email")+`
		   AND tenant_id IN (SELECT id FROM sa_tenants WHERE status='active' AND deleted_at IS NULL)`,
		now.UTC(), tokenHash, now.UTC(), email)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return 0, ErrNotFound
	}
	var tenantID int64
	var role string
	if err := tx.QueryRowContext(ctx,
		`SELECT tenant_id,role FROM sa_tenant_invites WHERE token_hash=?`, tokenHash).Scan(&tenantID, &role); err != nil {
		return 0, err
	}
	var tenantLock int
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM sa_tenants WHERE id=? AND status='active' AND deleted_at IS NULL`+m.db.Dialect().LockSQL(), tenantID).Scan(&tenantLock); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	var existingRole string
	if err := tx.QueryRowContext(ctx,
		`SELECT role FROM sa_tenant_members WHERE tenant_id=? AND uid=?`, tenantID, uid).Scan(&existingRole); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if saRoleRank(existingRole) >= saRoleRank(role) {
		role = existingRole
	}
	q, err := m.db.Dialect().Upsert("sa_tenant_members",
		[]string{"tenant_id", "uid", "role", "created_at"},
		[]string{"tenant_id", "uid"}, []string{"role"})
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, q, tenantID, uid, role, now.UTC()); err != nil {
		return 0, fmt.Errorf("accept invite member: %w", err)
	}
	return tenantID, tx.Commit()
}

func saRoleRank(role string) int {
	switch role {
	case "owner":
		return 3
	case "admin":
		return 2
	case "viewer":
		return 1
	default:
		return 0
	}
}

func (m *Mysql) CreateSAServer(ctx context.Context, tenantID int64, publicID, name string) (SAServer, error) {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return SAServer{}, err
	}
	defer tx.Rollback()
	id, err := tx.InsertReturningID(ctx,
		`INSERT INTO sa_servers (tenant_id,public_id,name,status,source_mode,created_at)
		 SELECT t.id,?,?,'active','url_secret',? FROM sa_tenants t
		 WHERE t.id=? AND t.status='active' AND t.deleted_at IS NULL
		   AND (SELECT COUNT(*) FROM sa_servers WHERE tenant_id=t.id)<t.server_limit`,
		publicID, name, m.now().UTC(), tenantID)
	if id == 0 && (err == nil || errors.Is(err, sql.ErrNoRows)) {
		var one int
		if scanErr := tx.QueryRowContext(ctx,
			`SELECT 1 FROM sa_tenants WHERE id=? AND status='active' AND deleted_at IS NULL`, tenantID).Scan(&one); scanErr != nil {
			if errors.Is(scanErr, sql.ErrNoRows) {
				return SAServer{}, ErrNotFound
			}
			return SAServer{}, scanErr
		}
		return SAServer{}, ErrQuota
	}
	if err != nil {
		if isDupKey(err) {
			return SAServer{}, ErrDuplicate
		}
		return SAServer{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sa_server_policies (server_id) VALUES (?)`, id); err != nil {
		return SAServer{}, err
	}
	if err := tx.Commit(); err != nil {
		return SAServer{}, err
	}
	return m.SAServerByID(ctx, tenantID, id)
}

func (m *Mysql) SAServerByID(ctx context.Context, tenantID, serverID int64) (SAServer, error) {
	return scanSAServer(m.db.QueryRowContext(ctx,
		`SELECT id,tenant_id,public_id,name,status,source_mode,created_at FROM sa_servers
		 WHERE tenant_id=? AND id=?`, tenantID, serverID))
}

func scanSAServer(s rowScanner) (SAServer, error) {
	var v SAServer
	err := s.Scan(&v.ID, &v.TenantID, &v.PublicID, &v.Name, &v.Status, &v.SourceMode, &v.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return SAServer{}, ErrNotFound
	}
	return v, err
}

func (m *Mysql) ListSAServers(ctx context.Context, tenantID int64, status string) ([]SAServer, error) {
	q := `SELECT id,tenant_id,public_id,name,status,source_mode,created_at FROM sa_servers WHERE tenant_id=?`
	args := []any{tenantID}
	if status != "" {
		q += ` AND status=?`
		args = append(args, status)
	}
	rows, err := m.db.QueryContext(ctx, q+` ORDER BY id DESC LIMIT 200`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SAServer, 0)
	for rows.Next() {
		v, err := scanSAServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (m *Mysql) UpdateSAServer(ctx context.Context, tenantID, serverID int64, name, status string) error {
	res, err := m.db.ExecContext(ctx,
		`UPDATE sa_servers SET name=?,status=? WHERE tenant_id=? AND id=?`, name, status, tenantID, serverID)
	return saUpdateAffected(res, err, func() (bool, error) {
		return m.saExists(ctx, `SELECT 1 FROM sa_servers WHERE tenant_id=? AND id=?`, tenantID, serverID)
	})
}

func (m *Mysql) DeleteSAServer(ctx context.Context, tenantID, serverID int64) error {
	res, err := m.db.ExecContext(ctx,
		`DELETE FROM sa_servers WHERE tenant_id=? AND id=?`, tenantID, serverID)
	return saAffected(res, err)
}

func (m *Mysql) PlatformSAServers(ctx context.Context, q string, page, size int) ([]SAServer, int, error) {
	where, args := saSearch(`s.name`, q)
	var total int
	if err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sa_servers s`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := m.db.QueryContext(ctx,
		`SELECT s.id,s.tenant_id,s.public_id,s.name,s.status,s.source_mode,s.created_at
		 FROM sa_servers s`+where+` ORDER BY s.id DESC LIMIT ? OFFSET ?`,
		append(args, size, dbx.Offset(page, size))...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]SAServer, 0)
	for rows.Next() {
		v, err := scanSAServer(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, v)
	}
	return out, total, rows.Err()
}

func (m *Mysql) PlatformSAServerByID(ctx context.Context, serverID int64) (SAServer, error) {
	return scanSAServer(m.db.QueryRowContext(ctx,
		`SELECT id,tenant_id,public_id,name,status,source_mode,created_at FROM sa_servers WHERE id=?`, serverID))
}

// PlatformServerAccessAudit is restricted to the platform handler. Owner
// handlers have no audit-log route and cannot read another tenant's history.
func (m *Mysql) PlatformServerAccessAudit(ctx context.Context, q string, page, size int) ([]map[string]interface{}, int, error) {
	where := ` WHERE a.action LIKE 'serveraccess!_%' ESCAPE '!'`
	args := []any{}
	if strings.TrimSpace(q) != "" {
		where += ` AND (` + m.db.Dialect().CIContains("a.action") + ` OR ` + m.db.Dialect().CIContains("COALESCE(a.detail,'{}')") + `)`
		like := dialect.LikeContains(strings.TrimSpace(q))
		args = append(args, like, like)
	}
	from := ` FROM audit_log a LEFT JOIN users u ON u.id=a.actor_uid` + where
	var total int
	if err := m.db.QueryRowContext(ctx, `SELECT COUNT(*)`+from, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := m.db.QueryContext(ctx,
		`SELECT a.id,COALESCE(a.actor_uid,0),a.action,COALESCE(a.detail,'{}'),a.created_at,COALESCE(u.nickname,'')`+from+
			` ORDER BY a.id DESC LIMIT ? OFFSET ?`, append(args, size, dbx.Offset(page, size))...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]map[string]interface{}, 0)
	for rows.Next() {
		var id, actor int64
		var action, detail, nick string
		var created time.Time
		if err := rows.Scan(&id, &actor, &action, &detail, &created, &nick); err != nil {
			return nil, 0, err
		}
		out = append(out, map[string]interface{}{"id": id, "actorUid": actor, "actorName": nick, "action": action, "detail": detail, "createdAt": created.Format(time.RFC3339)})
	}
	return out, total, rows.Err()
}

func saSearch(column, q string) (string, []any) {
	q = strings.TrimSpace(q)
	if q == "" {
		return "", nil
	}
	return ` AND ` + mCI(column) + ` LIKE ?`, []any{dialect.LikeContains(q)}
}

// mCI is a tiny indirection so saSearch can use the store dialect without
// threading a receiver through every list builder.
func mCI(column string) string { return "LOWER(" + column + ") LIKE LOWER(?)" }

func saAffected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}

func (m *Mysql) saExists(ctx context.Context, query string, args ...any) (bool, error) {
	var one int
	err := m.db.QueryRowContext(ctx, query, args...).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// saUpdateAffected preserves idempotent updates on MySQL, whose default
// clientFoundRows=false reports zero for a scoped row whose value did not
// change. A zero result is therefore resolved by a scoped existence read;
// DELETE paths continue to use saAffected so replay still returns not-found.
func saUpdateAffected(res sql.Result, err error, exists func() (bool, error)) error {
	if err != nil {
		return err
	}
	if n, affectedErr := res.RowsAffected(); affectedErr != nil {
		return affectedErr
	} else if n == 1 {
		return nil
	}
	ok, lookupErr := exists()
	if lookupErr != nil {
		return lookupErr
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}
