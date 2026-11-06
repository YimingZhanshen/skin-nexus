package store

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"skin-nexus/server/internal/accesslog"
	"skin-nexus/server/internal/dbx"
	"skin-nexus/server/internal/dialect"
)

// AccessRow is one row of the admin access-log table (time localised by
// the driver's parseTime; JSON-marshalled as RFC3339 by the handler).
type AccessRow struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`
	Outcome   string    `json:"outcome"`
	UID       int64     `json:"uid"`
	Who       string    `json:"who"`
	IP        string    `json:"ip"`
	UA        string    `json:"ua"`
	Detail    string    `json:"detail"`
	CreatedAt time.Time `json:"createdAt"`
}

// InsertAccessLog appends one observability row — and, when the caller attached
// a diagnostic capture, the matching access_log_details row. Errors are returned
// so the accesslog sink can swallow them — the CALLER never sees a failure (the
// logger contract: logging never breaks a request).
//
// The detail row's primary key is the summary row's own id (the table has no
// autoincrement by design), so the two inserts cannot be independent calls: the
// id is carried from here. A detail failure — duplicate id included — is logged
// and dropped, never returned, because the summary line the panel pages through
// did land and the request must not pay for a missing extra.
func (m *Mysql) InsertAccessLog(ctx context.Context, e accesslog.Entry) error {
	var uid sql.NullInt64
	if e.UID > 0 {
		uid = sql.NullInt64{Int64: e.UID, Valid: true}
	}
	id, err := m.db.InsertReturningID(ctx,
		`INSERT INTO access_logs (kind, outcome, uid, who, ip, ua, detail)
		 VALUES (?,?,?,?,?,?,?)`,
		e.Kind, e.Outcome, uid, e.Who, e.IP, e.UA, e.Detail)
	if err != nil {
		return err
	}
	if e.Capture == nil {
		return nil // window closed (or nothing to capture): summary row only
	}
	if derr := m.InsertAccessDetail(ctx, id, e.Capture); derr != nil {
		logAccessDetailFailure(id, derr)
	}
	return nil
}

// ListAccessLogs pages the table newest-first. kind is an exact match
// ("" = all); q is a case-insensitive substring over who/ip/detail — the
// three columns an operator actually greps (nickname, address, reason).
func (m *Mysql) ListAccessLogs(ctx context.Context, kind, q string, page, size int) ([]AccessRow, int, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 50
	}
	where := " WHERE 1=1"
	var args []any
	if kind != "" {
		where += " AND kind = ?"
		args = append(args, kind)
	}
	if q = strings.TrimSpace(q); q != "" {
		ci := m.db.Dialect()
		where += " AND (" + ci.CIContains("who") + " OR " + ci.CIContains("ip") + " OR " + ci.CIContains("detail") + ")"
		like := dialect.LikeContains(q)
		args = append(args, like, like, like)
	}
	var total int
	if err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM access_logs`+where, args...).
		Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := m.db.QueryContext(ctx,
		`SELECT id, kind, outcome, COALESCE(uid,0), who, ip, ua, detail, created_at
		 FROM access_logs`+where+` ORDER BY id DESC LIMIT ? OFFSET ?`,
		append(args, size, dbx.Offset(page, size))...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]AccessRow, 0, size)
	for rows.Next() {
		var r AccessRow
		if err := rows.Scan(&r.ID, &r.Kind, &r.Outcome, &r.UID, &r.Who,
			&r.IP, &r.UA, &r.Detail, &r.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// PurgeAccessLogs drops rows older than olderThan (retention). Called by
// the boot-time ticker; returns the deleted count for the operator log.
func (m *Mysql) PurgeAccessLogs(ctx context.Context, olderThan time.Duration) (int64, error) {
	days := int64(olderThan.Hours() / 24)
	if days < 1 {
		days = 1
	}
	res, err := m.db.ExecContext(ctx,
		`DELETE FROM access_logs WHERE created_at < ?`,
		m.now().UTC().Add(-time.Duration(days)*24*time.Hour))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
