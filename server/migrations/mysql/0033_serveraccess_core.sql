-- Server Access Slice S1: additive tenant/member/server core only.
-- Closing the module never drops these tables or deletes retained data.
CREATE TABLE sa_tenants (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  name       VARCHAR(191) NOT NULL,
  status     VARCHAR(32) NOT NULL DEFAULT 'active',
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  deleted_at DATETIME(3) NULL,
  PRIMARY KEY (id),
  KEY idx_sa_tenants_status (status),
  KEY idx_sa_tenants_deleted_at (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE sa_tenant_members (
  tenant_id  BIGINT UNSIGNED NOT NULL,
  uid        BIGINT UNSIGNED NOT NULL,
  role       VARCHAR(32) NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (tenant_id, uid),
  KEY idx_sa_tenant_members_uid (uid),
  CONSTRAINT fk_sa_tenant_members_tenant FOREIGN KEY (tenant_id) REFERENCES sa_tenants (id) ON DELETE CASCADE,
  CONSTRAINT fk_sa_tenant_members_user FOREIGN KEY (uid) REFERENCES users (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE sa_servers (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id   BIGINT UNSIGNED NOT NULL,
  public_id   VARCHAR(191) COLLATE utf8mb4_bin NOT NULL,
  name        VARCHAR(191) NOT NULL,
  status      VARCHAR(32) NOT NULL DEFAULT 'active',
  source_mode VARCHAR(32) NOT NULL DEFAULT 'url_secret',
  created_at  DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uq_sa_servers_public_id (public_id),
  KEY idx_sa_servers_tenant_status (tenant_id, status),
  CONSTRAINT fk_sa_servers_tenant FOREIGN KEY (tenant_id) REFERENCES sa_tenants (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
