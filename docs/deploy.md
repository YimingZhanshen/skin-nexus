# SkinNexus 部署手册（Docker 优先）

> 推荐所有站长用 **Docker Compose** 部署：升级只换容器、数据卷零丢失、免装运行时，
> 且 GitHub Actions 每次 push main 都会自动构建生产镜像推到 GHCR，部署机**只拉不编**。
> 从老站迁数据见 [migration.md](migration.md)；源码构建细节见 [build.md](build.md)。

## 一、部署方式选择

| 方式 | 适合 | 说明 |
|---|---|---|
| **A. Docker Compose（推荐）** | 绝大多数站长 | 拉镜像 → 改环境变量 → `up -d`；升级 `pull && up -d`；内置 MySQL 可选 |
| B. 源码构建 + systemd | 无 Docker 的主机（如已有主机面板/内网机） | 单二进制 + web/dist，见 §五 |

## 二、Docker Compose 部署（主线）

### 2.1 准备 compose 文件

**零编辑快速开始**：仓库根的 [`docker-compose.quickstart.yml`](../docker-compose.quickstart.yml)
把所有必填值改为 `.env` 插值，新站无需改文件：

```bash
mkdir skin-nexus && cd skin-nexus
curl -fsSLO https://raw.githubusercontent.com/YimingZhanshen/skin-nexus/main/docker-compose.quickstart.yml
cat > .env <<EOF
SKIN_BASE_URL=https://skin.example.com
SKIN_DB_PASSWORD=$(openssl rand -hex 16)
SKIN_DB_ROOT_PASSWORD=$(openssl rand -hex 16)
SKIN_ADMIN_TOKEN=$(openssl rand -hex 32)
EOF
docker compose -f docker-compose.quickstart.yml up -d
```

需要外部 MySQL、固定镜像 tag 或反代细节时，继续用下面的成品
[`docker-compose.yml`](../docker-compose.yml)：

```bash
mkdir -p /opt/skin-nexus && cd /opt/skin-nexus
# 从仓库复制 docker-compose.yml 后编辑环境变量（见 2.2）
```

镜像来源（compose 默认第一行）：

```yaml
image: ghcr.io/yimingzhanshen/skin-nexus:latest   # CI 自动构建，部署机只拉不编
# 需要固定版本时改用 :sha-<commit> 标签，回滚=改回旧 sha 再 up -d
# 离线/内网环境可源码构建：把 image 行改回 build: . 后 docker compose up -d --build
```

- `:latest` = main 分支最近一次构建；`:sha-<commit>` 为 40 位完整 commit sha，
  **只有 CI 构建过的 commit 有镜像**（一次 push 多个 commit 时只有 push 的 HEAD 有）。
  可用镜像清单：GitHub 仓库 Packages 页，或 `docker buildx imagetools inspect ghcr.io/yimingzhanshen/skin-nexus`。
- 首次部署前确认 GHCR 包为 **public**（匿名可 pull）；若包是 private，
  先 `docker login ghcr.io`（PAT 带 `read:packages`）再 `docker compose pull`。
- `pull_policy: always` 需要支持该字段的 Docker Compose v2（compose v1 不识别）；
  不确定时升级前手动 `docker compose pull` 即可绕过。

### 2.2 选择数据库模式（同一份文件二选一）

**模式 1：内置 MySQL（默认，全新站点最省心；镜像为 `mysql:8.0`，需要 5.7 请走模式 2 外部库）**

保留 `db` 服务与 `depends_on` 不动，共改 **3 处数据库密码占位 + 1 个管理 token**：

```yaml
db:
  environment:
    MYSQL_DATABASE: skin_nexus
    MYSQL_USER: skin
    MYSQL_PASSWORD: CHANGE_ME          # <- 改掉（①）
    MYSQL_ROOT_PASSWORD: CHANGE_ME_ROOT # <- 改掉（②）
```

③ `SKIN_DATABASE_DSN` 里的 `CHANGE_ME` **必须与 ① MYSQL_PASSWORD 一致**（漏改 → access denied →
容器反复重启）；④ `SKIN_ADMIN_TOKEN` 按 §2.3 生成。即 compose 里共动 4 处。

