# SkinNexus / 肤枢

<p align="center"><img src="docs/assets/logo.svg" width="140" alt="SkinNexus 像素枢纽 N 标识"></p>

<p align="center">
  <a href="https://github.com/YimingZhanshen/skin-nexus/actions/workflows/ci.yml"><img src="https://github.com/YimingZhanshen/skin-nexus/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/YimingZhanshen/skin-nexus/releases/tag/v0.4.0"><img src="https://img.shields.io/badge/release-v0.4.0-blue" alt="v0.4.0"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-AGPL--3.0-green" alt="AGPL-3.0"></a>
</p>

> 自托管的 Minecraft 皮肤站：Yggdrasil 外置登录全兼容，可从 blessing-skin-server 无损迁移。

**SkinNexus（肤枢）** 是给 Minecraft 服务器与社团用的皮肤站。玩家在网页上传、管理皮肤与披风，通过启动器免密登录直接进服；站长自托管，账号、纹理与签名密钥都在自己手里。

它是 [blessing-skin-server](https://github.com/bs-community/blessing-skin-server) 的现代化重写（Go + Vue 3），已在一套真实生产环境完成全量迁移与切流：**67 用户 / 61 纹理零丢行、玩家 UUID 零漂移**。

- **当前版本**：v0.5.0（开发中，未发布）· 迭代历史见 [`docs/CHANGELOG.md`](docs/CHANGELOG.md)
- **许可**：AGPL-3.0 · **发起人 / 著作权人**：YMZS

## 10 分钟部署（Docker Compose）

镜像已发布在 GHCR（public，无需 `docker login`），下面的命令直接拉取镜像，无需克隆源码：

```bash
mkdir skin-nexus && cd skin-nexus
curl -fsSLO https://raw.githubusercontent.com/YimingZhanshen/skin-nexus/main/docker-compose.quickstart.yml
cat > .env <<EOF
# 改成你的公开地址（首次启动前定好）
SKIN_BASE_URL=https://skin.example.com
SKIN_DB_PASSWORD=$(openssl rand -hex 16)
SKIN_DB_ROOT_PASSWORD=$(openssl rand -hex 16)
SKIN_ADMIN_TOKEN=$(openssl rand -hex 32)
EOF
docker compose -f docker-compose.quickstart.yml up -d
```

打开 `http://<主机>:8080/setup`，完成站点名、管理员与皮肤域配置即可。

<details>
<summary><b>已有 MySQL？不启内置库，直接拉镜像运行</b></summary>

```bash
docker pull ghcr.io/yimingzhanshen/skin-nexus:latest
docker run -d --name skin-nexus --restart unless-stopped -p 8080:8080 \
  -v skin-data:/data \
  -e SKIN_DATA_DIR=/data \
  -e SKIN_BASE_URL=https://skin.example.com \
  -e SKIN_DATABASE_DSN='skin:PASS@tcp(你的MySQL:3306)/skin_nexus?parseTime=true&charset=utf8mb4' \
  -e SKIN_ADMIN_TOKEN=$(openssl rand -hex 32) \
  -e SKIN_AUTO_MIGRATE=true \
  ghcr.io/yimingzhanshen/skin-nexus:latest
```

</details>

**从 blessing-skin-server 无损迁移**：按 [`docs/migration.md`](docs/migration.md) 的 `preflight → dry-run → import` 执行，小站通常 10 分钟内跑完，全程可回滚。外部 MySQL、反向代理、升级与备份见 [`docs/deploy.md`](docs/deploy.md)。

## 界面预览

<p align="center"><img src="docs/assets/showcase/landing.jpg" alt="落地页" width="100%"></p>

<p align="center">
  <img src="docs/assets/showcase/skinlib.jpg" alt="皮肤库" width="49%">
  <img src="docs/assets/showcase/market.jpg" alt="插件市场" width="49%">
</p>

<p align="center"><img src="docs/assets/showcase/launcher-plugin.jpg" alt="Launcher API 插件" width="100%"></p>

> 截图来自真实生产实例；站名与敏感配置已替换，皮肤与昵称取自公开皮肤库。

## 为什么选 SkinNexus

- **迁移不丢数据**：主键保留、UUID 零漂移、密码全兼容（12 种旧算式 + argon2id 惰性重哈希）；`preflight → dry-run → import` 三步可回滚，不是「导 CSV 碰运气」。
- **登录链路现代化**：内置 OIDC 免密登录（设备码 + refresh 轮换），不再需要 Janus 外挂；SJMCL 等社区启动器的免密链路已实现，真机验收进行中。
- **运维看得见**：登录与联盟通信日志（90 天）、审计、认证限流、登录/认证/`hasJoined` 三层封禁、面板自助诊断。
- **内容能被搜到**：落地页与皮肤详情页服务端渲染，`sitemap.xml`、`robots.txt`、`llms.txt`、逐页 meta/OG/Twitter Card 与 WebSite/Organization/CreativeWork JSON-LD 全量注入——这是三家主流竞品都没有的完整能力。
- **部署与升级简单**：Go 单二进制或 Docker Compose；CI 每次 push `main` 自动构建镜像推 GHCR，部署机只拉不编。

性能实测（2 核 / 3.5 GB 生产 VPS，详见 CHANGELOG）：落地页 ~600 RPS、纹理分发 5182 RPS、Yggdrasil 元数据 2365 RPS，全程零错误。

<details>
<summary><b>与 blessing-skin-server / element-skin / vSkin 的逐项对照（源码实证）</b></summary>

> 每格结论均对照对方仓库源码复核（路由表 / grep，2026-10-25–26）；方法与逐条证据见 [`docs/competitor-analysis.md`](docs/competitor-analysis.md)。对手占优处照实标注，勘误就地修正并记入 CHANGELOG。

| 能力 | SkinNexus | blessing | element-skin | vSkin |
|---|---|---|---|---|
| SEO（sitemap / robots / 逐页 meta·OG·JSON-LD） | ✅ 全量 | ⚠️ 仅逐页 title + 全站共用 description | ❌ | ❌ |
| 服务端渲染内容页（免 JS 可收录） | ✅ 首页 + 皮肤详情页 | ⚠️ Twig 壳，正文客户端渲染 | ❌ SPA 壳 | ❌ SPA 壳 |
| 皮肤库单品页（可分享 URL） | ✅ `/skinlib/{tid}` | ✅ `/skinlib/show/{tid}` | ❌ 列表 + 弹窗 | ❌ 仅列表 |
| 启动器深链（PCL 系换肤/改密直达） | ✅ `/user/closet`·`/user/profile` | ✅ 约定发源地 | ❌ | ❌ |
| Mojang 官方兜底 | ✅ 出站防护 + 面板热开关 | ❌ | ✅ | ✅ |
| 找回密码 + SMTP | ✅ 防枚举 / 单次令牌 | ✅ | ✅ | ✅ |
| OIDC 免密登录 | ✅ 内置，设备码流 + 刷新轮换 | ⚠️ 需 Janus 外挂 | ⚠️ discovery 公告不全 | ⚠️ OAuth 细节弱 |
| MUA Union 联盟接入 | ✅ 内置可开关 + 出站同步 | ✅ 经插件 | ❌ | ⚠️ 仅配置壳 |
| blessing 一键迁移（ID 保全 / UUID 零漂移） | ✅ preflight→dry-run→import | —（迁移源头） | ❌ | ❌ |
| 封禁执行面 | ✅ 登录 / 认证 / hasJoined | ✅ 权限位 | ✅ | ❌ 仅 hasJoined |
| 运维观测面（登录 + 通信日志 + 审计） | ✅ 全链路面板可查 | ❌ | ⚠️ 仅权限审计 | ❌ |
| 部署形态 | Go 单二进制 / Docker，CI 直出镜像 | PHP 8.1 + Laravel 10（PHP 8.1 已 EOL） | Go + PostgreSQL + Redis | FastAPI + SQLite |

</details>

## 核心功能

**玩家侧**
- 注册 / 登录（可开启邀请码准入）、账号资料与密码管理
- 上传皮肤与披风（拖放上传、上传进度、3D 实时预览，高清皮保留原生分辨率）
- 材质名称、来源（原创/二创/转载，二创/转载必须填原始发布地址）与 Markdown 描述；支持私密上传
- 衣柜与角色管理、设为头像
- 皮肤库：来源徽章、可见性三档、筛选与排序、点赞即入柜；单品页可分享、编辑资料与举报
- 授权应用与设备管理、Union 成员站绑定、账号注销

**站长侧**
- 管理面板：概览统计、用户 / 角色 / 材质管理、邀请码、站点设置（分组化）、Union 运维、自助诊断
- 公告（Markdown 双层消毒）、自定义 CSS/JS、主题包切换、自定义页脚（页脚文字 + 友链/备案号链接，SSR 与 SPA 共用；版权行固定保留）
- 封禁三层执行、认证限流与失败锁定、SMTP 找回密码
- 登录与服务器/联盟通信日志（90 天保留）、操作审计

**兼容与集成**
- Yggdrasil API 全端点：authlib-injector 零改配置，UUID 零漂移
- OIDC 免密登录（设备码流 + refresh 轮换，另支持授权码）、MUA Union 成员站模块（默认关 = 零表面）
- CustomSkinAPI / USM / legacy 裸 PNG / 公开只读 API
- Mojang 官方兜底：本地未命中时回源官方 sessionserver（出站地址防护 + 面板热开关）
- 服务端头像与预览渲染：`/avatar/...`、`/preview/...` 输出 PNG

**外观与内容**
- 主题包体系（零构建：`theme.json` + CSS + 可选 JS）与 [AI 生成提示词模板](docs/theme-spec.md)；页脚文字/友链/备案链接由后台配置，支持主题覆盖；默认主题采用宽屏内容区（1200/1440）与自适应高度插件页
- 插件平台 v2（goja）：统一 hookApi 2 + 配置/密钥、命名空间数据、三档路由、事件/guard、sandbox iframe UI；官方有 UI 的插件已接入 `init.locale` 中英文切换，manifest `ui.i18n` 支持 `nav.<page-id>`；P2 起签名 bundle 安装/升级/回滚、单源签名市场与 `skin-plugin` 发布工具链；v0.5 新增受控出站 `net:http` 与持久任务 `schedule:jobs`；官方插件源码与市场仓库 [skin-nexus-plugins](https://github.com/YimingZhanshen/skin-nexus-plugins) 采用 PR 投稿 + CI 签名；`launcher-api` 提供启动器 Bearer API（[规范](docs/launcher-api.md)），`skin-importer` 提供 Mojang/Blessing 单条与批量导入；规范见 [docs/plugin-spec.md](docs/plugin-spec.md)
- 中英双语（zh-CN / en），语言包可扩展
- SEO：SSR 落地页与皮肤详情页、sitemap、robots、可整站 noindex（预发环境）

## 快速部署

**环境**：Docker 20+ / Compose v2；MySQL 5.7 或 8.0（compose 默认内置 8.0，也可接外部库）。

```bash
git clone https://github.com/YimingZhanshen/skin-nexus.git
cd skin-nexus

# 编辑 docker-compose.yml，至少改三处：
#   SKIN_BASE_URL      你的公开访问地址（首次启动前设定，会写入纹理 URL 与 OIDC issuer）
#   SKIN_DATABASE_DSN  数据库连接串（密码与下方 MYSQL_PASSWORD 保持一致）
#   SKIN_ADMIN_TOKEN   机器/API 管理令牌（openssl rand -hex 32）

docker compose up -d
```

首次启动后浏览器打开 `http://<主机>:8080/setup`（空库时访问任一页面也会自动进入向导），填写站点名、管理员账号与皮肤域即可完成安装。

- **升级**：`docker compose pull && docker compose up -d`（数据卷不动）
- **从 blessing-skin-server 迁移**：见 [`docs/migration.md`](docs/migration.md) 的 `preflight → dry-run → import`，全程可回滚
- **无 Docker / 源码构建**：见 [`docs/build.md`](docs/build.md) 与 [`docs/deploy.md`](docs/deploy.md) §五

**上线前必做**

1. 反向代理透传 `X-Forwarded-Proto` 与 `X-Forwarded-For`（否则协议头错误、全站共享一个限流桶）。
2. 备份签名密钥（Docker 在 `skin-data` 卷 `/data/sign-key.pem`，源码部署默认 `data/sign-key.pem`）——丢失会导致全网皮肤签名失效。

完整环境变量表、反代示例、备份恢复与排错见 [`docs/deploy.md`](docs/deploy.md)。

## 文档

| 文档 | 内容 |
|---|---|
| [`docs/deploy.md`](docs/deploy.md) | 部署手册：Compose 双数据库模式、环境变量全表、Nginx 反代、运维 |
| [`docs/build.md`](docs/build.md) | 构建指南：环境要求、源码构建、Docker 镜像、CI 产物、本地调试 |
| [`docs/migration.md`](docs/migration.md) | 从 blessing-skin-server 全量迁移：CLI 手册、对账断言、切流与回滚 |
| [`docs/compat-matrix.md`](docs/compat-matrix.md) | 兼容矩阵：blessing / yggdrasil-connect 逐端点实测基线 |
| [`docs/theme-spec.md`](docs/theme-spec.md) | 主题包规范与 AI 生成提示词模板 |
| [`docs/plugin-spec.md`](docs/plugin-spec.md) | 插件创作规范（goja 宿主 v2：配置/数据/路由/事件/guard/UI） |
| [`docs/plugin-publishing.md`](docs/plugin-publishing.md) | 签名 bundle、registry、GitHub Pages 发布与密钥保管 |
| [`docs/launcher-api.md`](docs/launcher-api.md) | 启动器 API v1（Bearer/OIDC、角色/纹理/皮肤库） |
| [`docs/competitor-analysis.md`](docs/competitor-analysis.md) | 竞品对照的源码证据与方法 |
| [`docs/CHANGELOG.md`](docs/CHANGELOG.md) | 迭代历史（按日期倒序） |
| [`CONTRIBUTING.md`](CONTRIBUTING.md) | 贡献指南：翻译 / 主题包 / 代码与文档 |
| [`AGENTS.md`](AGENTS.md) | 仓库地图、七条铁律与协作纪律（AI agent 首读） |
| [`GOVERNANCE.md`](GOVERNANCE.md) · [`CITATION.cff`](CITATION.cff) | 治理结构（BDFL）与引用格式 |
| [`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md) · [`SECURITY.md`](SECURITY.md) · [`ACCESSIBILITY.md`](ACCESSIBILITY.md) | 社区行为准则、漏洞报告流程、无障碍声明 |

## 路线图

| 状态 | 内容 |
|---|---|
| ✅ 已完成 | 一期–三期：Yggdrasil 全量、OIDC 免密、Union 模块、i18n / SEO / 主题包、迁移工具链、管理面板；生产全量迁移与切流；v0.2.0（自助找回 / 官方兜底 / 运维日志 / SEO）；v0.3.0（签名插件安装/市场/回滚 + 发布工具链）；v0.4.0（slot 全接线、注册/上传插件字段、头像 CDN、资料/改名 guard、能力内省、本地包信任分级） |
| 🔶 进行中 | v0.5.0：公共纹理哈希去重、受控出站、持久任务队列与 `skin-importer` 导入插件 |
| ⬜ 规划中 | 正版账号绑定（Auth Source 第二实现）、邮箱验证流、插件市场运营 / sidecar 隔离 / 第一方原生插件 |

## 兼容契约（不可破坏）

改动以下任意一项前，先读 [`AGENTS.md`](AGENTS.md) 铁律并跑对应契约测试：

1. `/api/yggdrasil/**` 全路径原语义（204 清单、错误体 `{error,errorMessage}`、毫秒时间戳、PEM 严格格式）
2. 玩家 UUID 零漂移（`OfflinePlayer:` md5 变体，改名不重算）
3. 持久化数据与 blessing-skin-server 存储约定同构可直迁
4. `internal/union` wire 契约与 `internal/plugin` hook 合约冻结

## 参与贡献

翻译、主题包、代码与文档都欢迎，见 [`CONTRIBUTING.md`](CONTRIBUTING.md)。治理为 BDFL，日常改动走 PR；AI 辅助贡献可以，但提交者对输出负全责（证据铁律）。

## 许可

AGPL-3.0（见 [`LICENSE`](LICENSE)）。版权行：`Copyright (c) 2026 YMZS (YimingZhanshen) and SkinNexus contributors`。
