-- Server Access Slice S1: additive tenant/member/server core only.
-- Closing the module never drops these tables or deletes retained data.
CREATE TABLE sa_tenants (
  id         BIGSERIAL PRIMARY KEY,
  name       VARCHAR(191) NOT NULL,
  status     VARCHAR(32) NOT NULL DEFAULT 'active',
  created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  deleted_at TIMESTAMPTZ NULL
);
CREATE INDEX idx_sa_tenants_status ON sa_tenants (status);
CREATE INDEX idx_sa_tenants_deleted_at ON sa_tenants (deleted_at);

CREATE TABLE sa_tenant_members (
  tenant_id  BIGINT NOT NULL,
  uid        BIGINT NOT NULL,
  role       VARCHAR(32) NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (tenant_id, uid),
  CONSTRAINT fk_sa_tenant_members_tenant FOREIGN KEY (tenant_id) REFERENCES sa_tenants (id) ON DELETE CASCADE,
  CONSTRAINT fk_sa_tenant_members_user FOREIGN KEY (uid) REFERENCES users (id) ON DELETE CASCADE
);
CREATE INDEX idx_sa_tenant_members_uid ON sa_tenant_members (uid);

CREATE TABLE sa_servers (
  id          BIGSERIAL PRIMARY KEY,
  tenant_id   BIGINT NOT NULL,
  public_id   VARCHAR(191) NOT NULL,
  name        VARCHAR(191) NOT NULL,
  status      VARCHAR(32) NOT NULL DEFAULT 'active',
  source_mode VARCHAR(32) NOT NULL DEFAULT 'url_secret',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  CONSTRAINT fk_sa_servers_tenant FOREIGN KEY (tenant_id) REFERENCES sa_tenants (id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX uq_sa_servers_public_id ON sa_servers (public_id);
CREATE INDEX idx_sa_servers_tenant_status ON sa_servers (tenant_id, status);
