-- 迁移演示夹具（demo/legacy/fixture.sql）—— 全部数据均为虚构演示数据，
-- 不含任何真实用户/站点信息。演示账号密码统一为 demo1234（bcrypt $2y$ 存储）。
-- 由它生成 legacy.db：sqlite3 legacy.db < fixture.sql
-- 表结构与列形状取自 SkinNexus 导入器的集成测试（三引擎 DDL 的 SQLite 段）。

DROP TABLE IF EXISTS legacy_users;
DROP TABLE IF EXISTS legacy_players;
DROP TABLE IF EXISTS legacy_textures;
DROP TABLE IF EXISTS legacy_uuid;
DROP TABLE IF EXISTS legacy_options;
DROP TABLE IF EXISTS legacy_user_closet;
DROP TABLE IF EXISTS legacy_invitation_codes;

CREATE TABLE legacy_users (
  uid INTEGER PRIMARY KEY,
  email VARCHAR(100) NOT NULL, nickname VARCHAR(50) NOT NULL DEFAULT '',
  score INTEGER NOT NULL, avatar INTEGER NOT NULL DEFAULT 0, password VARCHAR(255) NOT NULL,
  ip VARCHAR(45) NOT NULL, permission INTEGER NOT NULL DEFAULT 0,
  last_sign_at DATETIME NOT NULL, register_at DATETIME NOT NULL,
  verified INTEGER NOT NULL DEFAULT 0,
  verification_token VARCHAR(255) NOT NULL DEFAULT '',
  is_dark_mode INTEGER NOT NULL DEFAULT 0, locale VARCHAR(255) NULL);

CREATE TABLE legacy_players (
  pid INTEGER PRIMARY KEY, uid INTEGER NOT NULL,
  name VARCHAR(50) NOT NULL, tid_cape INTEGER NOT NULL DEFAULT 0,
  last_modified DATETIME NOT NULL, tid_skin INTEGER NOT NULL DEFAULT -1);

CREATE TABLE legacy_textures (
  tid INTEGER PRIMARY KEY, name VARCHAR(50) NOT NULL,
  type VARCHAR(10) NOT NULL, hash VARCHAR(64) NOT NULL, size INTEGER NOT NULL,
  uploader INTEGER NOT NULL, public INTEGER NOT NULL, upload_at DATETIME NOT NULL,
  likes INTEGER NOT NULL DEFAULT 0);

CREATE TABLE legacy_uuid (
  id INTEGER PRIMARY KEY, pid INTEGER NULL,
  name VARCHAR(255) NOT NULL, uuid VARCHAR(255) NOT NULL);

CREATE TABLE legacy_options (
  id INTEGER PRIMARY KEY, option_name VARCHAR(50) NOT NULL, option_value TEXT NOT NULL);

CREATE TABLE legacy_user_closet (user_uid INTEGER NOT NULL, texture_tid INTEGER NOT NULL, item_name TEXT NULL);

CREATE TABLE legacy_invitation_codes (
  id INTEGER PRIMARY KEY, code VARCHAR(255) NOT NULL,
  generated_at DATETIME NOT NULL, used_by INTEGER NOT NULL DEFAULT 0,
  used_at DATETIME NULL, description VARCHAR(255) NULL);

INSERT INTO legacy_users (uid,email,nickname,score,avatar,password,ip,permission,last_sign_at,register_at,verified,verification_token,is_dark_mode,locale) VALUES
(1,'alice@example.com','alice_demo',120,0,'$2y$04$WdEFw7YuWmZZfP4u16vqauaBhn/W8FYFmGQJHQTdQwBzOAdaefQjq','203.0.113.10',2,'2026-11-01 10:00:00','2025-06-01 09:00:00',1,'',1,'zh-CN'),
(2,'bob@example.com','bob_demo',30,0,'$2y$04$WdEFw7YuWmZZfP4u16vqauaBhn/W8FYFmGQJHQTdQwBzOAdaefQjq','203.0.113.11',0,'2026-11-02 11:00:00','2025-07-01 09:00:00',1,'',0,'en'),
(3,'carol@example.com','carol_demo',0,0,'$2y$04$WdEFw7YuWmZZfP4u16vqauaBhn/W8FYFmGQJHQTdQwBzOAdaefQjq','203.0.113.12',0,'2026-11-03 12:00:00','2025-08-01 09:00:00',0,'tok-carol-demo',0,NULL);

INSERT INTO legacy_players (pid,uid,name,tid_cape,tid_skin,last_modified) VALUES
(10,1,'AliceDemo',0,5,'2026-02-01 09:00:00'),
(11,2,'BobDemo',6,-1,'2026-02-02 09:00:00'),
(12,3,'CarolDemo',0,7,'2026-02-03 09:00:00');

INSERT INTO legacy_textures (tid,name,type,hash,size,uploader,public,upload_at,likes) VALUES
(5,'demo steve skin','steve','4b4cd1b8c40599f1fe2fd79f6908602d8193714ea9fe398f3b1efa66cda414a1',162,1,1,'2025-08-01 09:00:00',3),
(6,'demo cape','cape','6700b14810b918a9ac917ee96ff224784846daf06e348e6ced77433a96516091',89,2,1,'2025-08-02 09:00:00',1),
(7,'demo alex skin','alex','573a5d2df15e85f78812b77f7073576c81290b225781016993e793da1a89b234',162,3,1,'2025-08-03 09:00:00',0),
(8,'demo private skin','steve','81fbd6b31aa3752bd5542eefa49f660e79c54541b57e8dc593c8e8fd46d252a8',160,1,0,'2025-08-04 09:00:00',0);

INSERT INTO legacy_uuid (id,pid,name,uuid) VALUES
(1,10,'AliceDemo','aaaa1111aaaa1111aaaa1111aaaa1111'),
(2,11,'BobDemo','bbbb2222bbbb2222bbbb2222bbbb2222'),
(3,12,'CarolDemo','cccc3333cccc3333cccc3333cccc3333'),
(4,NULL,'GhostDemo','dddd4444dddd4444dddd4444dddd4444');

INSERT INTO legacy_options (id,option_name,option_value) VALUES
(1,'site_name','演示老站 Demo Legacy'),
(2,'ygg_uuid_algorithm','v3');

INSERT INTO legacy_user_closet (user_uid,texture_tid,item_name) VALUES
(1,5,'我的皮肤'),
(2,6,'Bob 的披风'),
(3,7,NULL);

INSERT INTO legacy_invitation_codes (id,code,generated_at,used_by,used_at,description) VALUES
(1,'aaaabbbbccccddddeeeeffff00001111','2025-09-01 09:00:00',0,NULL,'虚构演示码 1'),
(2,'11112222333344445555666677778888','2025-09-02 09:00:00',1,'2025-09-03 09:00:00','虚构演示码 2');
