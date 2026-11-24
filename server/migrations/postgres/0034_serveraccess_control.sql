-- Server Access Slice S2: additive control-plane tables only.
ALTER TABLE sa_tenants ADD COLUMN server_limit INT NOT NULL DEFAULT 10;

CREATE TABLE sa_tenant_invites (
  id          BIGSERIAL PRIMARY KEY,
  tenant_id   BIGINT NOT NULL,
  email       VARCHAR(191) NOT NULL,
  role        VARCHAR(32) NOT NULL,
  token_hash  CHAR(64) NOT NULL,
  expires_at  TIMESTAMPTZ NOT NULL,
  accepted_at TIMESTAMPTZ NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  CONSTRAINT fk_sa_tenant_invites_tenant FOREIGN KEY (tenant_id) REFERENCES sa_tenants (id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX uq_sa_tenant_invites_token ON sa_tenant_invites (token_hash);
CREATE INDEX idx_sa_tenant_invites_tenant ON sa_tenant_invites (tenant_id, expires_at);

CREATE TABLE sa_server_credentials (
  id           BIGSERIAL PRIMARY KEY,
  server_id    BIGINT NOT NULL,
  mode         VARCHAR(32) NOT NULL,
  secret_hash  CHAR(64) NOT NULL,
  scopes       TEXT NOT NULL,
  status       VARCHAR(32) NOT NULL DEFAULT 'active',
  expires_at   TIMESTAMPTZ NULL,
  last_used_at TIMESTAMPTZ NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  CONSTRAINT fk_sa_server_credentials_server FOREIGN KEY (server_id) REFERENCES sa_servers (id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX uq_sa_server_credentials_hash ON sa_server_credentials (secret_hash);
CREATE INDEX idx_sa_server_credentials_server_status ON sa_server_credentials (server_id, status);

CREATE TABLE sa_server_policies (
  server_id                 BIGINT PRIMARY KEY,
  whitelist_enabled         INT NOT NULL DEFAULT 0,
  premium_fallback_enabled  INT NOT NULL DEFAULT 1,
  verify_rate_limit         INT NOT NULL DEFAULT 120,
  verify_burst              INT NOT NULL DEFAULT 20,
  max_concurrent            INT NOT NULL DEFAULT 8,
  whitelist_limit           INT NOT NULL DEFAULT 1000,
  event_retention_days      INT NOT NULL DEFAULT 30,
  CONSTRAINT fk_sa_server_policies_server FOREIGN KEY (server_id) REFERENCES sa_servers (id) ON DELETE CASCADE
);

CREATE TABLE sa_server_whitelist (
  id         BIGSERIAL PRIMARY KEY,
  server_id  BIGINT NOT NULL,
  uid        BIGINT NOT NULL,
  note       VARCHAR(255) NOT NULL DEFAULT '',
  created_by BIGINT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  CONSTRAINT fk_sa_server_whitelist_server FOREIGN KEY (server_id) REFERENCES sa_servers (id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX uq_sa_server_whitelist_server_uid ON sa_server_whitelist (server_id, uid);
CREATE INDEX idx_sa_server_whitelist_server ON sa_server_whitelist (server_id);

CREATE TABLE sa_server_bans (
  id         BIGSERIAL PRIMARY KEY,
  server_id  BIGINT NOT NULL,
  uid        BIGINT NOT NULL,
  reason     VARCHAR(255) NOT NULL DEFAULT '',
  expires_at TIMESTAMPTZ NULL,
  created_by BIGINT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  CONSTRAINT fk_sa_server_bans_server FOREIGN KEY (server_id) REFERENCES sa_servers (id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX uq_sa_server_bans_server_uid ON sa_server_bans (server_id, uid);
CREATE INDEX idx_sa_server_bans_server ON sa_server_bans (server_id);
