-- In-site notifications, 90-day retention (pruned by the store on write).
-- `ref` snapshots the subject because the texture can be hard-deleted later —
-- a notification that renders "texture 412 was struck" after 412 is gone and
-- its tombstone purged would be useless.
-- Kinds are a closed set emitted by the host (see internal/store/
-- notifications.go); the text is rendered client-side from the locale
-- dictionary, like error codes.
CREATE TABLE IF NOT EXISTS notifications (
  id         INTEGER PRIMARY KEY,
  uid        INTEGER NOT NULL,
  kind       VARCHAR(32) NOT NULL,
  tid        INTEGER NOT NULL DEFAULT 0,
  ref        VARCHAR(255) NOT NULL DEFAULT '',
  read_at    DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f +0000 UTC','now'))
);
CREATE INDEX idx_notif_uid ON notifications (uid, id);
CREATE INDEX idx_notif_unread ON notifications (uid, read_at);
