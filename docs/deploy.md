# SkinNexus 部署手册（Docker 优先）

> 推荐所有站长用 **Docker Compose** 部署：升级只换容器、数据卷零丢失、免装运行时，
> 且 GitHub Actions 每次 push main 都会自动构建生产镜像推到 GHCR，部署机**只拉不编**。
> 从老站迁数据见 [migration.md](migration.md)；源码构建细节见 [build.md](build.md)。

## 一、部署方式选择

| 方式 | 适合 | 说明 |
|---|---|---|
| **A. Docker Compose（推荐）** | 绝大多数站长 | 拉镜像 → 改环境变量 → `up -d`；升级 `pull && up -d`；内置 MySQL 可选。机器上是宝塔面板的话走同一套，见 §2.8 |
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

### 2.2 选择数据库（同一份文件二选一）

`SKIN_DATABASE_DSN` 的前缀决定用哪种数据库，不另设开关：不带前缀是
`user:pass@tcp(host)/db` 形式的 MySQL/MariaDB DSN（历史默认，老部署不用改），
`sqlite:` 前缀是本机文件库，`postgres://` / `postgresql://` 前缀是 PostgreSQL。
建表 DDL 按引擎分三套（`migrations/mysql|sqlite|postgres/`），启动时按连接自己
那套走，`schema_migrations` 里记录本库由哪棵树迁移——把 SQLite 的连接指向一个
MySQL 建好的库会直接拒绝启动，而不是半途改坏表。

| 引擎 | DSN 写法 | 适合谁 | 要知道的事 |
| --- | --- | --- | --- |
| MySQL 5.7 / 8.0 | `skin:PASS@tcp(127.0.0.1:3306)/skin_nexus?parseTime=true&charset=utf8mb4` | 多进程、要备份与并发的站 | 必须带 `parseTime=true`（缺失直接启动失败，连接层也会强制补上） |
| MariaDB 10.11 / 11.4 | 同上（走 MySQL 线协议与同一个 driver） | 已有 MariaDB 的宿主机 | 与 MySQL 同一条 DSN 形状，无需前缀 |
| SQLite | `sqlite:/var/lib/skin-nexus/skin.db` | 单人小站、面板单机、想少一个容器 | 文件须落在持久卷上；库为 WAL + `busy_timeout=5000`，写者排队；一个进程持有连接池，别用第二个进程同时写 |
| PostgreSQL 14+ | `postgres://skin:PASS@127.0.0.1:5432/skin_nexus?sslmode=disable` | 已有 PG 的站 | 时间列是 `TIMESTAMPTZ`；从老站导入后序列会自动对齐（显式 id 导入是 PG 唯一需要额外处理的地方） |

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

外部库 MySQL 5.7 / 8.0 均可（5.7.44 实测通过）；MariaDB、SQLite、PostgreSQL 见
上面的引擎表，DSN 前缀即选择。空库启动时 `SKIN_AUTO_MIGRATE=true`
自动建表，随后首访站点进 `/setup` 安装向导（建管理员 / 站点名 / 皮肤域）。
向导要填安装令牌（见 §2.3 的 `SKIN_SETUP_TOKEN`）：容器一对外暴露就有人能提交这张表单，令牌把「谁先提交谁当超管」这段窗口关上。

用 SQLite 时没有外部数据库要装，也没有 `db` 服务：把 `SKIN_DATABASE_DSN` 写成
`sqlite:/绝对路径/skin.db`，并让该路径落在持久卷（compose 里挂一个卷进去即可），
注释掉 `db` 服务与 `depends_on`。数据量到几十万像素级别、或需要多个进程同时写
时，换 MySQL/PostgreSQL。

### 2.3 环境变量

取值优先级三层：**显式 env（compose / systemd / shell）＞ 镜像内 `ENV` 默认（`Dockerfile:49-52`）＞
源码默认（`server/internal/config/config.go`）**。因此下表「默认」列同时给出两个数：源码默认只对
源码构建 + systemd 生效，Docker 部署看到的是镜像内值。

**必改三项**

| 变量 | 说明 |
|---|---|
| `SKIN_BASE_URL` | 站点公开地址，如 `https://skin.example.com`，也是面板 `site_url` 为空时的回退。**建议在首次 `up -d` 前定成最终访问地址**：签名密钥与已分发的材质地址不会随改地址而失效，但地址一变，新旧材质 URL 会同时存在。纹理 URL 与页面链接在**启动时**解析一次（option `site_url` → 本 env），改面板值要重启才全量生效；`SKIN_OIDC_ISSUER` 默认跟本 env、不跟 `site_url`。ALI 响应头与此无关（按请求 Host + `X-Forwarded-Proto` 现算）。安装向导写的就是面板 `site_url`（早于本版本的向导写的是 `base_url`，它仍夹在 `site_url` 与本 env 之间生效，见 migration.md） |
| `SKIN_DATABASE_DSN` | 外部库模式必改；内置库模式保持默认。前缀决定引擎（见 §2.2），MySQL/MariaDB 形式**必须带 `parseTime=true`**（缺失直接启动失败） |
| `SKIN_ADMIN_TOKEN` | `openssl rand -hex 32` 生成；机器/API 调用管理接口的回退凭据（管理面板本身用管理员账号登录即可，无需此 token；留空 = 机器 token 通道关闭） |

