-- Server Access Slice S3: append-only verification events and atomic quota windows.
CREATE TABLE sa_join_events (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id       BIGINT UNSIGNED NOT NULL,
  server_id       BIGINT UNSIGNED NOT NULL,
  join_session_id BIGINT UNSIGNED NULL,
  uid             BIGINT UNSIGNED NULL,
  uuid            VARCHAR(32) NOT NULL DEFAULT '',
  player_name     VARCHAR(64) NOT NULL,
  outcome         VARCHAR(32) NOT NULL,
  reason_code     VARCHAR(64) NOT NULL,
  source_mode     VARCHAR(32) NOT NULL,
  duration_ms     INT NOT NULL DEFAULT 0,
  request_id      VARCHAR(128) NOT NULL DEFAULT '',
  created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uq_sa_join_events_server_session (server_id, join_session_id),
  KEY idx_sa_join_events_server_time (server_id, created_at),
  KEY idx_sa_join_events_tenant_time (tenant_id, created_at),
  CONSTRAINT fk_sa_join_events_tenant FOREIGN KEY (tenant_id) REFERENCES sa_tenants (id) ON DELETE CASCADE,
  CONSTRAINT fk_sa_join_events_server FOREIGN KEY (server_id) REFERENCES sa_servers (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE sa_quota_usage (
  server_id    BIGINT UNSIGNED NOT NULL,
  window_start DATETIME(3) NOT NULL,
  counter      VARCHAR(64) NOT NULL,
  expires_at   DATETIME(3) NOT NULL,
  value        BIGINT UNSIGNED NOT NULL DEFAULT 0,
  PRIMARY KEY (server_id, window_start, counter),
  KEY idx_sa_quota_usage_expiry (expires_at),
  CONSTRAINT fk_sa_quota_usage_server FOREIGN KEY (server_id) REFERENCES sa_servers (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
