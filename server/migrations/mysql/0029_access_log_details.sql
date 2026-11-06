-- 日志诊断模式的明细表（2026-11-05 拍板，本轮实施）。
--
-- 主表 access_logs 一直是一条轻量摘要行（255 字节 detail、无响应体、无头），
-- 排障时想知道「这次请求到底回了什么」只能猜。明细不并进主表是刻意的：主表每
-- 六小时扫一次、按 90 天清，把它撑宽等于把摘要行也一起拖进备份和导出。
--
-- 1:1 挂在 access_logs.id 上：id 不是自增列，由写入方带过来（应用先在主表拿到
-- id 再写明细）。仓里没有外键（三棵树 grep FOREIGN KEY 零命中），这条关联靠
-- 同值主键维持，跟 job/notification 那些表一样的约定。
--
-- 两个留存时钟分开放：
--   captured_at        —— 跟着面板可设的明细保留期走（默认 90 天）
--   credentials_until  —— 只有这一列带可用凭据的行归它自己那条固定短时钟，
--                         不跟 90 天走：90 天是日志的保留期，不是「把可用凭据
--                         存一个季度」的许可
CREATE TABLE IF NOT EXISTS access_log_details (
  id                BIGINT UNSIGNED NOT NULL,
  status_code       INT NOT NULL DEFAULT 0,
  duration_ms       BIGINT NOT NULL DEFAULT 0,
  upstream_code     VARCHAR(64) NOT NULL DEFAULT '',
  headers_json      TEXT NULL,
  body_excerpt      TEXT NULL,
  credential_ref    TEXT NULL,
  credentials_until DATETIME(3) NULL,
  captured_at       DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 到期清扫按 credentials_until 找行，保留期清理按 captured_at 圈窗口。
CREATE INDEX idx_alid_credentials ON access_log_details (credentials_until);
CREATE INDEX idx_alid_captured ON access_log_details (captured_at);
