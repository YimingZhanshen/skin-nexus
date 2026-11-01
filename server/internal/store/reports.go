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
	Moderation  string
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

// reportJoinSQL is the shared projection for the list and the detail view. The
// texture may already have been deleted — then its tombstone is what the report
// was about, and the history must keep rendering (an INNER JOIN would let a
// deletion quietly destroy the moderation record).
const reportJoinSQL = `
	SELECT r.id, r.texture_id, r.reporter_uid, r.reason, COALESCE(r.detail,''), r.status,
	       COALESCE(r.handled_by,0), r.created_at, r.handled_at,
	       COALESCE(t.description, ts.description, ''),
	       COALESCE(t.type, ts.type, ''),
	       COALESCE(u.nickname,''),
	       COALESCE(rep.nickname,''),
	       COALESCE(t.public, ts.public, 0),
	       COALESCE(t.moderation, ts.moderation, '')
	  FROM texture_reports r
	  LEFT JOIN textures t ON t.id = r.texture_id
	  LEFT JOIN texture_tombstones ts ON ts.texture_id = r.texture_id
	  LEFT JOIN users u ON u.id = COALESCE(t.uid, ts.uid)
	  LEFT JOIN users rep ON rep.id = r.reporter_uid`

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
	rows, err := m.db.QueryContext(ctx, reportJoinSQL+` WHERE `+where+` ORDER BY r.created_at DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]ReportRow, 0, opt.PerPage)
	for rows.Next() {
		var r ReportRow
		if err := rows.Scan(&r.ID, &r.TextureID, &r.ReporterUID, &r.Reason, &r.Detail,
			&r.Status, &r.HandledBy, &r.CreatedAt, &r.HandledAt,
			&r.TextureName, &r.TextureType, &r.Uploader, &r.Reporter, &r.Public, &r.Moderation); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

func (m *Mysql) GetReport(ctx context.Context, id int64) (ReportRow, error) {
	var r ReportRow
	err := m.db.QueryRowContext(ctx, reportJoinSQL+` WHERE r.id = ?`, id).
		Scan(&r.ID, &r.TextureID, &r.ReporterUID, &r.Reason, &r.Detail,
			&r.Status, &r.HandledBy, &r.CreatedAt, &r.HandledAt,
			&r.TextureName, &r.TextureType, &r.Uploader, &r.Reporter, &r.Public, &r.Moderation)
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

// StrikeTexture pulls a texture out of the library and marks it as violating,
// then closes the report that led to that verdict — in one transaction. The
// two separate statements used to be issued by the HTTP layer, where a failure
// between them left a report closed over a still-public texture.
func (m *Mysql) StrikeTexture(ctx context.Context, tid, reportID, handledBy int64) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Locking the texture first makes concurrent strikes on the same report
	// serialize, and tells us the texture is really gone (not just unchanged).
	var exists int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM textures WHERE id = ? FOR UPDATE`, tid).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("texture strike: %w", err)
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE texture_reports SET status='resolved', handled_by=?, handled_at=NOW(3)
		  WHERE id=? AND status='open'`, handledBy, reportID)
	if err != nil {
		return fmt.Errorf("texture strike report: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound // already handled by someone else
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE textures SET public=0, public_dedupe=NULL, moderation='violation' WHERE id=?`, tid); err != nil {
		return fmt.Errorf("texture strike moderation: %w", err)
	}
	return tx.Commit()
}

// LiftTextureModeration restores a struck texture's right to be published. It
// deliberately does not publish it — the verdict is lifted, the visibility is
// the owner's decision.
func (m *Mysql) LiftTextureModeration(ctx context.Context, tid int64) error {
	res, err := m.db.ExecContext(ctx,
		`UPDATE textures SET moderation='' WHERE id=? AND moderation <> ''`, tid)
	if err != nil {
		return fmt.Errorf("texture moderation lift: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var exists int64
		if err := m.db.QueryRowContext(ctx, `SELECT id FROM textures WHERE id=?`, tid).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
	}
	return nil
}
