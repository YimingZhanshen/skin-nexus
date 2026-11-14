# SkinNexus 构建指南

> 目标读者：想从源码构建 SkinNexus 的皮肤站站长、贡献者与 AI agent。
> 部署与环境变量见 [`deploy.md`](deploy.md)；从老站迁数据见 [`migration.md`](migration.md)。

## 一、环境要求

| 工具 | 版本 | 说明 |
|---|---|---|
| Go | ≥ 1.26（`server/go.mod` 的 `go` 指令） | 低于该版本时 Go 工具链会按 `GOTOOLCHAIN` 自动下载；国内建议 `GOPROXY=https://goproxy.cn,direct` |
| Node.js | ≥ 22 | 前端构建（CI 与 Dockerfile 均用 22） |
| npm | 随 Node | `npm ci` 按 lockfile 严格安装 |
| Docker（可选） | 20+ / Compose v2 | 路径 A 部署与镜像构建 |
| 数据库 | MySQL 5.7 / 8.0、MariaDB 10.11 / 11.4、PostgreSQL 14+、SQLite | 四家都由 CI 的 `-tags integration` 存储层往返矩阵跑通；MySQL 5.7.44 另有生产实测。构建侧不需要任何数据库服务 |

不需要 CGO、不需要任何数据库客户端库（MySQL / PostgreSQL / SQLite 三个 driver 都是纯 Go）、不需要云服务。

## 二、源码构建（单二进制）

```bash
git clone https://github.com/YimingZhanshen/skin-nexus.git
cd skin-nexus/server

# 构建（Linux/树莓派/NAS 交叉编译示例）
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w" -o skin-nexus ./cmd/skin-nexus

# 自检（合并门禁 = 全部绿）
go vet ./...
go test ./...                       # 单元 + 契约测试
SKIN_TEST_DSN='user:pass@tcp(127.0.0.1:3306)/skin_nexus_test?parseTime=true' \
  go test -tags integration ./internal/store/   # 数据库往返集成测试（DSN 前缀选引擎；不带标签时跑纯单元与 SQLite 引擎用例）
```

产物 `skin-nexus` 是单二进制，运行时需要同级（或工作目录下）的：

- `web/dist/` —— 前端构建产物（缺失时站点返回占位页，API 仍可用）
- `migrations/` —— DDL 目录（`SKIN_MIGRATIONS_DIR` 默认 `./migrations`；二进制工作目录须能读到，Docker 镜像内固定在 `/app/migrations`）

## 三、前端构建

```bash
cd web
npm ci            # 严格按 package-lock.json
npm run build     # 产物 web/dist
npm run dev       # 本地开发（Vite 热更新；API 代理按 vite.config.js）
```

前端主体是静态 SPA（Vue 3 + Vite + vue-i18n + skinview3d），构建产物由后端在 `/` 挂载。
但「纯静态」这个说法不成立：三个页面由 Go 直接渲染 HTML，不走 SPA —— 落地页 `GET /`
（`renderLanding`）、材质详情页 `GET /skinlib/{tid}`（`texturePageHandler`）、皮肤库列表面向
爬虫的预渲染（`prerenderSkinlib`）。它们的文案吃 `internal/webi18n` 的目录，不是
`web/src/locales/`，改这几页的中文要同时动 Go 侧的注入点。

文案平价由 `scripts/check-locales.mjs`（CI 的 web job）钉住，三处一起查：SPA 词典
`web/src/locales/`、后端错误码目录（`internal/i18n` + `internal/account` 声明的码）、服务端渲染页的
`internal/webi18n` 目录。规则是两边键集相等、每个后端码都有中英词条、渲染模板里不留未入表的中文。

## 四、Docker 构建与镜像

仓库根的 `Dockerfile` 是多阶段构建（每轮 CI 实证）：

1. `node:22-alpine` —— `npm ci && npm run build` 出 `web/dist`
2. `golang:1.26-alpine` —— `CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=... -X main.commit=... -X main.buildTime=... -X main.buildBranch=..."` 出单二进制（镜像内 `GOTOOLCHAIN=local`，构建标签须 ≥ go.mod 指令版本）；CI 自动注入版本、commit、构建时间和分支，显示在后台「运行状态」
3. `alpine:3.20` 运行时 —— 仅 ca-certificates + tzdata，`VOLUME /data`，内置 HEALTHCHECK 探 `/healthz`

```bash
docker build -t skin-nexus:latest .   # 本地源码构建（离线/内网场景才需要）
```

**正常部署不需要本地构建**——compose 默认拉 GHCR 现成镜像（见 §五）；只有离线/内网
环境才把 compose 的 `image:` 换回 `build: .` 后 `docker compose up -d --build`。

`/data` 卷承载全部可变数据（textures/、sign-key.pem、oidc-key.pem、themes/、union 密钥），升级只换容器，卷不动。

## 五、CI 产物（推荐部署来源）

`.github/workflows/ci.yml` 定义 10 个 job，push 与 PR 各跑 9 个（`docker` 仅 push、`docker-build` 仅 PR；integration 三腿内含矩阵，实际容器数更多）：

