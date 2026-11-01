-- users 表形状自愈（review A2 P1, 2026-10-23）——列集合与 mysql/0009 一致。
-- 旧版 0001 建出的 users 缺列时登录会整片 401；每条 ALTER 独立成句，
-- 已存在的列报 duplicate column 被容忍，形状各自收敛到当前契约。
ALTER TABLE users ADD COLUMN password_algo VARCHAR(32) NOT NULL DEFAULT 'argon2id';
ALTER TABLE users ADD COLUMN player_limit INTEGER NOT NULL DEFAULT 3;
ALTER TABLE users ADD COLUMN avatar_tid BIGINT NULL;
ALTER TABLE users ADD COLUMN email_verified SMALLINT NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN can_join_server SMALLINT NOT NULL DEFAULT 1;