**模式 2：外部 MySQL（已有老 blessing 库 / 迁移目标库 / 主机面板环境的库）**

注释掉 `db` 服务与 `depends_on`，把 DSN 指向外部库：

```yaml
environment:
  SKIN_DATABASE_DSN: 'skin:PASS@tcp(127.0.0.1:3306)/skin_nexus?parseTime=true&charset=utf8mb4'
# 容器要访问宿主机上的外部 MySQL（宿主机 127.0.0.1:3306）时，二选一：
#   network_mode: host        # 整容器走主机网络（此时 ports 段删除；SKIN_LISTEN 建议 127.0.0.1:8080）
#   或 DSN 用宿主机内网 IP / docker 网桥网段（172.17.0.1）
```

外部库 MySQL 5.7 / 8.0 均可（5.7.44 实测通过）；空库启动时 `SKIN_AUTO_MIGRATE=true`
自动建表，随后首访站点进 `/setup` 安装向导（建管理员 / 站点名 / 皮肤域）。

### 2.3 必改环境变量

| 变量 | 说明 |
|---|---|
| `SKIN_BASE_URL` | 站点公开地址，如 `https://skin.example.com`。**必须在首次 `up -d` 之前改定为最终访问地址**：纹理 URL 在启动时按它快照、yggc/OIDC 的 issuer 也只认它（`SKIN_OIDC_ISSUER` 默认跟它）——事后在管理面板改 `site_url` 无法完全修正。ALI 响应头与此无关（按请求 Host + `X-Forwarded-Proto` 现算） |
| `SKIN_DATABASE_DSN` | 外部库模式必改；内置库模式保持默认。**必须带 `parseTime=true`**（缺失直接启动失败） |
| `SKIN_ADMIN_TOKEN` | `openssl rand -hex 32` 生成；机器/API 调用管理接口的回退凭据（管理面板本身用管理员账号登录即可，无需此 token；留空 = 机器 token 通道关闭） |

其余可选变量（默认值即可跑通）：

| 变量 | 默认 | 说明 |
|---|---|---|
| `SKIN_LISTEN` | `:8080` | 容器内监听，**保持 `:8080` 即可**（改端口/绑本机见表下注） |
| `SKIN_SERVER_NAME` / `SKIN_SITE_NAME` | `SkinNexus` | Yggdrasil 元数据显示名 / 站点名（也可在管理面板改） |
| `SKIN_SKIN_DOMAINS` | 空 | 皮肤域白名单；不接联盟填本站域名，接入 MUA Union 以联盟公布清单为准（改动会破坏联盟互认） |
| `SKIN_DATA_DIR` | `/data`（镜像内） | 统一数据根：textures/、签名钥、themes/ 都在它下面 |
| `SKIN_AUTO_MIGRATE` | `true`（镜像内） | 追踪式建表/补表：`schema_migrations` 逐文件记标，空库全量、既有库自动补新文件 |
| `SKIN_INVITE_REQUIRED` | `false` | 注册强制邀请码（等价老站必填语义）；也可在管理面板「站点设置」改 |
| `SKIN_REQUIRE_VERIFICATION` | `false` | join 邮箱验证门（邮件流落地前保持关） |
| `SKIN_SMTP_HOST` / `SKIN_SMTP_PORT` / `SKIN_SMTP_USER` / `SKIN_SMTP_PASS` / `SKIN_SMTP_FROM` | 无 / `465` / 无 / 无 / 无 | 找回密码邮件流（A1）：SMTP 五键，也可在管理面板「站点设置」配；未配置时注册/找回不提供邮箱通道 |
| `SKIN_MOJANG_FALLBACK` | `1`（开） | profile/hasJoined 本地未命中时向 Mojang 官方 sessionserver 发站外查询兜底；不希望站外请求就设 `0`（面板可热切） |
| `SKIN_RATE_LIMIT_AUTH` / `SKIN_LOGIN_FAIL_LIMIT` | `120` / `5` | 认证限流：每 IP 10 分钟滑动窗口 / 连续失败锁定次数 |
| `SKIN_OIDC_ENABLED` | `false` | 启动器免密登录（yggc/OIDC）；`SKIN_OIDC_ISSUER` 默认跟 `SKIN_BASE_URL` |
| `SKIN_UNION_ENABLED` | `false` | MUA Union 联盟模块；未接入保持关 = 零路由零表面 |
| `SKIN_LEGACY_ENV` | 无 | **从老站迁移且用户密码非 bcrypt/argon2 PHC 格式时必设**：指向挂载进容器的老站 `.env`（盐链来源），否则这些存量用户无法登录（见 [migration.md](migration.md)） |

