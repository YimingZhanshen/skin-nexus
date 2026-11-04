-- Every notification insert first prunes rows older than the retention window
-- (notifications.go DELETE WHERE created_at < ?). With only (uid, id) and
-- (uid, read_at) indexed, that prune cost a full scan of the table on every
-- single insert — the report cost paid by whoever triggered the notification.
CREATE INDEX idx_notif_created ON notifications (created_at);
