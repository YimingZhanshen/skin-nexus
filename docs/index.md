---
layout: home
title: SkinNexus — 自托管 Minecraft 皮肤站
description: Self-hosted Minecraft skin server in one Go binary — Yggdrasil external auth, lossless migration from blessing-skin-server, signed plugin and theme market.

hero:
  name: SkinNexus / 肤枢
  text: 自托管 Minecraft 皮肤站
  tagline: Go 单二进制 · Yggdrasil 外置登录 · 从 blessing-skin-server 无损迁移，玩家 UUID 与旧密码全保留
  actions:
    - text: 快速开始
      link: /deploy
      theme: brand
    - text: 迁移指南
      link: /migration
      theme: alt
    - text: GitHub
      link: https://github.com/YimingZhanshen/skin-nexus
      theme: alt

features:
  - title: 无损迁移
    details: 从 blessing-skin-server 一键迁入：主键保留、玩家 UUID 不变、旧密码算式继续可验证。preflight 到 dry-run 到 import 三步可回滚，另附虚构数据演练夹具。
  - title: 外置登录开箱即用
    details: Yggdrasil 协议兼容 authlib-injector，启动器免密登录直接进服；内置 OIDC 提供者（含设备码流程），支持 SJMCL 等启动器。
  - title: 部署只有一件事
    details: Go 单二进制或 Docker 一条命令；数据库四选一（MySQL / MariaDB / PostgreSQL / SQLite），小站用 SQLite 就能活，没有构建链要伺候。
  - title: 站长工具齐
    details: 管理面板带审核与举报处置、审计日志、诊断模式；材质生命周期闭环（像素定模型、违规处置、引用清点后删除留证）。
  - title: 插件与主题生态
    details: Ed25519 签名包分发的插件市场与主题市场；主题支持亮暗切换与整站换肤，插件可挂路由、UI 插槽与后台任务。
  - title: 对搜索引擎友好
    details: 首页与材质详情页服务端渲染，sitemap 与 llms.txt 齐备；本站文档即部署在 Cloudflare Pages 的 VitePress 站点。
---

## 文档导航

- [部署运维](/deploy) —— Docker 或裸二进制上线、环境变量全表、反代与备份
- [从 blessing-skin-server 迁移](/migration) —— 三步迁移手册与回滚
- [知识手册](/handbook/index) —— 80 篇架构与模块文档（含架构图）
- [插件规范](/plugin-spec) · [主题规范](/theme-spec) · [启动器 API](/launcher-api)
- [竞品源码分析](/competitor-analysis) · [变更日志](/CHANGELOG) · [迁移演练夹具](https://github.com/YimingZhanshen/skin-nexus/tree/main/demo)

**English**: SkinNexus is a self-hosted Minecraft skin server in one Go binary — Yggdrasil
external auth for authlib-injector, a lossless migration path from blessing-skin-server
(primary keys, player UUIDs and legacy password hashes preserved), and a signed plugin and
theme market. Deploy with Docker or a bare binary on MySQL / MariaDB / PostgreSQL / SQLite.
