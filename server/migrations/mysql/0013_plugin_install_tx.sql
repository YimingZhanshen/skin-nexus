-- Signed plugin install audit (P2 Core). One row per install/upgrade/uninstall
-- transaction; artifact bytes live under <plugins>/.state and are never stored
-- in MySQL. DDL-only per internal/migrate/tracked.go.
CREATE TABLE IF NOT EXISTS plugin_install_tx (
  id           CHAR(32)     NOT NULL,
  plugin       VARCHAR(64)  NOT NULL,
  kind         VARCHAR(16)  NOT NULL,
  from_version VARCHAR(64)  NULL,
  to_version   VARCHAR(64)  NULL,
  source       VARCHAR(16)  NOT NULL DEFAULT 'upload',
  state        VARCHAR(24)  NOT NULL,
  error        VARCHAR(1024) NULL,
  actor_uid    BIGINT       NOT NULL DEFAULT 0,
  created_at   DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at   DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  KEY idx_pit_plugin (plugin, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