其余变量**默认值即可跑通**，逐项列全（与代码逐条对齐；条数不写在这里，`check-docs` 会双向比对环境变量集合，写死的数字只会漂）：

**监听与运行标签**

| 变量 | 默认（源码 / 镜像内） | 说明 |
|---|---|---|
| `SKIN_LISTEN` | `:8080` / `:8080` | 进程监听地址，**保持 `:8080` 即可**，改值见表下注 |
| `SKIN_ENV` | `production` / `production` | 部署环境标签（production/demo/staging…），仅用于管理面板「运行状态」展示，不改变行为 |

> `SKIN_LISTEN` 改值注意：bridge 网络下不要改成 `127.0.0.1:8080`（进程只绑容器环回 → 端口映射
> connection refused，而 healthcheck 仍报 healthy，极难排查）；反代场景想只对本机开放就改 ports
> 绑定为 `127.0.0.1:8080:8080`；仅 `network_mode: host` 时才建议 `127.0.0.1:8080`。镜像 HEALTHCHECK
> 固定探 `127.0.0.1:8080`，改端口需同步改 healthcheck。

**站点标识与注册门**（大多可在「管理 → 站点设置」改，优先级见 §2.3 末）

| 变量 | 默认 | 说明 |
|---|---|---|
| `SKIN_SITE_NAME` | `SkinNexus` | 站点名（面板可改） |
| `SKIN_TRUSTED_PROXIES` | 空（= loopback + RFC1918 全信任） | 逗号分隔的 CIDR 列表，指定**哪些对端可以声称** `X-Forwarded-For`。留空沿用历史行为（同机 nginx / docker bridge 场景不用改）；一旦配置，只有列出的网段被信任，其余对端来的 XFF 一律不信。写错（非 CIDR）直接启动失败，而不是悄悄放宽信任——认证限流与审计里的 IP 全靠这条 |
| `SKIN_SETUP_TOKEN` | 空（每次启动临时生成并打进日志） | 安装向导的一次性令牌。空库时 `/api/v1/setup` 谁先提交谁当超管，所以这一步要一个只有运维者拿得到的值：留空则本次启动随机生成并在日志里打印 `setup: ... install token "xxxxxxxxxx"`，`docker compose logs skin-nexus` 可见；自己定一个则跨重启不变。填错只回 403，不写任何数据；站点初始化后本变量不再起作用。**另一面也要知道**：随机源不可用时（`crypto/rand` 失败）既没有你的令牌也没有随机令牌，向导就是**裸开的**，此时唯一的提示是启动日志里那条 `setup: WARNING the install wizard is unguarded`——把站点暴露出去之前先 grep 这一行；自己设 `SKIN_SETUP_TOKEN` 就不依赖随机源 |
| `SKIN_SERVER_NAME` | `SkinNexus` | Yggdrasil 元数据里的服务器显示名（面板可改） |
| `SKIN_SKIN_DOMAINS` | 空 | 皮肤域白名单，逗号分隔；不接联盟填本站域名，接入 MUA Union 以联盟公布清单为准（改动会破坏联盟互认） |
| `SKIN_INVITE_REQUIRED` | `false` | 注册强制邀请码（等价老站必填语义）；面板「站点设置」可改 |
| `SKIN_REQUIRE_VERIFICATION` | `false` | join 邮箱验证门（按 `email_verified` 判定）；面板可热切。开启后新注册账号会自动收到验证邮件，宽限期由面板 `email_verify_grace_days` 决定（默认 0=立即执行）——迁移站务必先设宽限或先核对存量标志位，否则老号一律进不了服 |

**认证、限流与正版兜底**

| 变量 | 默认 | 说明 |
|---|---|---|
| `SKIN_RATE_LIMIT_AUTH` | `120` | 认证接口限流：每 IP 10 分钟滑动窗口内的请求数 |
| `SKIN_LOGIN_FAIL_LIMIT` | `5` | 连续登录失败锁定次数 |
| `SKIN_MOJANG_FALLBACK` | `1`（开） | profile/hasJoined 本地未命中时向 Mojang 官方 sessionserver 发站外查询兜底；不希望站外请求就设 `0`（面板可热切） |

**SMTP 找回密码邮件流**（未配置时注册/找回不提供邮箱通道；面板「站点设置」同样可配）

