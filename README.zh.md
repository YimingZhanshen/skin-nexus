<div align="center">

# SkinNexus / 肤枢

[English](README.md)

<img src="docs/assets/logo.svg" width="140" alt="SkinNexus 像素枢纽 N 标识">

> 自托管的 Minecraft 皮肤站。账号、纹理文件和签名密钥，都在你自己机器上。

[![CI](https://github.com/YimingZhanshen/skin-nexus/actions/workflows/ci.yml/badge.svg)](https://github.com/YimingZhanshen/skin-nexus/actions/workflows/ci.yml) [![release](https://img.shields.io/badge/release-v0.6.0-blue)](https://github.com/YimingZhanshen/skin-nexus/releases/tag/v0.6.0) [![license](https://img.shields.io/badge/license-AGPL--3.0-green)](LICENSE)

[部署](#部署) · [功能](#功能) · [文档](https://docs.skin.yimingzhanshen.cn/) · [参与贡献](CONTRIBUTING.md) · [许可](#许可)

</div>

---

Go 单二进制 + Vue 3，数据库可选 MySQL / MariaDB / PostgreSQL / SQLite。玩家在网页上传管理皮肤与披风，用启动器免密登录直接进服——不需要 Mojang 账号。

它是 **blessing-skin-server 的替代方案**：协议层兼容 [blessing-skin-server](https://github.com/bs-community/blessing-skin-server) 及其插件生态，老站可以无损迁移——主键保留、玩家 UUID 不变、旧密码算式继续可用。已在一套真实生产环境完成迁移与切流：67 用户 / 61 纹理零丢行。

**→ 其余一切在 [docs.skin.yimingzhanshen.cn](https://docs.skin.yimingzhanshen.cn/)**——部署、迁移、插件/主题规范、兼容矩阵、启动器 API。本 README 是门，文档站才是屋子。

当前版本 v0.6.0（2026-11-12 发布）；`main` 已完成下一个开发周期，打 tag 由站长拍板。迭代历史见 [`docs/CHANGELOG.md`](docs/CHANGELOG.md)。

## 部署

镜像在 GHCR（public，不需要 `docker login`），不用克隆源码：

```bash
mkdir skin-nexus && cd skin-nexus
curl -fsSLO https://raw.githubusercontent.com/YimingZhanshen/skin-nexus/main/docker-compose.quickstart.yml
cat > .env <<EOF
SKIN_BASE_URL=https://skin.example.com
SKIN_DB_PASSWORD=$(openssl rand -hex 16)
SKIN_DB_ROOT_PASSWORD=$(openssl rand -hex 16)
SKIN_ADMIN_TOKEN=$(openssl rand -hex 32)
SKIN_SETUP_TOKEN=$(openssl rand -hex 16)
EOF
docker compose -f docker-compose.quickstart.yml up -d
```

打开 `http://<主机>:8080/setup`，填站点名、管理员账号、皮肤域，以及上面那个 `SKIN_SETUP_TOKEN`——空库时谁先提交这张表单谁就是超管，所以这一步要的正是只有你拿得到的值。升级就是 `docker compose pull && docker compose up -d`，数据卷永远不动。

quickstart 之外的：外部数据库、裸二进制、反代、备份——[部署手册](https://docs.skin.yimingzhanshen.cn/deploy)。从 blessing-skin-server 迁数据——[迁移指南](https://docs.skin.yimingzhanshen.cn/migration)（preflight → dry-run → import，可回滚）。

上线前两件事：

1. 反代必须转发 `X-Forwarded-Proto` 与 `X-Forwarded-For`——否则协议判定失效，全站共享一个限流桶。
2. 备份签名密钥（`skin-data` 卷里的 `/data/sign-key.pem`）。丢了它，已下发的每一张纹理的签名全部失效。

## 功能

- **玩家**——皮肤/披风上传（拖放、3D 预览、高清皮保留原生分辨率）、来源标注（原创/二创/转载）、私密上传、衣柜与角色管理、可分享的皮肤库详情页、举报与站内收件箱、OAuth 应用与设备管理。
- **站长**——管理面板（用户/角色/材质/邀请码/运行状态/自助诊断）、公告、自定义 CSS/JS、主题包与主题市场（Ed25519 验签、装卸热生效）、三层封禁、认证限流与锁定、SMTP 找回密码 + 邮箱验证、操作审计。会话与启动器令牌只存摘要——读到库不等于拿到能用的登录态。
- **协议**——Yggdrasil 全端点（authlib-injector 开箱即用）、内置 OIDC 免密登录（设备码流，无 sidecar）、MUA Union 成员站模块（默认关 = 零暴露面）、CustomSkinAPI / USM / 旧版路由、Mojang 正版兜底、服务端头像与预览渲染、启动器 Bearer API、皮肤导入（Mojang / 老站，单条与批量）。
- **内容与扩展**——中英双语、零构建主题包（`theme.json` + CSS）、签名插件平台（goja 宿主：配置、命名空间数据、分档路由、事件、guard、iframe UI）、SEO（服务端渲染页面、sitemap、robots、llms.txt、逐页 meta + JSON-LD）。

2 核 / 3.5GB 生产 VPS 实测：落地页 ~600 RPS、纹理分发 5182 RPS、Yggdrasil 元数据 2365 RPS，全程零错误。

## 为什么是它

服务端渲染 SEO、可分享的材质详情 URL、闭环的材质生命周期、构建期强制的错误契约——四个对照项目没有一家四样全有。逐格源码证据（`文件:行`，快照 2026-11-01）见 [竞品分析](https://docs.skin.yimingzhanshen.cn/competitor-analysis)。

## 文档

| 我想…… | 去这里 |
|---|---|
| 部署、环境变量、反代、备份 | [docs.skin.yimingzhanshen.cn/deploy](https://docs.skin.yimingzhanshen.cn/deploy) |
| 从 blessing-skin-server 迁移 | [docs.skin.yimingzhanshen.cn/migration](https://docs.skin.yimingzhanshen.cn/migration) |
| 从源码构建 / 看懂 CI | [docs.skin.yimingzhanshen.cn/build](https://docs.skin.yimingzhanshen.cn/build) |
| 查协议兼容情况 | [docs.skin.yimingzhanshen.cn/compat-matrix](https://docs.skin.yimingzhanshen.cn/compat-matrix) |
| 写插件 / 写主题 | [插件规范](https://docs.skin.yimingzhanshen.cn/plugin-spec) · [主题规范](https://docs.skin.yimingzhanshen.cn/theme-spec) |
| 发布与签名扩展 | [docs.skin.yimingzhanshen.cn/plugin-publishing](https://docs.skin.yimingzhanshen.cn/plugin-publishing) |
| 启动器对接 | [docs.skin.yimingzhanshen.cn/launcher-api](https://docs.skin.yimingzhanshen.cn/launcher-api) |
| 翻知识手册 | [docs.skin.yimingzhanshen.cn/handbook](https://docs.skin.yimingzhanshen.cn/handbook/) |

仓库内文档：[`docs/deploy.md`](docs/deploy.md) · [`docs/build.md`](docs/build.md) · [`docs/migration.md`](docs/migration.md) · [`docs/CHANGELOG.md`](docs/CHANGELOG.md)。详细文档以中文为主。

## 冻结的兼容契约

动这四条之前，先读 [`AGENTS.md`](AGENTS.md) 的铁律并跑对应的契约测试：

1. `/api/yggdrasil/**` 的原语义（204 清单、`{error,errorMessage}` 错误体、毫秒时间戳、PEM 严格格式）
2. 玩家 UUID 零漂移（`OfflinePlayer:` md5 变体，改名永不重算）
3. 持久化数据与 blessing-skin-server 存储约定同构、可直迁
4. `internal/union` 的 wire 契约与 `internal/plugin` 的 hook 契约冻结

## 许可

AGPL-3.0（见 [`LICENSE`](LICENSE)）。`Copyright (c) 2026 YMZS (YimingZhanshen) and SkinNexus contributors`。
欢迎翻译、主题包、代码与文档贡献——见 [`CONTRIBUTING.md`](CONTRIBUTING.md)。治理模式 BDFL，变更走 PR。
