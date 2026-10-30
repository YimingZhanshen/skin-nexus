package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	mysql "github.com/go-sql-driver/mysql"
)

var ErrDuplicateReport = errors.New("store: report already exists")

type ReportRow struct {
	ID          int64
	TextureID   int64
	ReporterUID int64
	Reason      string
	Detail      string
	Status      string
	HandledBy   int64
	CreatedAt   time.Time
	HandledAt   sql.NullTime
	TextureName string
	TextureType string
	Uploader    string
	Reporter    string
	Public      bool
}

type ReportOpts struct {
	Status  string
	Page    int
	PerPage int
}

func normalizeReportOpts(o ReportOpts) ReportOpts {
	if o.Page < 1 {
		o.Page = 1
	}
	if o.PerPage < 1 || o.PerPage > 100 {
		o.PerPage = 20
	}
	return o
}

// CreateReport inserts one report; the unique (texture, reporter) key is
// mapped to ErrDuplicateReport so the HTTP layer can return 409.
func (m *Mysql) CreateReport(ctx context.Context, textureID, reporterUID int64, reason, detail string) (int64, error) {
	res, err := m.db.ExecContext(ctx,
		`INSERT INTO texture_reports (texture_id, reporter_uid, reason, detail, status)
		 VALUES (?,?,?,?,'open')`, textureID, reporterUID, reason, nullableStr(detail))
	if err != nil {
		var me *mysql.MySQLError
		if errors.As(err, &me) && me.Number == 1062 {
			return 0, ErrDuplicateReport
		}
		return 0, err
	}
	return res.LastInsertId()
}

func (m *Mysql) CountReportsByUserSince(ctx context.Context, uid int64, since time.Time) (int, error) {
	var n int
	err := m.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM texture_reports WHERE reporter_uid = ? AND created_at >= ?`, uid, since).Scan(&n)
	return n, err
}

func (m *Mysql) CountReportsByTextureSince(ctx context.Context, tid int64, since time.Time) (int, error) {
	var n int
	err := m.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM texture_reports WHERE texture_id = ? AND created_at >= ?`, tid, since).Scan(&n)
	return n, err
}

func (m *Mysql) ListReports(ctx context.Context, opt ReportOpts) ([]ReportRow, int, error) {
	opt = normalizeReportOpts(opt)
	where, args := "1=1", []any{}
	switch opt.Status {
	case "open", "resolved", "dismissed":
		where += " AND r.status = ?"
		args = append(args, opt.Status)
	}
	var total int
	if err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM texture_reports r WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, opt.PerPage, (opt.Page-1)*opt.PerPage)
	rows, err := m.db.QueryContext(ctx,
		`SELECT r.id, r.texture_id, r.reporter_uid, r.reason, COALESCE(r.detail,''), r.status,
		        COALESCE(r.handled_by,0), r.created_at, r.handled_at,
		        COALESCE(t.description,''), t.type, COALESCE(u.nickname,''),
		        COALESCE(rep.nickname,''), t.public
		   FROM texture_reports r
		   JOIN textures t ON t.id = r.texture_id
		   LEFT JOIN users u ON u.id = t.uid
		   LEFT JOIN users rep ON rep.id = r.reporter_uid
		  WHERE `+where+` ORDER BY r.created_at DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]ReportRow, 0, opt.PerPage)
	for rows.Next() {
		var r ReportRow
		if err := rows.Scan(&r.ID, &r.TextureID, &r.ReporterUID, &r.Reason, &r.Detail,
			&r.Status, &r.HandledBy, &r.CreatedAt, &r.HandledAt,
			&r.TextureName, &r.TextureType, &r.Uploader, &r.Reporter, &r.Public); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

func (m *Mysql) GetReport(ctx context.Context, id int64) (ReportRow, error) {
	var r ReportRow
	err := m.db.QueryRowContext(ctx,
		`SELECT r.id, r.texture_id, r.reporter_uid, r.reason, COALESCE(r.detail,''), r.status,
		        COALESCE(r.handled_by,0), r.created_at, r.handled_at,
		        COALESCE(t.description,''), t.type, COALESCE(u.nickname,''),
		        COALESCE(rep.nickname,''), t.public
		   FROM texture_reports r
		   JOIN textures t ON t.id = r.texture_id
		   LEFT JOIN users u ON u.id = t.uid
		   LEFT JOIN users rep ON rep.id = r.reporter_uid
		  WHERE r.id = ?`, id).
		Scan(&r.ID, &r.TextureID, &r.ReporterUID, &r.Reason, &r.Detail,
			&r.Status, &r.HandledBy, &r.CreatedAt, &r.HandledAt,
			&r.TextureName, &r.TextureType, &r.Uploader, &r.Reporter, &r.Public)
	if errors.Is(err, sql.ErrNoRows) {
		return ReportRow{}, ErrNotFound
	}
	if err != nil {
		return ReportRow{}, fmt.Errorf("report get: %w", err)
	}
	return r, nil
}

// ResolveReport closes one report. hide is handled by the caller (it also
// updates the texture visibility); this method only records the decision.
func (m *Mysql) ResolveReport(ctx context.Context, id int64, status string, handledBy int64) error {
	if status != "resolved" && status != "dismissed" {
		return fmt.Errorf("invalid report status %q", status)
	}
	res, err := m.db.ExecContext(ctx,
		`UPDATE texture_reports SET status=?, handled_by=?, handled_at=NOW(3)
		  WHERE id=? AND status='open'`, status, handledBy, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