| 变量 | 默认 | 说明 |
|---|---|---|
| `SKIN_SMTP_HOST` | 无 | SMTP 服务器地址 |
| `SKIN_SMTP_PORT` | `465` | SMTP 端口（465 = TLS） |
| `SKIN_SMTP_USER` | 无 | SMTP 账号 |
| `SKIN_SMTP_PASS` | 无 | SMTP 口令（凭据，只走 env） |
| `SKIN_SMTP_FROM` | 无 | 发件地址 |

**启动器免密登录（yggc / OIDC）**

| 变量 | 默认（源码 / 镜像内） | 说明 |
|---|---|---|
| `SKIN_OIDC_ENABLED` | `false` | 打开 OIDC 端点与发现文档 |
| `SKIN_OIDC_ISSUER` | 跟 `SKIN_BASE_URL` | issuer；只对显式值生效 |
| `SKIN_OIDC_SIGN_KEY` | `<data>/oidc-key.pem` / `/data/oidc-key.pem` | OIDC JWT 签名私钥，**需备份** |
| `SKIN_OIDC_SHARED_CLIENT_ID` | `2` | 站内共享 OAuth client id，一般不动 |

**MUA Union 联盟模块**（真源在「管理 → 站点设置」；env 仅作首次初始值 / 面板为空时回退）

| 变量 | 默认（源码 / 镜像内） | 说明 |
|---|---|---|
| `SKIN_UNION_ENABLED` | `false` | 联盟总开关；关 = 零路由零表面。面板另有 `union_enabled` 开关（这就是「env 七键 / 面板八键」的差） |
| `SKIN_UNION_API_ROOT` | `https://skin.mualliance.ltd/api/union` | 联盟 hub API 根 |
| `SKIN_UNION_HUB_ORIGIN` | `https://skin.mualliance.ltd` | hub 站点源（跨域/回调用） |
| `SKIN_UNION_MEMBER_KEY` | 无 | 成员站凭据（联盟发放，凭据只走 env/面板） |
| `SKIN_UNION_HOST_PUBKEY` | `<data>/union-host-pubkey.pem` | 联盟验签用的 hub 公钥文件 |
| `SKIN_UNION_OAUTH2_SIG_KEY` | `<data>/union-oauth2-sig.pem` | OAuth2 grant 签名私钥，**需备份** |
| `SKIN_UNION_ALLOW_UPDATEBACKENDKEY` | `false` | 是否允许 hub 下发更新成员 key |
| `SKIN_UNION_QUERYEMAIL_MODE` | `full` | 联盟邮箱查询应答：`full` = 返回邮箱、`off` = 403 拒绝；其他值按 `off` 处理（fail-closed） |

**数据路径与密钥**（`<data>` = `SKIN_DATA_DIR`；镜像内 `/data`，源码默认 `./data`）

| 变量 | 默认（源码 / 镜像内） | 说明 |
|---|---|---|
| `SKIN_DATA_DIR` | `./data` / `/data` | 统一数据根，textures/、themes/、各类密钥都在它下面 |
| `SKIN_TEXTURES_DIR` | `<data>/textures` | 材质存储目录 |
| `SKIN_THEMES_DIR` | `<data>/themes` | 主题包目录（市场安装与本地上传都落这里） |
| `SKIN_PLUGINS_DIR` | `<data>/plugins` | 插件包目录 |
| `SKIN_SIGN_KEY` | `<data>/sign-key.pem` | Yggdrasil 纹理签名私钥，**丢失 = 全网皮肤失效，务必纳入定期备份** |
| `SKIN_PLUGIN_STATE_DIR` | `<data>/plugins/.state` | 安装/回滚缓存，**需备份** |
| `SKIN_PLUGIN_SECRET_KEY` | 无 → 自动生成 `<data>/plugin-secrets.key` | 插件配置密钥的主密钥（AES-256-GCM），**需一并备份** |
| `SKIN_LEGACY_ENV` | 无 | **从老站迁移且用户密码非 bcrypt/argon2 PHC 格式时必设**：指向挂载进容器的老站 `.env`（盐链来源），否则这些存量用户无法登录（见 [migration.md](migration.md)） |

**建表与迁移**

| 变量 | 默认（源码 / 镜像内） | 说明 |
|---|---|---|
| `SKIN_AUTO_MIGRATE` | `false` / `true` | 追踪式建表/补表：`schema_migrations` 逐文件记标，空库全量、既有库自动补新文件（树里最新一号见 `migrations/<引擎>/`，三套引擎同号对齐） |
| `SKIN_MIGRATIONS_DIR` | `./migrations` / `/app/migrations` | 迁移树根目录，启动时按连接引擎取 `<目录>/<mysql\|sqlite\|postgres>/` |

**插件市场与主题市场**（语义一致：默认值编译内置，零配置即可用）

