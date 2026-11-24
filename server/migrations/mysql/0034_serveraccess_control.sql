-- Server Access Slice S2: additive control-plane tables only.
ALTER TABLE sa_tenants ADD COLUMN server_limit INT NOT NULL DEFAULT 10;

CREATE TABLE sa_tenant_invites (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id   BIGINT UNSIGNED NOT NULL,
  email       VARCHAR(191) NOT NULL,
  role        VARCHAR(32) NOT NULL,
  token_hash  CHAR(64) COLLATE utf8mb4_bin NOT NULL,
  expires_at  DATETIME(3) NOT NULL,
  accepted_at DATETIME(3) NULL,
  created_at  DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uq_sa_tenant_invites_token (token_hash),
  KEY idx_sa_tenant_invites_tenant (tenant_id, expires_at),
  CONSTRAINT fk_sa_tenant_invites_tenant FOREIGN KEY (tenant_id) REFERENCES sa_tenants (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE sa_server_credentials (
  id           BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  server_id    BIGINT UNSIGNED NOT NULL,
  mode         VARCHAR(32) NOT NULL,
  secret_hash  CHAR(64) COLLATE utf8mb4_bin NOT NULL,
  scopes       TEXT NOT NULL,
  status       VARCHAR(32) NOT NULL DEFAULT 'active',
  expires_at   DATETIME(3) NULL,
  last_used_at DATETIME(3) NULL,
  created_at   DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uq_sa_server_credentials_hash (secret_hash),
  KEY idx_sa_server_credentials_server_status (server_id, status),
  CONSTRAINT fk_sa_server_credentials_server FOREIGN KEY (server_id) REFERENCES sa_servers (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE sa_server_policies (
  server_id                 BIGINT UNSIGNED NOT NULL,
  whitelist_enabled         TINYINT(1) NOT NULL DEFAULT 0,
  premium_fallback_enabled  TINYINT(1) NOT NULL DEFAULT 1,
  verify_rate_limit         INT NOT NULL DEFAULT 120,
  verify_burst              INT NOT NULL DEFAULT 20,
  max_concurrent            INT NOT NULL DEFAULT 8,
  whitelist_limit           INT NOT NULL DEFAULT 1000,
  event_retention_days      INT NOT NULL DEFAULT 30,
  PRIMARY KEY (server_id),
  CONSTRAINT fk_sa_server_policies_server FOREIGN KEY (server_id) REFERENCES sa_servers (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE sa_server_whitelist (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  server_id  BIGINT UNSIGNED NOT NULL,
  uid        BIGINT UNSIGNED NOT NULL,
  note       VARCHAR(255) NOT NULL DEFAULT '',
  created_by BIGINT UNSIGNED NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uq_sa_server_whitelist_server_uid (server_id, uid),
  KEY idx_sa_server_whitelist_server (server_id),
  CONSTRAINT fk_sa_server_whitelist_server FOREIGN KEY (server_id) REFERENCES sa_servers (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE sa_server_bans (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  server_id  BIGINT UNSIGNED NOT NULL,
  uid        BIGINT UNSIGNED NOT NULL,
  reason     VARCHAR(255) NOT NULL DEFAULT '',
  expires_at DATETIME(3) NULL,
  created_by BIGINT UNSIGNED NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uq_sa_server_bans_server_uid (server_id, uid),
  KEY idx_sa_server_bans_server (server_id),
  CONSTRAINT fk_sa_server_bans_server FOREIGN KEY (server_id) REFERENCES sa_servers (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