路径/密钥类变量默认值即可，一般无需改动：`SKIN_TEXTURES_DIR`（`<data>/textures`）、`SKIN_SIGN_KEY`（`<data>/sign-key.pem`）、`SKIN_OIDC_SIGN_KEY`（`<data>/oidc-key.pem`）、`SKIN_THEMES_DIR`（`<data>/themes`）、`SKIN_PLUGINS_DIR`（`<data>/plugins`）、`SKIN_PLUGIN_STATE_DIR`（默认 `<data>/plugins/.state`，安装/回滚缓存，需备份）、`SKIN_PLUGIN_SECRET_KEY`（插件密钥主密钥；缺省生成 `<data>/plugin-secrets.key`，**需一并备份**）、`SKIN_PLUGIN_MARKET_URL`（签名 registry，默认官方市场；显式设为空=关闭市场；下游可改为自己的 registry）、`SKIN_PLUGIN_TRUST_KEYS`（`keyId:base64pub,...`，空=拒绝一切市场/签名安装；compose 默认内置官方公钥）、`SKIN_PLUGIN_UPLOAD_MAX`（默认 64 MiB）、`SKIN_MIGRATIONS_DIR`（镜像内 `/app/migrations`）、`SKIN_OIDC_SHARED_CLIENT_ID`（`2`）。

> `SKIN_LISTEN` 改值注意：bridge 网络下不要改成 `127.0.0.1:8080`（进程只绑容器环回 → 端口映射
> connection refused，而 healthcheck 仍报 healthy，极难排查）；反代场景想只对本机开放就改 ports
> 绑定为 `127.0.0.1:8080:8080`；仅 `network_mode: host` 时才建议 `127.0.0.1:8080`。镜像 HEALTHCHECK
> 固定探 `127.0.0.1:8080`，改端口需同步改 healthcheck。
Union 七键（`SKIN_UNION_API_ROOT` / `SKIN_UNION_MEMBER_KEY` / `SKIN_UNION_HOST_PUBKEY` / `SKIN_UNION_ALLOW_UPDATEBACKENDKEY` / `SKIN_UNION_QUERYEMAIL_MODE` / `SKIN_UNION_OAUTH2_SIG_KEY` / `SKIN_UNION_HUB_ORIGIN`）走管理面板配置，env 仅作初始值/回退。完整变量清单以 `server/internal/config/config.go` 加上散在 `main.go` / `adminapi` 里的 `os.Getenv` 为准（`SKIN_ADMIN_TOKEN`、`SKIN_SMTP_*`、`SKIN_MOJANG_FALLBACK`、`SKIN_LEGACY_ENV` 都不在 config.go）。

> **站点类设置大多已迁管理 UI**（「管理 → 站点设置」：站点名/皮肤域/邀请码/验证门/mail 五键/ygg 六键/Union 八键——
> env「Union 七键」与面板「Union 八键」是两个集合：面板多 `union_enabled` 开关），
> 但**优先级分键**，别想当然：`site_url` option 优先于 env（不过**纹理 URL 只认启动时的
> `SKIN_BASE_URL` 快照**，面板改 site_url 不会改纹理 URL）；`site_name`/`server_name`/
> `skin_domains` 则是**显式 env 优先**——想在面板改这三项，就不要在 compose 里设对应 env
> （compose 默认已不设）。`union_enabled`/`union_api_root`/`union_member_key` 热切换立即生效，
> 其余重启生效。凭据类（DSN、admin token）只走环境变量。

### 2.4 启动与验收

