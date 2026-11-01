-- Plugin platform v2 foundation. manifest/config/doc stay TEXT: the host
-- marshals JSON in Go, so no engine-specific JSON operator is depended on and
-- the three trees keep the same read/write path.
CREATE TABLE IF NOT EXISTS plugin_meta (
  name           VARCHAR(64)  NOT NULL PRIMARY KEY,
  version        VARCHAR(64)  NOT NULL DEFAULT '',
  hook_api       INTEGER      NOT NULL DEFAULT 1,
  runtime        VARCHAR(16)  NOT NULL DEFAULT 'goja',
  manifest       TEXT NULL,
  config         TEXT NULL,
  schema_version INTEGER      NOT NULL DEFAULT 0,
  source         VARCHAR(16)  NOT NULL DEFAULT 'manual',
  checksum       VARCHAR(64)  NOT NULL DEFAULT '',
  state          VARCHAR(16)  NOT NULL DEFAULT 'discovered',
  last_error     TEXT NULL,
  installed_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS plugin_secrets (
  plugin      VARCHAR(64)  NOT NULL,
  k           VARCHAR(100) NOT NULL,
  ciphertext  BYTEA NOT NULL,
  nonce       BYTEA NOT NULL,
  key_version INTEGER NOT NULL DEFAULT 1,
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (plugin, k)
);

CREATE TABLE IF NOT EXISTS plugin_kv (
  plugin     VARCHAR(64)  NOT NULL,
  ns         VARCHAR(64)  NOT NULL DEFAULT '',
  k          VARCHAR(191) NOT NULL,
  v          TEXT NOT NULL,
  expires_at TIMESTAMPTZ NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (plugin, ns, k)
);
CREATE INDEX idx_pkv_expiry ON plugin_kv (plugin, expires_at);

CREATE TABLE IF NOT EXISTS plugin_docs (
  plugin     VARCHAR(64)  NOT NULL,
  coll       VARCHAR(64)  NOT NULL,
  id         VARCHAR(191) NOT NULL,
  doc        TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (plugin, coll, id)
);
CREATE INDEX idx_pdocs_updated ON plugin_docs (plugin, coll, updated_at);

CREATE TABLE IF NOT EXISTS plugin_migrations (
  plugin     VARCHAR(64) NOT NULL,
  version    INTEGER NOT NULL,
  applied_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (plugin, version)
);