| 变量 | 默认 | 说明 |
|---|---|---|
| `SKIN_PLUGIN_MARKET_URL` | 官方 `…/skin-nexus-plugins/v1/registry.json` | 签名 registry；**显式置空 = 关闭插件市场**；下游 fork 改成自己的 registry |
| `SKIN_PLUGIN_TRUST_KEYS` | 官方公钥（编译内置，`market-2026-10:<pub>`） | `keyId:base64pub,...`；**显式置空 = 拒绝一切市场/签名安装**；换源时填自己的公钥 |
| `SKIN_PLUGIN_UPLOAD_MAX` | `67108864`（64 MiB） | 插件包上传上限（也是 multipart 的 body 预算） |
| `SKIN_THEME_MARKET_URL` | 官方 `…/skin-nexus-plugins/themes/v1/registry.json` | 主题签名 registry；**显式置空 = 关闭主题市场浏览** |
| `SKIN_THEME_TRUST_KEYS` | 官方公钥（编译内置，`theme-market-2026-11:<pub>`） | 与插件市场**独立密钥**；**显式置空 = 市场安装关闭**，后台本地上传仍可用。官方公钥另见分发仓库 `v1/keys.json` |

> - 「未设置」与「设为空串」在两个市场 URL/TRUST_KEYS 上是**不同**语义（`os.LookupEnv`）：未设置 → 官方默认；空串 → 关闭。
>   其余布尔/路径类变量走 `getenv`，空串等同于未设置。
> - 主题包上传上限固定 16 MiB（`internal/thememkt`），无对应 env。
> - 市场安装只认签名与摘要，不认 CI 状态；registry 有 90 天有效期，到期续签由分发仓库 CI 每周自动完成
>   （剩余 <60 天即重签推送），站长无需动手；细节见 [plugin-publishing.md](plugin-publishing.md) §7。

**compose 文件专用**（不是进程变量，由 `docker-compose*.yml` 展开）

| 变量 | 默认 | 说明 |
|---|---|---|
| `SKIN_IMAGE_TAG` | `latest` | 镜像标签；固定版本用 `sha-<commit>`，回滚 = 改回旧 sha 再 `up -d` |
| `SKIN_PORT` | `8080` | 宿主机发布端口（quickstart 文件用） |
| `SKIN_DB_PASSWORD` | 必填（`:?` 守卫） | 内置库业务账号口令 |
| `SKIN_DB_ROOT_PASSWORD` | 必填（`:?` 守卫） | 内置库 root 口令 |

集成测试专用的 `SKIN_TEST_DSN` 见 [build.md](build.md)，不属部署面。

**env 与管理面板的优先级（分键，别想当然）**

站点类设置大多已迁「管理 → 站点设置」（站点名 / 皮肤域 / 邀请码 / 验证门 / mail 五键 / ygg 六键 /
Union 八键），但生效优先级按键区分：

| 项 | 谁赢 | 备注 |
|---|---|---|
| `site_url` | 面板 option | 影响页面链接、sitemap、**纹理/预览/头像 URL**（含 ygg 签名里的那串），重启后一起变；OIDC issuer 仍只认 `SKIN_OIDC_ISSUER`/`SKIN_BASE_URL` |
| `site_name` / `server_name` / `skin_domains` | **显式 env** | 想在面板改这三项就别在 compose 里设对应 env（compose 默认已不设） |
| `union_enabled` / `union_api_root` / `union_member_key` | 面板，且热切换立即生效 | 其余站点设置重启生效 |
| DSN、`SKIN_ADMIN_TOKEN`、插件密钥加密键 | 只走 env | 面板不落库；SMTP 口令在面板可写但**只写不读**（见下） |

散点变量说明：`SKIN_ADMIN_TOKEN`、`SKIN_SMTP_*`、`SKIN_MOJANG_FALLBACK`、`SKIN_LEGACY_ENV`、
`SKIN_PLUGIN_SECRET_KEY` **不在 `config.go`**（分别在 `adminapi`、`main.go` 里直接 `os.Getenv`）——
`config.go` 是主入口但不是唯一清单，完整真源以本节表为准（与代码逐条对齐；集合由 `check-docs` 双向比对，
所以这里不写「共几个」这种一改就漂的数字）。

### 2.4 启动与验收

```bash
docker compose up -d
docker compose ps            # 等两个服务 healthy（镜像内置 HEALTHCHECK 探 /healthz，DB 探活）
curl -s http://127.0.0.1:8080/api/yggdrasil/ | head -c 300   # 元数据 JSON
# 浏览器打开 /setup（或任一 SPA 路由）→ 空库自动进安装向导
```

首启链路（空库）：自动迁移建表（`SKIN_AUTO_MIGRATE=true`）→ 访问 `/setup`（或任一 SPA 路由）自动进入向导
（建管理员/站点名/皮肤域）→ **向导是一次性门**：完成后永久 404，向导里建的账号即**超级管理员**（`permission=2`）。
若目标库已有用户（如迁移导入后），向导自动跳过（users>0 即视为已初始化）；此时管理员
需在导入前用向导创建，或导入后用 SQL/管理接口补建——顺序详见 [migration.md](migration.md)。
旧版本建的站点若只有 `permission=1` 管理员、没有任何超管，启动时会幂等提升最老的管理员为超管并记录 uid。

