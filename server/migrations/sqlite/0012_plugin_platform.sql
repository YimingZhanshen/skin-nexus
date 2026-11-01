-- Plugin platform v2 foundation. manifest/config/doc stay TEXT: the host
-- marshals JSON in Go, so no engine-specific JSON operator is depended on.
CREATE TABLE IF NOT EXISTS plugin_meta (
  name           VARCHAR(64)  NOT NULL PRIMARY KEY,
  version        VARCHAR(64)  NOT NULL DEFAULT '',
  hook_api       INTEGER      NOT NULL DEFAULT 1,
  runtime        VARCHAR(16)  NOT NULL DEFAULT 'goja',
  manifest       TEXT NULL,
  config         TEXT NULL,
  schema_version INTEGER      NOT NULL DEFAULT 0,
  source         VARCHAR(16)  NOT NULL DEFAULT 'manual',
  checksum       CHAR(64)     NOT NULL DEFAULT '',
  state          VARCHAR(16)  NOT NULL DEFAULT 'discovered',
  last_error     TEXT NULL,
  installed_at   DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f +0000 UTC','now')),
  updated_at     DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f +0000 UTC','now'))
);

CREATE TABLE IF NOT EXISTS plugin_secrets (
  plugin      VARCHAR(64)  NOT NULL,
  k           VARCHAR(100) NOT NULL,
  ciphertext  BLOB NOT NULL,
  nonce       BLOB NOT NULL,
  key_version INTEGER NOT NULL DEFAULT 1,
  updated_at  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f +0000 UTC','now')),
  PRIMARY KEY (plugin, k)
);

CREATE TABLE IF NOT EXISTS plugin_kv (
  plugin     VARCHAR(64)  NOT NULL,
  ns         VARCHAR(64)  NOT NULL DEFAULT '',
  k          VARCHAR(191) NOT NULL,
  v          TEXT NOT NULL,
  expires_at DATETIME NULL,
  updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f +0000 UTC','now')),
  PRIMARY KEY (plugin, ns, k)
);
CREATE INDEX idx_pkv_expiry ON plugin_kv (plugin, expires_at);

CREATE TABLE IF NOT EXISTS plugin_docs (
  plugin     VARCHAR(64)  NOT NULL,
  coll       VARCHAR(64)  NOT NULL,
  id         VARCHAR(191) NOT NULL,
  doc        TEXT NOT NULL,
  created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f +0000 UTC','now')),
  updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f +0000 UTC','now')),
  PRIMARY KEY (plugin, coll, id)
);
CREATE INDEX idx_pdocs_updated ON plugin_docs (plugin, coll, updated_at);

CREATE TABLE IF NOT EXISTS plugin_migrations (
  plugin     VARCHAR(64) NOT NULL,
  version    INTEGER NOT NULL,
  applied_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f +0000 UTC','now')),
  PRIMARY KEY (plugin, version)
);