```bash
docker compose up -d
docker compose ps            # 等两个服务 healthy（镜像内置 HEALTHCHECK 探 /healthz，DB 探活）
curl -s http://127.0.0.1:8080/api/yggdrasil/ | head -c 300   # 元数据 JSON
# 浏览器打开 /setup（或任一 SPA 路由）→ 空库自动进安装向导
```

首启链路（空库）：自动迁移建表（`SKIN_AUTO_MIGRATE=true`）→ 访问 `/setup`（或任一 SPA 路由）自动进入向导
（建管理员/站点名/皮肤域）→ **向导是一次性门**：完成后永久 404，向导里建的账号即管理员。
若目标库已有用户（如迁移导入后），向导自动跳过（users>0 即视为已初始化），此时管理员
需在导入前用向导创建，或导入后用 SQL/管理接口补建——顺序详见 [migration.md](migration.md)。

> 端口收敛建议：8080 只对反代放开（ports 写 `127.0.0.1:8080:8080` 或防火墙收敛）——
> 同宿主/同网桥的其它容器处于 XFF 信任区，可伪造最后一跳绕过限流。

### 2.5 升级

```bash
docker compose pull && docker compose up -d    # 只换容器，卷不动，数据零丢失
# 新版会自动补齐新 DDL（追踪式迁移）；回滚 = image 改回旧 :sha-* 再执行同命令
```

> **回滚只换代码、不回滚 schema**：迁移引擎只进不退，新 DDL 已落库且旧镜像会继续用它
> （加列无感，但删列/改语义的迁移会让旧代码出错）——破坏性变更前先做 §2.6 备份。

### 2.6 备份与恢复

数据全在两个命名卷（`docker volume ls | grep skin`）：

| 卷 | 内容 | 备份方式 |
|---|---|---|
| `skin-data` | textures/、sign-key.pem、oidc-key.pem、themes/、union 密钥 | 见下 |
| `skin-db` | 内置 MySQL 数据（外部库模式无此卷，用你自己的 mysqldump 方案） | `mysqldump` |

```bash
# skin-data 整卷备份（sign-key.pem 丢失 = 全网皮肤失效，务必纳入定期备份）
docker run --rm -v <项目>_skin-data:/data -v /backup:/backup alpine \
  tar czf /backup/skin-data-$(date +%F).tgz -C /data .

# 内置库逻辑备份
docker compose exec db sh -c 'exec mysqldump -uskin -p"$MYSQL_PASSWORD" skin_nexus' \
  | gzip > /backup/skin_nexus-$(date +%F).sql.gz

# 恢复：停站 → 解包回卷 / 导入 SQL → 起站
```

> `sign-key.pem` 是纹理签名私钥（**首次启动自动生成**，RSA-4096），建议额外**加密后异地**备份一份；
> `oidc-key.pem` 与它分离（免密登录 token 专用），**仅在 `SKIN_OIDC_ENABLED=true` 时生成**——
> OIDC 关闭时文件不存在属正常，开启后首启自动生成。两钥互不代替，都纳入备份。

### 2.7 日志与排错

```bash
docker compose logs -f skin-nexus          # 应用日志
docker compose logs --tail 50 db          # 内置库日志
```

| 现象 | 处理 |
|---|---|
| 容器反复 unhealthy | 看日志：DSN 不通 / 密码错最常见；`docker compose exec skin-nexus wget -qO- http://127.0.0.1:8080/healthz` 单独验证 |
| 启动报 migration 失败 | 非空库但缺表/结构漂移时追踪式迁移会报错退出——先备份再看日志定位文件，勿手改 `schema_migrations` |
| 外部库连不上 | 网络模式与 DSN 主机对不上（host 模式配 `127.0.0.1`、桥接模式配 `172.17.0.1` 或内网 IP）；MySQL 账号需有建表权限（自动迁移用） |
| 页面是占位页 | `web/dist` 缺失——确认拉的是 GHCR 镜像而非残缺自构建 |