> 向导最后一步会把你填的**站点地址**与环境变量 `SKIN_BASE_URL` 摆在一起，不一致时会明确警告。
> 该地址写进面板 `site_url`（管理 → 设置 → 站点地址），优先级高于 env：用局域网 IP 或
> `http://localhost:8080` 完成向导，站点就会用它拼所有材质链接——客户端多半取不到图，
> 表现是「皮肤站正常、进服没皮肤」。当场改最省事，事后在面板里改完**重启容器**即可。

> 端口收敛建议：8080 只对反代放开（ports 写 `127.0.0.1:8080:8080` 或防火墙收敛）——
> 同宿主/同网桥的其它容器处于 XFF 信任区，可伪造最后一跳绕过限流。

### 2.5 升级

```bash
docker compose pull && docker compose up -d    # 只换容器，卷不动，数据零丢失
# 新版会自动补齐新 DDL（追踪式迁移）；回滚 = image 改回旧 :sha-* 再执行同命令
```

> **回滚只换代码、不回滚 schema**：迁移引擎只进不退，新 DDL 已落库且旧镜像会继续用它
> （加列无感，但删列/改语义的迁移会让旧代码出错）——破坏性变更前先做 §2.6 备份。

> **升到「手臂模型按像素判定」的版本后补做一次存量校正**（0.5.x 起）：旧数据里的 `model`
> 来自上传者当时的声明，可能与像素不符。先用同一套 `SKIN_*` 环境变量试运行看差异条数，
> 确认后再写库——校正会同时改变游戏内手臂宽度、`/avatar`、`/preview` 与皮肤库 steve/alex
> 筛选的结果，玩家可感知，所以默认不自动执行：
>
> ```bash
> docker compose run --rm skin-nexus redetect-models            # 只报告，不写库
> docker compose run --rm skin-nexus redetect-models --apply    # 按报告结果写库
> ```
>
> 命令幂等，可反复执行；大库可先 `--limit 500` 试一段。文件缺失或不可解码的材质只计数、不猜测。

> **升到「会话与启动器令牌只存摘要」的版本：这一次升级不可回滚**。新版首次启动会在一个事务里把
> `web_sessions.token`、`ygg_tokens.access_token`、`ygg_join_sessions.access_token` 三处凭证列改写为
> `hex(sha256(原值))`，并在同一事务里落一枚
> `options` 标记；启动日志给出改写行数（`auth: rewrote N stored token(s) as digests (this happens once)`），
> 只跑一次。摘要不可逆：代码退回旧版后，库里已经取不出原文，比对一律落空，**所有网页会话与启动器登录都要
> 重新登录一次**（账号密码不受影响），且无法用代码修复。要退回只有两条路——接受重登，或按 §2.6 从升级前的
> 备份恢复。所以 `pull` 之前先备份。
>
> 客户端手里那把原文不受影响：比对与查库都走同一套摘要，升级瞬间仍在线的会话继续可用（含 `validate` /
> `refresh` / `join` / `hasJoined`）。`ygg_tokens.client_token` 故意保持原文——它单独不能认证任何事
> （用到它的端点都还要求一把有效 accessToken），而 Yggdrasil 要求服务端把它原样回吐，摘要形式做不到。
> 升级后可核对：`fsck` 会输出 `credentials_at_rest rows=N digest_migrated=true`。

