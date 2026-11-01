-- Access/communication logs (2026-10-26 运维观测面): web + launcher
-- logins, Yggdrasil server traffic, Union traffic — one narrow table the
-- admin panel can filter/paginate without touching audit_log (which stays
-- for security-relevant ACTIONS; this table is for OBSERVING traffic).
--   * kind/outcome are short enumerations written by internal/accesslog;
--   * who/ip/ua/detail are truncated rune-safe by the logger, never here;
--   * created_at is indexed for the retention purge (90d) and time filters;
--   * uid nullable: launcher/union traffic often has no account behind it.
CREATE TABLE IF NOT EXISTS access_logs (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  kind       VARCHAR(24)  NOT NULL,
  outcome    VARCHAR(16)  NOT NULL,
  uid        BIGINT UNSIGNED NULL,
  who        VARCHAR(64)  NOT NULL DEFAULT '',
  ip         VARCHAR(45)  NOT NULL DEFAULT '',
  ua         VARCHAR(255) NOT NULL DEFAULT '',
  detail     VARCHAR(255) NOT NULL DEFAULT '',
  created_at DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  KEY idx_al_kind_time (kind, created_at),
  KEY idx_al_who (who),
  KEY idx_al_uid (uid),
  KEY idx_al_time (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
