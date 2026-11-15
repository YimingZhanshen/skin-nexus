# 迁移演示夹具（demo/legacy）

一套**全部虚构**的老站数据，用来把「从 blessing-skin-server 无损迁移」的完整流程
（preflight → dry-run → import）亲手跑一遍再决定是否迁移。不含任何真实用户或站点信息。

## 目录内容

| 文件 | 说明 |
|---|---|
| `legacy.db` | 虚构老站的 SQLite 库（3 用户 / 3 角色 / 4 纹理 / 4 UUID 映射 / 2 邀请码 / 衣柜与选项） |
| `fixture.sql` | `legacy.db` 的生成源（可读真源：`sqlite3 legacy.db < fixture.sql`） |
| `.env` | 老站配置（PWD_METHOD=bcrypt；预检与旧密码验证都要用） |
| `textures/` | 4 张占位纹理（3 皮肤 + 1 披风），文件名 = 内容 sha256，符合老站纹理树规范 |

## 演示账号（密码统一 `demo1234`）

- `alice@example.com` / `alice_demo` —— 超管（迁移后可直接进管理面板）
- `bob@example.com` / `bob_demo` —— 普通用户，带披风
- `carol@example.com` / `carol_demo` —— 未验证邮箱用户

另有一个 `GhostDemo` 的孤儿 UUID 映射（无角色），用来演示导入器的 uuid 清洗：
导入报告里 `uuid clean: in=4 orphans=1 out=3` 就是它被正确丢弃。

## 三步实测（本地二进制；Docker 路径见 docs/deploy.md §四）

```bash
cd server

# 1. 预检：老站 .env 的密码算式 + 纹理树完整性（文件名=sha256、图片头合法）
go run ./cmd/skin-nexus migrate preflight --env ../demo/legacy/.env --textures ../demo/legacy/textures

# 2. 试跑（只读，不写库）
go run ./cmd/skin-nexus migrate import \
  --source-dsn sqlite:$(pwd)/../demo/legacy/legacy.db \
  --target-dsn sqlite:/path/to/你的新站.db \
  --source-prefix legacy_ \
  --textures-src ../demo/legacy/textures \
  --textures-dst /path/to/新站纹理目录 \
  --dry-run

# 3. 正式导入（去掉 --dry-run 即可；建议先备份目标库）
```

目标站需要先按 `docs/deploy.md` 起过一次（`SKIN_AUTO_MIGRATE=true` 建表）。
导入完成后，起服时挂 `SKIN_LEGACY_ENV=.../demo/legacy/.env`，上面三个账号用
`demo1234` 就能直接登录——旧密码算式不需要重置。

## 本地实测读数（2026-11-15，SQLite 双端）

- preflight：纹理树 4/4（hashMismatch=0、badIHDR=0），bcrypt 链对齐
- dry-run：users 3 / players 3 / textures 4 / uuid_map 4 / options 2 / closet 3 / 邀请码 2
- import：全部插入零跳过，`uuid clean: in=4 orphans=1 out=3`
- 登录实证：Yggdrasil `authenticate` 用 alice 的旧密码返回 200，
  profile `AliceDemo`（UUID `aaaa1111…` 零漂移）

> 再说一遍：**数据全部虚构**。账号、IP（TEST-NET-3 保留段）、纹理均为占位生成，
> 密码 `demo1234` 只用于演示，生产迁移请走 `docs/migration.md` 的完整手册。

---

# Migration demo fixture (demo/legacy)

An all-fabricated legacy site so you can run the full migration flow
(preflight → dry-run → import) yourself before trusting it with real data.
Demo accounts: `alice@example.com` (super admin), `bob@example.com`,
`carol@example.com` — all with password `demo1234`. The `GhostDemo` orphan row
demonstrates the uuid cleaner (`in=4 orphans=1 out=3`). Steps above work with
`go run ./cmd/skin-nexus migrate …`; the Docker path is in `docs/deploy.md` §4.
After the import, boot with `SKIN_LEGACY_ENV` pointing at the fixture `.env` and
the old password hashes keep verifying without a reset.
