# 竞品源码分析

> 结论先行：功能面没有一家对手同时具备我们手里的四件套；真正的品类缺口是
> **对象存储/CDN ＞ 账号安全深度 ＞ 服务端接入面**。
>
> 取证口径：浅克隆到 `refs/`（不入库）后源码 grep，格内 `文件:行` 为对方仓库内定位；
> 未找到写 `NOT FOUND`，不猜；「未取证」≠ 对方没有。README 宣称与源码不符处单独标注。
> 正文取证 2026-11-01，覆盖率复测 11-09，状态校正与增补 11-15（历史快照见 git 历史）。
> 与 blessing 的协议兼容事实不在本文，见 [`compat-matrix.md`](compat-matrix.md) 与 [`migration.md`](migration.md)。

**许可证边界**：只吸收概念与公开协议事实；禁止复制、翻译改写 GPL/AGPL 竞品源码、译文或测试
fixture；引用一律注明对方路径与行号。

## 竞品名录

| 仓库 | 栈 | 许可 | HEAD | 一句话定位 |
|---|---|---|---|---|
| [blessing-skin-server](https://github.com/blessing-skin/blessing-skin-server) | PHP Laravel + MySQL | MIT | 2026-03-30 | 品类事实标准，生态与教程存量十年 |
| [Drasl](https://github.com/Unmojang/Drasl) | Go + SQLite/MySQL | GPL-3.0 | 2026-11-01 | Go 自托管同类，工程纪律标杆 |
| [chrly（Ely.by 皮肤系统）](https://github.com/elyby/chrly) | Go + Redis | Apache-2.0 | 2024-07-09 | 在役最大 Yggdrasil 生态的皮肤单件，纹理签名见长 |
| [HRPAuth](https://github.com/CoreMatch/HRPAuth) | Go + MySQL + Redis | AGPL-3.0 | 2026-11-13 | 认证核心 + OAuth2 中心，MFA 全家桶 |
| [element-skin](https://github.com/water2004/element-skin) | Go + PostgreSQL + Redis + Vue 3 | MIT | 2026-10-25 | 功能面广，覆盖率闸 90% |
| [vSkin](https://github.com/iJunecn/vSkin) | Python FastAPI + Vue 3 + SQLite | GPL-3.0 | 2026-05-29 | 基本停更，反面教材价值为主 |
| [PigeonSkin-Server](https://github.com/Pigeon-Server/PigeonSkin-Server) | 见仓（Cloudflare 免费服务方案） | 未取证 | 见仓 | 新入场者，已占通用搜索结果位 |
| [LittleSkin premium-verification](https://github.com/xuckee/premium-verification) 等生态件 | PHP | AGPL/GPL | 见仓 | CN 事实标准的正版资格校验件 |

## 总判断

- **功能面**：SEO 服务端渲染体系、启动器深链兼容、材质生命周期闭环（像素定模型 → 违规处置 →
  引用清点后彻底删除并留证）、签名插件市场与任务运行器——没有一家同时具备，我方四项皆有。
  差距不在功能列表，在**可发布性与契约完整性**（矩阵「错误契约 / 覆盖率 / 公共配置」三行）。
- **品类缺口（按量级）**：
  1. 对象存储 + CDN 刷新——各家全无（§矩阵首两行）。我方以 `immutable` + 可改写 URL 的插件
     filter 换取 5182 RPS 的纹理分发，代价记在 `compat-matrix.md` §4。
  2. 账号安全深度（TOTP / WebAuthn·passkey / 邮箱二要素 / 恢复密钥）——HRPAuth 一家拉满并经
     源码核验，其余各家与我方都停在「密码 + 邮箱验证」层面（§重量级补录）。
  3. 不挂 authlib-injector 的接入面（services / discovery / certificates 族）——Drasl 整族挂载；
     chrly `/textures/signed` 与 HRPAuth-Spigot 是同一方向的另一种形态。
- **伪需求**：签名密钥轮换（authlib-injector 无轮换消费路径，规范亦不建议换 key）、像素级材质
  hash（与 blessing 迁移字节对账冲突，`internal/migrate/scan.go:66-80`）、three.js 全景首页
  （伤 LCP 与 SSR 优势冲突）、换 PostgreSQL/Redis 栈（单站规模无收益）。
- **已补的历史缺口**：邮箱验证流与站内通知中心曾列此处，2026-11-02 落地（矩阵 §账号 对应行）。

## 能力矩阵

图例：✅ 有并可用 · ⚠️ 有但残缺/有坑 · ❌ 确认没有 · — 不适用 ·「未取证」本次没查。

### 材质与存储

| 能力 | SkinNexus | element-skin | vSkin | blessing | Drasl |
|---|---|---|---|---|---|
| 对象存储抽象（S3/OSS/COS） | ❌ 本地盘 `texture/pipeline.go:64` | ❌ `service/texture/storage.go:24-33` | ❌ `texture.py:20-21` | ⚠️ 有驱动开关、无云适配器（`config/filesystems.php:54-57`） | ❌ 本地盘 `common.go:476-509` |
| CDN 失效/刷新调用 | ❌ `cdn_enabled` 只管真实 IP（`main.go:192-208`） | ❌ | ❌ | ❌ `cdn_address` 只改前端资源域名（`config/options.php:45`） | ❌ |
| 内容寻址 URL + `immutable` | ✅ `main.go:793,965` | ⚠️ Nginx 直出、Go 不发缓存头（`profile.go:30-34`） | ⚠️ 头声明基址与挂载点不一致（`yggdrasil_routes.py:58-59` vs `:109-112`） | ✅ `routes/static.php:8` | ⚠️ 内容寻址但 web 解析发 no-cache（`front.go:1200-1206`） |
| 删除：行 + 文件 + 引用计数 | ✅ `account/delete.go:47-51` + `store/texdelete.go:152-155` | ❌ 只删行，`DeleteFile` 零调用（`service/texture/admin.go:150-195`）→ 文件永久堆积 | ❌ 只删行不计数（`texture.py:71-85`） | ✅ 计数为 1 才删（`SkinlibController.php:337-356`） | ✅ `DeleteSkinIfUnused`（`common.go:627-655`） |
| 删除留证（墓碑） | ✅ `migrations/0019_texture_tombstones.sql` | ❌ | ❌ | ❌ 举报行留着但 tid 悬空（`ReportController.php:115-124`） | ❌ |
| 删除并发收敛 | ✅ 按 hash `GET_LOCK` 跨上传/删除（`texdelete.go:48-78`） | ❌ | ❌ | ❌ 举报处置 delete 跳过引用计数守卫（`ReportController.php:112-113`） | ⚠️ 依赖 SQLite/WAL 串行化 |
| 手臂模型来源 | ✅ 像素判定（`texture/model.go:38-57`），客户端声明仅建议值 | ❌ 采信表单（`profile/store.go:24-29`） | ❌ 表单原样入库（`site_routes.py:158`） | ❌ 表单枚举（`SkinlibController.php:206`） | 未取证 |
| 旧 22×17 披风补边 | ✅ 新上传补边、存量不改写（`pipeline.go:141-189`） | ❌ 收下不归一（`storage.go:82-84`） | ❌ 同（`image_utils.py:23`） | ❌ 直接拒收（`SkinlibController.php:234,255-256`） | 未取证 |
| 违规处置状态化 | ✅ `textures.moderation` 列 + UPDATE 拦截（`store/skinlib.go:314-317`）；按行不按 hash 是已知限制 | ❌ 仅 `is_public`，可无门槛转回公开（`management.go:55-63`） | ❌ 无管理端材质路由（`admin_routes.py:39-286`） | ⚠️ 行删了重复检查就放行（`SkinlibController.php:282-291`） | 未取证 |

### 契约与工程纪律

| 能力 | SkinNexus | element-skin | vSkin | blessing | Drasl |
|---|---|---|---|---|---|
| 错误体带稳定 code | ✅ `{error, code, message}` + 码目录（`adminapi/errors.go:31-35`、`i18n/codes.go`） | ✅ 三元组 + 18 值闭合枚举、不含文案（`util/response.go:18-37,107-124`） | ❌ 只有人话（`site_routes.py:168`） | ❌ 粗粒度整数 0/1/2（`app/helpers.php:27-46`） | 未取证 |
| 错误契约被机器强制 | ✅ AST 闸（`errorgate/errorgate.go` + 测试） | ⚠️ 未见对应闸 | ❌ 无 CI | ❌ | 未取证 |
| 覆盖率阈值 | ⚠️ 地板 45.0（实测 52.5）`ci.yml:35-53` | ✅ 硬阈值 90%（`docker-publish.yml:75-83`） | ❌ | ⚠️ 上传 Codecov 不设阈值（`CI.yml:78-85`） | 未取证 |
| 集成测试真库矩阵 | ✅ 六腿 MySQL 8/5.7 + MariaDB 11.4/10.11 + PG16 + SQLite 文件库（`ci.yml:72-180`） | 未取证 | ❌ 无 CI | ⚠️ PHPUnit 矩阵 | ✅ SQLite/MySQL 双后端 |
| 前端测试/lint 门禁 | ❌ | ⚠️ 有配置无 job | ⚠️ 同 | ✅ `yarn lint + fmt:check + type:check` | 未取证 |
| 公共配置白名单 | ✅ 5 键硬编码（`webapi.go:414-431`） | ✅ 17 键列举并校验数量（`publicsite/public.go:64-69`） | ⚠️ 白名单内联大 handler | ✅ 4 键（`HomeController.php:35-40`） | ❌ 模板直读 Config |
| env 唯一清单 + 未知变量告警 | ✅ `config/env.go` AST 双向对齐 + `check-docs.mjs` | ❌ | ❌ | ❌ | ❌ |
| 迁移前自动备份 | ❌（只换镜像不回滚 schema，`deploy.md` §2.5） | 未取证 | 未取证 | ⚠️ 依赖手工 | ✅ 默认开（`db.go:273-289`） |
| 未实现诚实返回 501 | ✅ 宁可不注册（Union 关闭 = 全 404） | 未取证 | 未取证 | 未取证 | ✅ `discovery.go:185-188`，文档显式标未实现 |
| 多前缀挂同一 handler | ✅ 单 mux 分面 | 未取证 | 未取证 | ⚠️ 三张路由表 | ✅ `""`、`/auth`、`/authlib-injector/authserver` 三前缀（`main.go:396-438`） |

### 账号、内容与生态

| 能力 | SkinNexus | element-skin | vSkin | blessing | Drasl |
|---|---|---|---|---|---|
| 邮箱验证流 | ✅ 发信 + 点击消费 + 宽限期 + 两侧验证门（`webapi/email_verify.go`、`internal/verifygate/`） | ✅ Redis 码 TTL300 + 重发限流（`verification.go:100-121`） | ✅ SQLite 码 TTL300 | ⚠️ 验证链接永不过期（`SendEmailVerification.php:15`） | ❌ 邀请码注册（`main.go:673-722`） |
| 找回密码令牌强度 | ✅ 单次消费 + 摘要存储 + 失败合并（`webapi/password.go:121,204-246`） | 未取证 | 未取证 | ✅ 1h 过期 + IP 节流（`AuthController.php:270-294`） | 未取证 |
| 站内通知中心 | ✅ 通知表 + 通知页 + 未读筛选（`webapi/notifications.go`、`NotificationsPage.vue`） | ✅ notices/receipts/targets 三表 | ❌ | ✅ `notifications` + SiteMessage | ❌ |
| 皮肤库检索 | ⚠️ 关键词 LIKE description、2 排序（`store/skinlib.go:103-129`） | ✅ hash/名称/上传者 + 游标分页（`library.go:41-101`） | ❌ 仅分页 | ⚠️ 任意 sort 列直进 orderBy（`SkinlibController.php:69-71`） | 未取证 |
| 服务端渲染内容页 | ✅ 首页 + 详情页 + llms.txt + sitemap（`texturepage.go`、`landing.go`） | ❌ SPA 壳 | ❌ SPA 壳 | ⚠️ Twig 壳 | ⚠️ Go 模板整页，SEO 未取证 |
| 服务端材质渲染 | ✅ Go 立方体光栅带版本号（`avatargen.go:37-44`） | ❌ 仅客户端 WebGL | ⚠️ 仅截脸 | ✅ 独立包 + WebP 协商 + 缓存（`TextureController.php:61-79`） | 未取证 |
| 签名插件市场 / 任务运行器 | ✅ Ed25519 签名包 + 注册表 + 持久任务 lease | ❌ | ❌ | ⚠️ 目录制无签名链 | ❌ |
| 正版账号绑定 | ❌ 仅 `authsource.go` 接口占位 | 未取证 | ⚠️ 仅微软 OAuth 登录 | — | ❌ 无 MSA/XBL/XSTS |
| `/publickeys` + 证书族 | ❌ 未挂（元数据 `signaturePublickey`，authlib 口径） | 未取证 | 未取证 | ❌ | ✅ 整族挂载（`main.go:454-470`） |

注（Drasl 证书族的消费方）：authlib-injector 对 `/publickeys` 与 profile key 都用本地实现回应
（`httpd/PublickeysFilter.java:40-61`、`ProfileKeyFilter.java:48-82`），真正消费者是**不挂 agent 的
原生客户端**（`-Dminecraft.api.*.host`），不是老启动器。

### 重量级补录（chrly / HRPAuth，2026-11-15 一手取证）

只记本次实际取证的维度；未查维度不写格子。

| 维度 | SkinNexus | chrly（Ely.by） | HRPAuth |
|---|---|---|---|
| 纹理签名 | ✅ 自签，公钥走元数据 | ✅ `signer/signer.go` + `/textures/signed/{username}`（服务端直发签名 profile）+ 公钥 DER/PEM 双端点 | —（Ygg 面在兄弟仓） |
| Mojang 兜底/代理 | ✅ `ygg_mojang_fallback` | ✅ 代理 + worker 模式 UUID 解析（`api/mojang/mojang.go`） | — |
| 账号安全深度 | 密码（12 旧算式 + argon2id）+ 邮箱验证 + 失败锁定 | — | ✅ TOTP / WebAuthn·passkey / 邮箱二要素 / 恢复密钥 / 验证码（`services/webauthn_service.go` 等） |
| API 契约治理 | 错误码目录 + AST 闸；launcher 面 openapi.yaml | — | ✅ HA-Contract 集中 OpenAPI + 错误码表 |
| 发布工程 | GHCR + 三引擎 CI | Docker 镜像 | ✅ 6 平台二进制 + 多架构 manifest + digest 钉死 + 配置模板渲染 |
| 旧客户端兼容 | ✅ legacy/usm/CustomSkinAPI 路由 | ✅ `/skins?name=`、`/cloaks?name=` | — |
| 游戏服务端接入 | 无 | ✅ `/textures/signed` 供服务端 API | ✅ HRPAuth-Spigot（Velocity） |

注：HRPAuth 的 Yggdrasil/皮肤面拆在兄弟仓（HRPAuth-Yggdrasil-API / HASkinLib / HASkinProxy），
本次未读其源码、仅按 README 声明计。两家强项恰好落在总判断的第 2、3 条品类缺口上。

## 可借鉴的工程纪律

1. **闭环后再宣传**：element-skin「材质文件 LRU 缓存」README 有、源码无；但其 90% 覆盖率闸是真的。
2. **错误契约不带文案**：`{object, operation, reason, params}` 把失败分类与人话彻底分开。
3. **公共配置白名单并校验数量**：多一个键就报错（`publicsite/public.go:64-69`）。
4. **迁移前自动备份**（Drasl 默认开）：我方迁移引擎只进不退，破坏性变更前靠手工备份（`deploy.md` §2.6）。
5. **未实现就 501**：声明面与实现面分离，不伪装成功。
6. **多前缀挂一个 handler**：协议方言差异集中在挂载层。
7. **Webhook 投递器**（element-skin）：两阶段 DB claim + lease、退避、死信、SSRF 防护 DialContext、
   投递前权限复核——我方 `internal/jobs` 已有 lease/退避，差距在死信与投递前复核。
8. **不留长期兼容分支**：两家 Go 竞品都无 v1/v2 双写包袱。

## 各家安全与正确性反面教材

仅作技术对照，不作宣传素材。

- **vSkin**：限流与 OAuth state 都存进程内存（重启即失效）；材质头基址与实际挂载点不一致；
  删除没删到行也返回 ok（`site_routes.py:209-215`）。
- **element-skin**：`DeleteFile` 全路径零调用，行删了文件永留盘上；`RecountUsage` 无生产调用方。
- **blessing**：举报处置 delete 跳过引用计数守卫（`ReportController.php:112-113`），共享内容连坐；
  验证链接永不过期；列表排序列名来自请求直进 `orderBy`。
- **Drasl**：`/web/texture/resolved/{token}` 的 AEAD token 无时间戳（`common.go:52-73`），
  「防缓存」URL 实际永久有效；`services.go:69` cape 仍是 TODO。