| job | 触发 | 内容 |
|---|---|---|
| server | 两者 | `gofmt` 空输出 + `go build` + `go vet`（含 `-tags integration`）+ `go test ./...` 覆盖率地板与 0% 包集合比对 + 插件工具链测试 |
| integration | 两者 | MySQL 8.0 / 5.7 各跑 `-p 1 -tags integration ./internal/store/ ./internal/account/`。`-p 1` 是必需的：两套用例共用同一个库，而 store 那套在迁移前会把所有表 DROP 掉，并行时互相打断（sqlite、postgres、mariadb 三条腿同一签名报错）。5.7 专抓版本差异 SQL（login COLLATE / prepared 1267） |
| integration-mariadb | 两者 | MariaDB 11.4 / 10.11，同一条命令；抓只有 MySQL 自家扩展能吃下的写法 |
| integration-portable | 两者 | PostgreSQL 16 与 SQLite 文件库，同一条命令；抓方言与序列差异 |
| web | 两者 | `npm ci` + `check-locales.mjs` + `check-modal-chrome.mjs` + `check-a11y.mjs`（th scope / table caption / img alt）+ `npm run build` + `npm audit --audit-level=high`（low 及以上另写 step summary，不阻断） |
| codesql | 两者 | CodeQL（go + javascript-typescript），告警进 security tab 作审查队列，非发布门禁 |
| docs | 两者 | `check-docs.mjs`：死链 / 表格 / 快照 dating / env 双向真源；`check-theme-contract.mjs`：`theme-spec.md` §3 契约默认值与 `theme-base.css` 逐键比对 + 主题包对宿主令牌的悬空引用（包在姊妹仓，本 checkout 里没有时明确打印跳过而不算通过） |
| zizmor | 两者 | workflow 自身卫生（模板注入、未固定第三方 action），`continue-on-error`，非阻断 |
| docker-build | **仅 PR** | `docker build --pull`，不登录 registry、不推送、不打 tag、不写 registry 缓存——只证明多阶段 Dockerfile 仍可构建 |
| docker | **仅 push** | 多阶段镜像推 GHCR：`ghcr.io/yimingzhanshen/skin-nexus:latest` 与 `:sha-<commit>`（registry + GHA 缓存） |

`./internal/account/` 挂在三条真库腿上：它的复用名测试是 UUID 零漂移铁律唯一的自动化回归
（改名后旧名仍持有原 OfflineUUID，重建同名角色必须答 409 而非 500），且跨引擎成立——
`dbx.IsDupKey` 对 MySQL/MariaDB/PostgreSQL/SQLite 的唯一键冲突都能归类。

server/web 两个 job 启用 Go modules 与 npm 依赖缓存（`cache-dependency-path`），
workflow 级 `concurrency` 会在同一 ref 有新推送时取消旧运行，避免排队浪费额度。

**部署机推荐只拉不编**（compose 已默认 `pull_policy: always`）：

```bash
docker compose pull && docker compose up -d    # 首装与升级同命令，数据卷不动
# 固定版本/回滚：把 compose 的 image 改为 :sha-<commit> 标签再执行同命令
```

仓库不存放任何手工打包的部署产物（二进制/前端快照/tar 包一律不入库）——
GHCR 镜像即唯一部署产物来源，避免产物与代码版本漂移。

## 六、本地开发联调

```bash
# 后端：最小必需环境变量只有 DSN；空库 + SKIN_AUTO_MIGRATE 自动建表
cd server
SKIN_DATABASE_DSN='root:root@tcp(127.0.0.1:3306)/skin_nexus_dev?parseTime=true' \
SKIN_AUTO_MIGRATE=true \
go run ./cmd/skin-nexus            # 默认 :8080

# 前端：另开终端跑 Vite dev server，浏览器直连 5173（代理到 8080）
cd web && npm run dev
```

常用开关（全表见 [`deploy.md`](deploy.md)）：`SKIN_OIDC_ENABLED`（免密登录）、`SKIN_UNION_ENABLED`（联盟模块，默认关）、`SKIN_INVITE_REQUIRED`（注册强制邀请码）。

## 七、构建排错

| 现象 | 原因与处理 |
|---|---|
| `go: go.mod requires go >= 1.26` | 本机 Go 太旧且 `GOTOOLCHAIN=local`；升级 Go 或放开 `GOTOOLCHAIN=auto` |
| 页面显示 “web/dist not built yet” | 后端工作目录下没有 `web/dist`；先构建前端或改用 Docker 镜像（内含产物） |
| 启动即报 migration 失败 | `SKIN_MIGRATIONS_DIR` 指不到 DDL 目录；从仓库根启动时指向 `server/migrations` |
| `npm run build` 中途报 Vite 语法/特性错误 | Node < 22（package.json 未设 `engines`，`npm ci` 不会提前拦截）；升级 Node 22（nvm/fnm 皆可） |
| `go test -race` 报无法使用 | 无 CGO 环境不支持 `-race`；改普通 `go test` + 并发用例覆盖 |

## 八、给 AI agent 的最短路径

```bash
cd server && go build ./... && go vet ./... && go test ./...   # 后端改动自检
cd web && npm ci && npm run build                              # 前端改动自检
```

改动 Yggdrasil 面必须额外跑 `go test ./internal/ygg/` 且不动契约断言；改动 union 包前先读该包 doc 注的冻结区说明（[`AGENTS.md`](../AGENTS.md) 铁律 1/6）。


## 插件平台自检

```bash
cd server
go test ./internal/plugin/ ./internal/signedpkg/ ./internal/outbound/
go test -tags integration ./internal/store/   # 需 SKIN_TEST_DSN
```

市场相关环境变量见 [`deploy.md`](deploy.md) §2.3（插件：`SKIN_PLUGIN_MARKET_URL` / `SKIN_PLUGIN_TRUST_KEYS` /
`SKIN_PLUGIN_STATE_DIR` / `SKIN_PLUGIN_UPLOAD_MAX`；主题：`SKIN_THEME_MARKET_URL` / `SKIN_THEME_TRUST_KEYS`，
默认值编译内置，显式置空才关闭）。

签名打包工具子命令全集：`go run ./cmd/skin-plugin keygen | pack | pack-theme | lint | verify | registry | verify-registry`
（`registry --kind theme` 生成主题 registry）。完整发布流程见 [`plugin-publishing.md`](plugin-publishing.md)。