> **材质目录对账 `fsck`**（缺省只读）：报「有文件无行」的孤儿字节、「有行无文件」的悬空引用、
> 只被墓碑引用的违规文件，以及 `AUTOINCREMENT` 之前留下的行 id 错配格。`--fix` 只 unlink
> **既无行也无墓碑**的地址——被墓碑引用的是违规判定的最后一份物证，不自动删；`--deep`
> 再把每个文件重哈希与文件名比对：
>
> ```bash
> docker compose run --rm skin-nexus fsck          # 只报告
> docker compose run --rm skin-nexus fsck --fix    # 只清无引用的孤儿文件
> docker compose run --rm skin-nexus fsck --deep   # 连内容是否与文件名相符一起验
> ```
>
> ⚠️ SQLite 没有跨进程 advisory lock，跑 `--fix` 前先停服：命令内部会「在锁内重读一次确认仍无
> 行引用」，而并发上传进程看不见这把锁（MySQL / PostgreSQL 走引擎级 advisory lock，不受此限）。
> 回收过的行 id 只报不修——墓碑与活行不可能都对，该留哪一个只有站长说得清。

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
| 玩家说游戏里没有皮肤，网页上一切正常 | 先按顺序排三件事：①启动器有没有接外置登录（地址填的是 `/api/yggdrasil`）②站点地址是不是对外可访问的（面板「运行状态 → 部署自检」的「站点地址」一行会把进程值 / option / env 并排吐出来）③该账号是否还没通过邮箱验证（开了验证门时 join 会被拒，网页登录不受影响）。玩家侧的同一份说明在 `/help` |
| 注册/登录突然全量被拒 | 认证限流与失败锁定按 IP 与账号分别计数（`SKIN_RATE_LIMIT_AUTH`、`SKIN_LOGIN_FAIL_LIMIT`）。**反代后没转发真实 IP 时，所有玩家共享网关那一个 IP**——先按 §三 CDN/反代小节把头交付回去，再看是否被限；「请 N 秒后再试」是账号锁定，等或让管理员重置 |
| 找回密码邮件收不到 | 面板「运行状态 → 部署自检 → 邮件服务」：没配 SMTP 会直说「站点还没有配置发信账号，请先在设置里填好 SMTP」；只填了服务器地址、没填发件地址会报红（这一种最坑：面板能保存成功，但一封都发不出去）。同处点「发送测试邮件」真发一封到你自己账号的邮箱；失败的原始原因写进服务日志与审计。挂死的 SMTP 现在最迟 12s 给出结论（面板那侧 20s 就会放弃，等太久只会让人以为没反应） |
| 上传材质被拒：内容曾被判定违规 | 一份字节被判违规并彻底删除后就永久封禁，重传（含私有导入、含换了账号）统一 403 `texture.violation_reupload`。普通删除不受影响：只有删除时带违规判定的才会。误判要解禁得在数据侧处理——面板故意没做这个入口，解除判定需要说清是谁撤了谁的结论（见 [`compat-matrix.md`](compat-matrix.md) §4） |
| 首次启动就 fatal `no *.sql found in migrations/…` | 裸二进制的工作目录没指对：迁移树默认 `./migrations`（相对启动目录）。给 `SKIN_MIGRATIONS_DIR` 一个**绝对路径的树根**（不是 `sqlite`/`mysql` 子目录），或按 §五 把工作目录设在能同时读到 `web/dist` 与 `migrations/` 的位置。Docker 镜像里路径是内置的，不受影响 |
| 安装向导提交后回 503「数据库尚未建表」 | 忘了开 `SKIN_AUTO_MIGRATE=true`（旧行为是撞在第一条 INSERT 上、回一句「昵称或邮箱已被占用」，把人往错的方向带）。启动时自动建表，或先跑一次 `skin-nexus migrate`；此时站点什么都没写，开好再重填向导即可 |

### 2.8 用宝塔面板管这一套（Docker 管理器）

面板的「Docker」菜单只是 `docker` + `docker compose` 的一层壳，**镜像与卷与 §2.1~§2.5 完全同一套**，
不存在「面板版」与「命令行版」两种部署。已经用面板装好 Docker 的机器，直接照下面走。

1. **放文件**：目录自己定，示例用 `/opt/skin-nexus`。把 §2.1 的
   `docker-compose.yml` 与该文件同目录的 `.env` 放进去（`.env` 里至少写 `SKIN_BASE_URL`、`SKIN_IMAGE_TAG`、
   数据库口令）。
2. **起服务**：面板「Docker → Compose」里**只列在面板中创建的项目**；命令行建的目录不会出现在列表里，
   这不是故障。在项目目录里执行一次即可，之后仍然可以用面板启停容器：

   ```bash
   cd /opt/skin-nexus && docker compose up -d
   docker compose ps                       # 应用是 Up，内置 db 是 Up (healthy)
   ```

   （镜像里没给应用容器挂 healthcheck，健康检查只加在 db 上；验证应用本身用
   `docker compose exec skin-nexus wget -qO- http://127.0.0.1:8080/healthz`，见 §2.7。）

3. **域名与证书**：面板「网站 → 添加站点」建一个**纯静态**站点绑域名，然后在站点设置里
   「反向代理 → 添加反向代理」，目标 URL `http://127.0.0.1:8080`，发送域名 `$host`。
   证书走站点设置「SSL → Let's Encrypt」，需要域名已解析到本机且 80 端口可达；签完再打开「强制 HTTPS」。
   反代头与真实 IP 的写法与 §三 完全一致（`X-Forwarded-Proto` 决定 Ali-Texture 地址是否 https）。
4. **反代之后不要把 8080 摊在公网**：仓库自带的 `docker-compose.yml` 默认写的是 `'8080:8080'`（所有网卡），
   文件里那行注释给的就是面板场景的写法——把它改成 `'127.0.0.1:8080:8080'` 再 `up -d`，玩家只从 443 进来。
   否则玩家实际拿到的是无 TLS 的站点，而且绕过反代会让 `SKIN_CDN_*` 那组设置失效。
   面板的系统防火墙/安全组里放行的也只需要 80/443。
