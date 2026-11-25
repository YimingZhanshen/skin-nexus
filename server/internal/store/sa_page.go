package store

import (
	"context"

	"skin-nexus/server/internal/dbx"
)

// ListSAServersPage returns one owner-visible page and the total matching the
// status filter. CountSAServers separately reports unfiltered tenant quota use.
func (m *Mysql) ListSAServersPage(ctx context.Context, tenantID int64, status string, page, size int) ([]SAServer, int, error) {
	where := ` FROM sa_servers WHERE tenant_id=?`
	args := []any{tenantID}
	if status != "" {
		where += ` AND status=?`
		args = append(args, status)
	}
	total, err := m.saPageCount(ctx, where, args)
	if err != nil {
		return nil, 0, err
	}
	rows, err := m.db.QueryContext(ctx,
		`SELECT id,tenant_id,public_id,name,status,source_mode,created_at`+where+
			` ORDER BY id DESC LIMIT ? OFFSET ?`, append(args, size, dbx.Offset(page, size))...)
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

// CountSAServers is the authoritative quota count: it is tenant-scoped and is
// never reduced by an owner list's status filter or pagination window.
func (m *Mysql) CountSAServers(ctx context.Context, tenantID int64) (int, error) {
	return m.saPageCount(ctx, ` FROM sa_servers WHERE tenant_id=?`, []any{tenantID})
}

func (m *Mysql) ListSAServerWhitelistPage(ctx context.Context, tenantID, serverID int64, page, size int) ([]SAServerWhitelist, int, error) {
	from := ` FROM sa_server_whitelist w JOIN sa_servers s ON s.id=w.server_id
		WHERE s.tenant_id=? AND w.server_id=?`
	args := []any{tenantID, serverID}
	total, err := m.saPageCount(ctx, from, args)
	if err != nil {
		return nil, 0, err
	}
	rows, err := m.db.QueryContext(ctx,
		`SELECT w.id,w.server_id,w.uid,w.note,w.created_by,w.created_at`+from+
			` ORDER BY w.id DESC LIMIT ? OFFSET ?`, append(args, size, dbx.Offset(page, size))...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]SAServerWhitelist, 0)
	for rows.Next() {
		v, err := scanSAServerWhitelist(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, v)
	}
	return out, total, rows.Err()
}

func (m *Mysql) ListSAServerBansPage(ctx context.Context, tenantID, serverID int64, page, size int) ([]SAServerBan, int, error) {
	from := ` FROM sa_server_bans b JOIN sa_servers s ON s.id=b.server_id
		WHERE s.tenant_id=? AND b.server_id=?`
	args := []any{tenantID, serverID}
	total, err := m.saPageCount(ctx, from, args)
	if err != nil {
		return nil, 0, err
	}
	rows, err := m.db.QueryContext(ctx,
		`SELECT b.id,b.server_id,b.uid,b.reason,b.expires_at,b.created_by,b.created_at`+from+
			` ORDER BY b.id DESC LIMIT ? OFFSET ?`, append(args, size, dbx.Offset(page, size))...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]SAServerBan, 0)
	for rows.Next() {
		v, err := scanSAServerBan(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, v)
	}
	return out, total, rows.Err()
}

func (m *Mysql) ListSATenantInvitesPage(ctx context.Context, tenantID int64, page, size int) ([]SATenantInvite, int, error) {
	where := ` WHERE tenant_id=?`
	args := []any{tenantID}
	total, err := m.saPageCount(ctx, ` FROM sa_tenant_invites`+where, args)
	if err != nil {
		return nil, 0, err
	}
	rows, err := m.db.QueryContext(ctx,
		`SELECT id,tenant_id,email,role,expires_at,accepted_at,created_at FROM sa_tenant_invites`+where+
			` ORDER BY id DESC LIMIT ? OFFSET ?`, append(args, size, dbx.Offset(page, size))...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]SATenantInvite, 0)
	for rows.Next() {
		v, err := scanSATenantInvite(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, v)
	}
	return out, total, rows.Err()
}

func (m *Mysql) saPageCount(ctx context.Context, from string, args []any) (int, error) {
	var total int
	if err := m.db.QueryRowContext(ctx, `SELECT COUNT(*)`+from, args...).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}
