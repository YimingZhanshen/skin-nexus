-- In-site notifications (2026-11-02). Players previously learned that a
-- texture was taken down or an account was banned only by opening the page and
-- finding it changed; the only other channel was a transient toast.
-- 90-day retention matches the access/communication logs, and the rows carry a
-- snapshot of the subject (`ref`) because the texture can be hard-deleted later
-- — a notification that renders "texture 412 was struck" after 412 is gone and
-- its tombstone purged would be useless.
-- Kinds are a closed set emitted by the host (see internal/store/notifications.go);
-- the text is rendered client-side from the locale dictionary, like error codes.
CREATE TABLE IF NOT EXISTS notifications (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  uid        BIGINT UNSIGNED NOT NULL,
  kind       VARCHAR(32)     NOT NULL,
  tid        BIGINT UNSIGNED NOT NULL DEFAULT 0,
  ref        VARCHAR(255)    NOT NULL DEFAULT '',
  read_at    DATETIME(3)     NULL,
  created_at DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  KEY idx_notif_uid (uid, id),
  KEY idx_notif_unread (uid, read_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