5. **更新与回退**：`.env` 里把 `SKIN_IMAGE_TAG` 钉成 `sha-<完整提交号>` 而不是 `latest`，
   出问题时能说清线上是哪个提交（CI 只在 `main` 推送时构建镜像，打 tag 不触发，所以 `latest` 只反映提交）。

   ```bash
   cd /opt/skin-nexus
   cp .env .env.bak                                    # 改配置前先备份
   sed -i 's/^SKIN_IMAGE_TAG=.*/SKIN_IMAGE_TAG=sha-新提交号/' .env
   docker compose pull skin-nexus && docker compose up -d
   docker compose logs --tail 30 skin-nexus            # 迁移水位有无报错
   ```

   升级后先到管理面板「运行状态 → 部署自检」看六项是否全绿，尤其是「数据库迁移」；
   回退只需把 tag 改回旧提交号再 `up -d`（数据在 named volume 里，跟镜像无关）。
6. **日志**：面板「Docker → 容器 → 日志」等价于 `docker compose logs -f skin-nexus`。

> 注意 `.env` 的行尾：面板编辑器保存下来的文件可能没有行尾换行，用 `echo 'K=V' >> .env` 会把上一条粘进同
> 一行（`sed -i` 改单行不受影响）。改完 `cat -A .env | tail -5` 确认一眼，省一次启动失败。

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
> 都会记成同一个值（切流后追溯困难）。服务端只在对端处于信任区时才采信 XFF 的
> **最后一跳**（防伪造绕限流）：默认信任区是 loopback + RFC1918，够同机反代用；容器端口
> 若被别的机器摸得到，请用 `SKIN_TRUSTED_PROXIES` 收成实际反代网段。所以反代必须用
> `$proxy_add_x_forwarded_for` 或 `$remote_addr` 追加真实地址，不要透传客户端原样头。
>
> `X-Real-IP` 默认不读取；开启 CDN 开关后才按配置的头顺序读取（见下）。

### 安全响应头（服务端已内置，反代不必再加）

每个响应都带：`X-Content-Type-Options: nosniff`、`Referrer-Policy: same-origin`、
`X-Frame-Options: DENY`、`Permissions-Policy`（相机/麦克风/支付/USB 等本站用得到的能力一律关掉），
以及一条 `Content-Security-Policy`：脚本与样式只允许同源（外加内联，见下）、图片同源或 http(s)、
`frame-ancestors 'none'`（禁被嵌套，与 `X-Frame-Options` 同义）、`object-src 'none'`、
`base-uri 'self'`、`form-action 'self'`。

**CSP 有意允许 `'unsafe-inline'` 的 script/style**：「自定义 CSS/JS」「主题包」「插件 bundle」
是站长主动安装的功能，都靠内联。所以这条策略不是「装了恶意插件包也能挡住」的防线，
而是其余一切的边界。要上严格 CSP（nonce/hash）就得同时放弃内联自定义 JS，
那是产品取舍，不在这里顺手改。

`/plugin-assets/` 与 `/theme-assets/` 里**以 HTML 文档返回**的响应额外带
`Sandbox: allow-scripts allow-forms`（与面板 iframe 的 `sandbox` 同一组标记）：
有人直接访问第三方包里的 HTML 时，那份文档跑在隔离源里，带不上本站会话。
CSS/JS 这类子资源**不带**这个头——浏览器对「子资源的 sandbox 作用在谁身上」语义并不一致，
不如只处理确定有意义的那一类。

反代/CDN 若也加同名头，值是**追加**而不是替换，同一份响应出现两条 CSP 时浏览器取交集，
容易把自己锁死。要么只在服务端配，要么只在最外层配一处。

### CDN 场景（Cloudflare / 阿里云 CDN 等）

CDN → Nginx → 容器时，XFF 最后一跳通常是 CDN 节点，不是玩家。后台
**安全与限流 → CDN 加速开关**打开后，服务端按「CDN 真实 IP 头」列表从前往后
匹配第一个合法 IP：

```
CF-Connecting-IP, True-Client-IP, Ali-CDN-Real-IP, X-Real-IP, X-Forwarded-For
```

- 头列表可在面板里增删/排序；只接受字母、数字、连字符，拒绝 `Authorization`/`Cookie` 等。
- 仍只在对端为 loopback/RFC1918 时采信，直连公网伪造头无效。
- `X-Forwarded-For` 仍取最后一跳，所以有 CDN 专用头时优先用专用头。
- 配置即时生效，无需重启。

Nginx 侧建议显式覆盖，避免客户端伪造透传：

```nginx
proxy_set_header CF-Connecting-IP $http_cf_connecting_ip;
proxy_set_header True-Client-IP $http_true_client_ip;
proxy_set_header Ali-CDN-Real-IP $http_ali_cdn_real_ip;
```

> 只有确认 Nginx/CDN 会覆盖这些头时才开启开关；若站点直接暴露 8080，
> 客户端可以自带同名头，不要启用。

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