## 三、Nginx 反代（前置 HTTPS 证书）

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
}
# /textures/ 大流量可让 nginx 直读数据卷目录绕过后端（可选）：
# location /textures/ {
#     alias /var/lib/docker/volumes/<项目>_skin-data/_data/textures/;
#     add_header Cache-Control "public, max-age=31536000, immutable";
# }
```

> `X-Forwarded-Proto` 必传：ALI 响应头的协议判断依赖它。
>
> `X-Forwarded-For` 同样必传：注册按 IP 计数（`regs_per_ip`）与 `users.ip` 记录、
> 认证限流窗口都走 `ygg.ClientIP`。缺了它，容器看到的对端全是反代地址——
> **全站共享一个计数桶，第三个注册就把所有人挡在门外**，且所有账号的 `ip` 列
> 都会记成同一个值（切流后追溯困难）。服务端只在对端处于 loopback/RFC1918
> 信任区时才采信 XFF 的**最后一跳**（防伪造绕限流），所以反代必须用
> `$proxy_add_x_forwarded_for` 或 `$remote_addr` 追加真实地址，不要透传客户端原样头。
>
> `X-Real-IP` 服务端并不读取（只认 `X-Forwarded-For` / `X-Forwarded-Proto`），保留无妨。

## 四、从老站迁移

独立手册见 [migration.md](migration.md)（preflight → dry-run → import 三步 +
切流断言 + 签名钥对齐）。Docker 场景两个入口差异：

```bash
# 迁移 CLI 直接用镜像跑（不需要在服务器上装 Go）：
docker compose exec skin-nexus /app/skin-nexus migrate preflight \
  --env /data/legacy.env --textures /data/legacy-textures
# --textures 指向【老站】纹理目录（审计对象是老站的纹理树，不是新站数据卷）；
# legacy.env 与老站纹理目录可放进数据卷，或在 compose 临时加只读挂载：
#   - ./legacy.env:/data/legacy.env:ro
#   - /老站/storage/textures:/data/legacy-textures:ro
```

## 五、备选：源码构建 + systemd（无 Docker 环境）

构建步骤见 [build.md](build.md) §二/§三；产物为单二进制 + `web/dist/` + `migrations/`。
systemd 单元完整示例：

```ini
[Unit]
Description=SkinNexus
After=network.target mysql.service

[Service]
WorkingDirectory=/opt/skin-nexus
Environment=SKIN_LISTEN=127.0.0.1:8080
Environment=SKIN_BASE_URL=https://skin.example.com
Environment=SKIN_DATABASE_DSN=skin:PASS@tcp(127.0.0.1:3306)/skin_nexus?parseTime=true&charset=utf8mb4
Environment=SKIN_DATA_DIR=/opt/skin-nexus/data
Environment=SKIN_MIGRATIONS_DIR=/opt/skin-nexus/migrations
Environment=SKIN_AUTO_MIGRATE=true
Environment=SKIN_ADMIN_TOKEN=<openssl rand -hex 32>
ExecStart=/opt/skin-nexus/skin-nexus
Restart=always

[Install]
WantedBy=multi-user.target
```

要点：工作目录须能读到 `web/dist` 与 `migrations/`（或用 `SKIN_MIGRATIONS_DIR` 指定）；
手动建表按序执行 `migrations/0001…0014`（以目录实际文件为准），或设 `SKIN_AUTO_MIGRATE=true` 让启动时自动应用。
Nginx 反代与 §三完全相同。日志 `journalctl -u skin-nexus -f`。

## 六、运维周期检查

- **弱哈希看板**：管理面板仪表盘 `weakHashUsers` 归零前保持关注（用户登录即惰性换 argon2id）
- **Union 开关**：管理面板「站点设置」热切换——关 = 全 union 路由 404 + 纹理签名回本地密钥；开 = 即时恢复。开启前须已有成员密钥；面板展示信任声明（Union 模式下纹理签名私钥全网共享）
- **主题/外观**：主题包放 `skin-data` 卷的 `themes/`（规范见 [theme-spec.md](theme-spec.md)）；自定义 CSS/JS 与 SEO 在管理面板对应区块
- **定期**：备份任务跑 §2.6 两条命令；证书到期前续期；`docker compose pull` 后看 CHANGELOG 再升级
