package store

// In-site notifications (2026-11-02): the host's three "something happened to
// YOUR account/content" events — report outcome, texture moderation, ban
// state. Players previously found out by opening the page and noticing it had
// changed.
//
// `kind` is a closed set and the row carries a snapshot of the subject, so the
// client renders the text from its locale dictionary exactly like an error
// code. Rows are pruned on write (same 90-day horizon as the access logs),
// which keeps the table bounded without a cron job.
import (
	"context"
	"time"
)

const (
	NotifReportResolved   = "report.resolved"   // a report on your texture was upheld
	NotifReportDismissed  = "report.dismissed"  // a report on your texture was rejected
	NotifTextureHidden    = "texture.hidden"    // taken down and marked a violation
	NotifModerationLifted = "moderation.lifted" // the violation verdict was lifted
	NotifAccountBanned    = "account.banned"
	NotifAccountUnbanned  = "account.unbanned"
)

const notifRetention = 90 * 24 * time.Hour

// NotifRow is one notification as the API returns it.
type NotifRow struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`
	TextureID int64     `json:"tid"`
	Ref       string    `json:"ref"`
	Read      bool      `json:"read"`
	CreatedAt time.Time `json:"createdAt"`
}

// InsertNotification appends one row for uid. Failure is returned but callers
// treat it as best-effort: losing a notification must not fail the operation
// it reports.
func (m *Mysql) InsertNotification(ctx context.Context, uid int64, kind string, tid int64, ref string) error {
	_, _ = m.db.ExecContext(ctx,
		`DELETE FROM notifications WHERE created_at < NOW(3) - INTERVAL ? SECOND`,
		int64(notifRetention/time.Second))
	_, err := m.db.ExecContext(ctx,
		`INSERT INTO notifications (uid, kind, tid, ref) VALUES (?,?,?,?)`,
		uid, kind, tid, ref)
	return err
}

// ListNotifications pages one player's inbox newest-first. onlyUnread narrows
// to the unread slice (the panel's filter); size is clamped.
func (m *Mysql) ListNotifications(ctx context.Context, uid int64, onlyUnread bool, page, size int) ([]NotifRow, int, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	where := ` WHERE uid = ?`
	var args []any
	args = append(args, uid)
	if onlyUnread {
		where += ` AND read_at IS NULL`
	}
	var total int
	if err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := m.db.QueryContext(ctx,
		`SELECT id, kind, tid, ref, read_at IS NOT NULL, created_at
		 FROM notifications`+where+` ORDER BY id DESC LIMIT ? OFFSET ?`,
		append(append([]any{}, args...), size, (page-1)*size)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]NotifRow, 0, size)
	for rows.Next() {
		var n NotifRow
		if err := rows.Scan(&n.ID, &n.Kind, &n.TextureID, &n.Ref, &n.Read, &n.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, n)
	}
	return out, total, rows.Err()
}

// UnreadCount backs the sidebar badge.
func (m *Mysql) UnreadNotificationCount(ctx context.Context, uid int64) (int, error) {
	var n int
	err := m.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM notifications WHERE uid = ? AND read_at IS NULL`, uid).Scan(&n)
	return n, err
}

// MarkNotificationRead marks one of the caller's own rows. A foreign id simply
// matches nothing — the inbox is partitioned by uid, so this is not an
// authorization oracle.
func (m *Mysql) MarkNotificationRead(ctx context.Context, uid, id int64) error {
	_, err := m.db.ExecContext(ctx,
		`UPDATE notifications SET read_at = NOW(3) WHERE id = ? AND uid = ? AND read_at IS NULL`, id, uid)
	return err
}

// MarkAllNotificationsRead clears the caller's badge.
func (m *Mysql) MarkAllNotificationsRead(ctx context.Context, uid int64) error {
	_, err := m.db.ExecContext(ctx,
		`UPDATE notifications SET read_at = NOW(3) WHERE uid = ? AND read_at IS NULL`, uid)
	return err
}

// TextureNameForNotification snapshots what a moderation notice is about (the
// short display name lives in `description`, see 0017). A texture deleted
// before the player reads the notice yields "" and the client renders the kind
// text on its own.
func (m *Mysql) TextureNameForNotification(ctx context.Context, tid int64) string {
	var name string
	err := m.db.QueryRowContext(ctx,
		`SELECT COALESCE(description,'') FROM textures WHERE id = ?`, tid).Scan(&name)
	if err != nil {
		return ""
	}
	return name
}