导入把老站的 options 全表原样搬过来，写法是「先到先得」（同键已存在就跳过，不覆盖）。因此老站若带着
`site_url` / `base_url`，它们会盖在你的 `SKIN_BASE_URL` 之上——迁移完成后到「管理 → 设置 → 站点地址」核对，
改成最终域名或直接留空回退 env，改完重启容器。细节与断言见 [migration.md](migration.md)。

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
手动建表按序执行 `migrations/<引擎>/0001…` 直到目录里最后一个文件（以 `ls migrations/<引擎>` 实际输出为准），或设 `SKIN_AUTO_MIGRATE=true` 让启动时自动应用。
Nginx 反代与 §三完全相同。日志 `journalctl -u skin-nexus -f`。

## 六、运维周期检查

- **弱哈希看板**：管理面板仪表盘 `weakHashUsers` 归零前保持关注（用户登录即惰性换 argon2id）
- **部署自检**：管理面板「运行状态 → 部署自检」六项（站点地址 / 迁移水位 / 邮件 / 预览渲染 / 前端资源 / 插件签名密钥），改完配置点「重新检查」即可，不用重启也不用翻日志；机器接法 `GET /api/admin/v1/diagnostics`（带 `X-Admin-Token`）
- **测试发信**：同页「邮件服务」一行有「发送测试邮件」，收件人恒为**当前管理员账号自己的邮箱**（端点没有收件人字段，不做中继），失败会直接把 SMTP 的拒绝原因报出来并写进审计。只在网页会话下可用，`X-Admin-Token` 会被拒；未配置 SMTP 时明确回答「站点未配置邮件服务」而不是静默
- **Union 开关**：管理面板「站点设置」热切换——关 = 全 union 路由 404 + 纹理签名回本地密钥；开 = 即时恢复。开启前须已有成员密钥；面板展示信任声明（Union 模式下纹理签名私钥全网共享）
- **主题/外观**：主题包放 `skin-data` 卷的 `themes/`（规范见 [theme-spec.md](theme-spec.md)）；自定义 CSS/JS 与 SEO 在管理面板对应区块
- **定期**：备份任务跑 §2.6 两条命令；证书到期前续期；`docker compose pull` 后看 CHANGELOG 再升级

### 6.1 base 镜像与依赖的更新节奏

这套东西将来是交给「下一个接手的人」的，所以更新规则写成清单，不靠谁临时想起来。

| 层 | 当前值 | 支持窗口 | 谁负责升 |
|---|---|---|---|
| 运行时 | `alpine:3.24` | Alpine 分支约 2 年（3.24 至 2028-06） | 维护者，分支到期前换 tag 提 PR |
| Go 构建器 | `golang:1.26-alpine` | 1.26.x 补丁随 Go 发布 | 浮动 tag + CI `pull: true`，重建即最新补丁 |
| Node 构建器 | `node:22-alpine` | Node 22 LTS | 同上 |
| 数据库 | `mysql:8.0`（内置模式） | Oracle 维护窗口 | 站长自选；外部库（MySQL / MariaDB / PostgreSQL）与 SQLite 文件库同理，见 §2.2 |

**为什么构建层浮动、运行层钉死**：构建层不进镜像，重构建一次就是补丁——`docker/build-push-action`
里显式开了 `pull: true`，否则 buildx 缓存会拿旧 base，「重建即补丁」就不成立了。运行层是玩家实际拿到的
那层，钉在受支持分支上才能追溯；具体构建版本不用猜，管理面板「运行状态」直接报 `goVersion` 与 `mysqlVersion`。

**每季度一次（15 分钟）**：

```bash
docker build --pull -t skin-nexus:audit .        # 用最新 base 重建一次，看是否报错
docker run --rm skin-nexus:audit --version       # 版本号
# 面板「运行状态」核对 goVersion / mysqlVersion / migration，再看「部署自检」六项是否全绿
```

再对照上游：Go 的 release notes、Alpine 安全公告、`cd web && npm audit --audit-level=high`
（CI 只把 critical 设为阻塞，high 写进 step summary——修 high 常要大版本升级，不能混进功能轮）。

**依赖更新已自动化**：`.github/dependabot.yml` 每周一（Asia/Shanghai）检查四类更新并开 PR——
go.mod（`server/`）、npm（`web/`）、Dockerfile 的 `FROM` 行、GitHub Actions。minor/patch 合并成每个
生态一条 PR，major 单独开（所以 vue-i18n 9→11 这种大版本会自己冒出来，不会淹没在小更新里）。
PR 走的是同一套 CI，覆盖率地板与结构闸都在拦。

一个覆盖不到的地方：`docker-compose.yml` 里的 `mysql:8.0` **不在** dependabot 的 docker 生态范围内
（它只读 Dockerfile），数据库镜像仍要按上面的季度自检手动核。

