-- 日志诊断模式的明细表（2026-11-05 拍板，本轮实施）。列名与 mysql/sqlite 树
-- 逐字一致，由 internal/migrate/schema_trees_test.go 机器核；类型与默认值按引擎
-- 自己的写法走。语义见 mysql 树那份文件头：id 不是自增，由写入方从 access_logs
-- 带过来，1:1 关联靠同值主键；captured_at 走面板可设的保留期，
-- credentials_until 走固定短时钟。
CREATE TABLE IF NOT EXISTS access_log_details (
  id                BIGINT NOT NULL,
  status_code       INTEGER NOT NULL DEFAULT 0,
  duration_ms       BIGINT NOT NULL DEFAULT 0,
  upstream_code     VARCHAR(64) NOT NULL DEFAULT '',
  headers_json      TEXT,
  body_excerpt      TEXT,
  credential_ref    TEXT,
  credentials_until TIMESTAMPTZ,
  captured_at       TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id)
);

CREATE INDEX idx_alid_credentials ON access_log_details (credentials_until);
CREATE INDEX idx_alid_captured ON access_log_details (captured_at);
