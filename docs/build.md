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
| MySQL | 5.7 或 8.0 | 5.7.44 / 8.0 均实测通过（外部库或 compose 内置） |

不需要 CGO、不需要 MySQL 客户端库（纯 Go driver）、不需要任何云服务。

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
  go test -tags integration ./internal/store/   # MySQL 往返集成测试（需本地可达的 MySQL）
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

前端为纯静态 SPA（Vue 3 + Vite + vue-i18n + skinview3d），构建后由后端在 `/` 挂载。

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

`.github/workflows/ci.yml` 定义 4 个 job（PR 跑 3 个——docker 仅 push main 触发；integration 含双矩阵腿）：

| job | 内容 |
|---|---|
| server | `go build` + `go vet` + `go test ./...` |
| integration | MySQL **8.0 + 5.7 双矩阵**各跑一遍 `-tags integration` 存储层往返（5.7 专抓版本差异 SQL 问题） |
| web | `npm ci && npm run build` |
| docker | 多阶段镜像推 GHCR：`ghcr.io/yimingzhanshen/skin-nexus:latest` 与 `:sha-<commit>`（GHA 缓存；仅 push main 触发推送，PR 构建不推镜像） |

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

插件市场/安装相关环境变量见 `docs/deploy.md`（`SKIN_PLUGIN_MARKET_URL`、`SKIN_PLUGIN_TRUST_KEYS`、`SKIN_PLUGIN_STATE_DIR`）。
签名打包工具：`go run ./cmd/skin-plugin keygen|pack|registry|verify|verify-registry`，完整流程见 `docs/plugin-publishing.md`。
