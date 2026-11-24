-- Server Access Slice S3: append-only verification events and atomic quota windows.
CREATE TABLE sa_join_events (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  tenant_id       INTEGER NOT NULL,
  server_id       INTEGER NOT NULL,
  join_session_id INTEGER NULL,
  uid             INTEGER NULL,
  uuid            VARCHAR(32) NOT NULL DEFAULT '',
  player_name     VARCHAR(64) NOT NULL,
  outcome         VARCHAR(32) NOT NULL,
  reason_code     VARCHAR(64) NOT NULL,
  source_mode     VARCHAR(32) NOT NULL,
  duration_ms     INT NOT NULL DEFAULT 0,
  request_id      VARCHAR(128) NOT NULL DEFAULT '',
  created_at      DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f +0000 UTC','now')),
  CONSTRAINT fk_sa_join_events_tenant FOREIGN KEY (tenant_id) REFERENCES sa_tenants (id) ON DELETE CASCADE,
  CONSTRAINT fk_sa_join_events_server FOREIGN KEY (server_id) REFERENCES sa_servers (id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX uq_sa_join_events_server_session ON sa_join_events (server_id, join_session_id);
CREATE INDEX idx_sa_join_events_server_time ON sa_join_events (server_id, created_at);
CREATE INDEX idx_sa_join_events_tenant_time ON sa_join_events (tenant_id, created_at);

CREATE TABLE sa_quota_usage (
  server_id    INTEGER NOT NULL,
  window_start DATETIME NOT NULL,
  counter      VARCHAR(64) NOT NULL,
  expires_at   DATETIME NOT NULL,
  value        INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (server_id, window_start, counter),
  CONSTRAINT fk_sa_quota_usage_server FOREIGN KEY (server_id) REFERENCES sa_servers (id) ON DELETE CASCADE
);
CREATE INDEX idx_sa_quota_usage_expiry ON sa_quota_usage (expires_at);
