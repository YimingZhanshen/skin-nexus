-- Signed plugin install audit. Artifact bytes live under <plugins>/.state and
-- are never stored in the database.
CREATE TABLE IF NOT EXISTS plugin_install_tx (
  id           VARCHAR(32) NOT NULL PRIMARY KEY,
  plugin       VARCHAR(64) NOT NULL,
  kind         VARCHAR(16) NOT NULL,
  from_version VARCHAR(64) NULL,
  to_version   VARCHAR(64) NULL,
  source       VARCHAR(16) NOT NULL DEFAULT 'upload',
  state        VARCHAR(24) NOT NULL,
  error        VARCHAR(1024) NULL,
  actor_uid    BIGINT NOT NULL DEFAULT 0,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_pit_plugin ON plugin_install_tx (plugin, created_at);
