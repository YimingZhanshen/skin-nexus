<div align="center">

# SkinNexus / 肤枢

[English](README.md)

<img src="docs/assets/logo.svg" width="140" alt="SkinNexus 像素枢纽 N 标识">

> 给 Minecraft 玩家管理角色形象的网站程序——自己架设，账号、皮肤、密码都在你自己手里。

[![CI](https://github.com/YimingZhanshen/skin-nexus/actions/workflows/ci.yml/badge.svg)](https://github.com/YimingZhanshen/skin-nexus/actions/workflows/ci.yml) [![release](https://img.shields.io/badge/release-v0.6.0-blue)](https://github.com/YimingZhanshen/skin-nexus/releases/tag/v0.6.0) [![license](https://img.shields.io/badge/license-AGPL--3.0-green)](LICENSE)

[它是什么](#它是什么) · [比老站好在哪](#比老站好在哪) · [部署](#部署) · [功能](#功能) · [文档](https://docs.skin.yimingzhanshen.cn/) · [许可](#许可)

</div>

---

<p align="center">
  <img src="https://img.shields.io/badge/-老站搬家·不丢账号不丢皮肤-2ea44f?style=for-the-badge" alt="老站搬家：不丢账号、不丢皮肤">
  <img src="https://img.shields.io/badge/-发现漏洞·自己修当天发-b0453a?style=for-the-badge" alt="发现漏洞：自己修、当天发新版">
  <img src="https://img.shields.io/badge/-启动器登录·免密一键进服-1d76db?style=for-the-badge" alt="启动器登录：免密一键进服">
  <img src="https://img.shields.io/badge/-架站简单·升级不丢数据-6f42c1?style=for-the-badge" alt="架站简单：升级不丢数据">
</p>

## 它是什么

**SkinNexus 是一个给 Minecraft 玩家管理「游戏形象」的网站。** 玩家在网页上上传皮肤和披风、给角色换装、逛皮肤库挑形象，然后用启动器免密登录直接进游戏服务器。网站架在你自己的服务器上——玩家的账号、皮肤、密码全部存在你自己的机器里。

**blessing-skin-server（圈内常叫 bs 或老站）是这个品类里最老牌的程序。** 你现在玩的皮肤站，大概率就是它架的。它生态最大、教程最多，但技术底子是十年前的 PHP——出安全漏洞常常等不到官方修复，功能也很多年没有大更新。

**SkinNexus 就是为老站用户做的「新一代替代」。** 最重要的一件事：从老站搬过来**什么都不丢**——账号、皮肤披风、玩家在游戏里的身份、老密码一样不少，玩家几乎感觉不到换过站。搬完之后，下面这些是实打实的改变。

## 比老站好在哪

| 你可能遇到过的坑 | 老站 blessing-skin-server | SkinNexus |
|---|---|---|
| 技术太老，出漏洞没人修 | 十年前的 PHP 底子，只能等官方 | 新技术重写，漏洞自己修、当天发新版 |
| 换站怕丢数据 | — | 账号 / 皮肤 / 游戏身份 / 旧密码全保留 |
| 启动器免密登录要折腾 | 得额外装登录组件 | 内置，装好即用 |
| 一件皮肤出问题连累大家 | 一删了之，收藏的人跟着遭殃 | 先清点引用、留证，再删 |
| 玩家搜不到你的站 | 页面对搜索引擎不友好 | 搜索引擎、AI 助手都读得懂 |
| 部署运维费劲 | PHP 环境一堆讲究 | 两条命令起站，升级不动数据 |

一台 2 核 / 3.5GB 的小服务器实测：皮肤图片每秒响应 5182 次、首页每秒 600 次访问，全程零错误。逐格对比证据见 [竞品分析](https://docs.skin.yimingzhanshen.cn/competitor-analysis)。

## 部署

镜像在 GHCR（公开，不需要 `docker login`），不用克隆源码：

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

打开 `http://<主机>:8080/setup`，填站点名、管理员账号、皮肤域名，以及上面那个 `SKIN_SETUP_TOKEN`——空库时谁先提交这张表单谁就是超级管理员，所以这一步要的正是只有你拿得到的值。以后升级就是 `docker compose pull && docker compose up -d`，数据永远不动。

上线前两件事：

1. 用了 Nginx / 宝塔这类反向代理的话，记得转发 `X-Forwarded-Proto` 和 `X-Forwarded-For` 两个请求头（照抄[部署文档](https://docs.skin.yimingzhanshen.cn/deploy)即可），否则网址识别和登录保护会出问题。
2. 备份签名密钥：数据卷里的 `/data/sign-key.pem`。丢了它，玩家已下载的皮肤图会全部验签失败。

更多姿势（外部数据库、裸机部署、迁移老站）都在 [docs.skin.yimingzhanshen.cn](https://docs.skin.yimingzhanshen.cn/deploy)；从 blessing-skin-server 搬家看[迁移指南](https://docs.skin.yimingzhanshen.cn/migration)（先预检、再预演、后搬家，可回滚）。

## 功能

- **玩家用得到的**——网页传皮肤/披风（拖进去就行，带 3D 预览）、给角色换装、逛皮肤库、收藏喜欢的形象、举报不良内容、管理登录设备。
- **站长用得到的**——后台管玩家和内容、发公告、一键换网站主题（可装卸、立即生效）、封禁与防撞库、找回密码 / 邮箱验证、完整操作记录。玩家的登录凭证在数据库里只存「指纹」，就算数据库被拖走也拿不到能用的登录态。
- **接游戏用到的**——authlib-injector 等主流启动器开箱即用；免密登录内置（不需要再装别的组件）；可加入 MUA 联盟与其他皮肤站互通；支持正版账号兜底、老格式兼容、皮肤批量导入。
- **门面与扩展**——中英双语、免构建换肤（主题包）、插件平台、搜索引擎与 AI 优化（让玩家搜得到你）。

技术上：Go 单二进制 + Vue 3，数据库支持 MySQL / MariaDB / PostgreSQL / SQLite 四选一。

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

当前版本 v0.6.0；迭代历史见 [`docs/CHANGELOG.md`](docs/CHANGELOG.md)。详细文档以中文为主。

## 冻结的兼容契约（给开发者）

动这四条之前，先读 [`AGENTS.md`](AGENTS.md) 的铁律并跑对应的契约测试：

1. `/api/yggdrasil/**` 的原语义（204 清单、`{error,errorMessage}` 错误体、毫秒时间戳、PEM 严格格式）
2. 玩家 UUID 零漂移（`OfflinePlayer:` md5 变体，改名永不重算）
3. 持久化数据与 blessing-skin-server 存储约定同构、可直迁
4. `internal/union` 的 wire 契约与 `internal/plugin` 的 hook 契约冻结

## 许可

AGPL-3.0（见 [`LICENSE`](LICENSE)）。`Copyright (c) 2026 YMZS (YimingZhanshen) and SkinNexus contributors`。
欢迎翻译、主题包、代码与文档贡献——见 [`CONTRIBUTING.md`](CONTRIBUTING.md)。治理模式 BDFL，变更走 PR。
