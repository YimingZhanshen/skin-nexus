-- Access/communication logs (运维观测面), same columns as mysql/0011.
CREATE TABLE IF NOT EXISTS access_logs (
  id         INTEGER PRIMARY KEY,
  kind       VARCHAR(24)  NOT NULL,
  outcome    VARCHAR(16)  NOT NULL,
  uid        INTEGER NULL,
  who        VARCHAR(64)  NOT NULL DEFAULT '',
  ip         VARCHAR(45)  NOT NULL DEFAULT '',
  ua         VARCHAR(255) NOT NULL DEFAULT '',
  detail     VARCHAR(255) NOT NULL DEFAULT '',
  created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f +0000 UTC','now'))
);
CREATE INDEX idx_al_kind_time ON access_logs (kind, created_at);
CREATE INDEX idx_al_who ON access_logs (who);
CREATE INDEX idx_al_uid ON access_logs (uid);
CREATE INDEX idx_al_time ON access_logs (created_at);
