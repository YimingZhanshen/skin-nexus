# 竞品源码分析

> 快照口径：2026-11-01。方法：浅克隆到工作区外的 `refs/`（不入库），子代理按维度深读 + 第一手 grep
> 复核，**每一格给「对方仓库内的文件:行」**；未找到写 `NOT FOUND`，不猜。凡「README 宣称 ≠ 源码实证」
> 均单独标注。对手最后提交日期见下表——仓库当前不活跃不等于结论失效，但引用前仍应重新取证。
>
> | 仓库 | 栈 | 许可 | HEAD 提交日 |
> |---|---|---|---|
> | [element-skin](https://github.com/water2004/element-skin) | Go + PostgreSQL + Redis + Vue 3 | MIT | 2026-10-25 |
> | [vSkin](https://github.com/iJunecn/vSkin) | Python FastAPI + Vue 3 + SQLite | GPL-3.0 | 2026-05-29（基本停更） |
> | [blessing-skin-server](https://github.com/blessing-skin/blessing-skin-server) | PHP Laravel + MySQL | MIT | 2026-03-30 |
> | [Drasl](https://github.com/Unmojang/Drasl) | Go + SQLite/MySQL | GPL-3.0 | 2026-11-01 |
> | [LittleSkin premium-verification](https://github.com/xuckee/premium-verification) 等生态件 | PHP | AGPL/GPL | 见仓 |
>
> 许可证边界：本文只吸收**概念与公开协议事实**，禁止复制、翻译改写 GPL/AGPL 竞品源码、译文或测试
> fixture；引用一律注明对方路径与行号，行号只作定位线索。
>
> 与 blessing-skin-server 的**协议兼容**事实（老站实测、迁移对账）不在本文范围内，见
> [`compat-matrix.md`](compat-matrix.md) 与 [`migration.md`](migration.md)。

## 1. 总判断（2026-11-01）

五个仓库在**功能面上没有一家同时具备**：SEO 服务端渲染体系、启动器深链兼容、材质生命周期闭环
（像素定模型 → 违规处置 → 引用清点后彻底删除并留证）、签名插件市场与任务运行器。我方四项皆有，
差距不在功能列表，而在**可发布性与契约完整性**——本轮（质量闸与本地化轮）正是对着这一点做的，
对照依据见下表「错误契约 / 覆盖率 / 公共配置」三行。

真实缺口按量级排：**对象存储与 CDN 刷新**（五家全无，属生态级缺口，我方以 `immutable` + 可改写
URL 的插件 filter 换取 5182 RPS 的纹理分发，代价记在 `compat-matrix.md` §4）＞
**邮箱验证流 + 站内通知中心**（element-skin 两项齐备；blessing 有通知中心、验证链接却永不过期，
我方只有找回密码邮件）＞
**不挂 authlib-injector 的接入面**（services / discovery / certificates 族，Drasl 整族挂载）。
判为**伪需求**的：签名密钥轮换、像素级材质 hash、three.js 全景首页、换 PostgreSQL/Redis 栈。

## 2. 横向能力矩阵

「我方」列的证据是本仓库路径。空格含义：✅ 有并可用 · ⚠️ 有但残缺/有坑 · ❌ 确认没有 · — 不适用 ·
「未取证」本次没查，**不等于对方没有**，引用前补测。

### 2.1 材质与存储

| 能力 | SkinNexus | element-skin | vSkin | blessing | Drasl |
|---|---|---|---|---|---|
| 对象存储抽象（S3/OSS/COS） | ❌ 本地盘 `server/internal/texture/pipeline.go:64` | ❌ `skin-backend/internal/service/texture/storage.go:24-33` | ❌ `skin-backend/database_module/modules/texture.py:20-21` | ⚠️ 有驱动开关但**没装任何云适配器**：`config/filesystems.php:54-57` + `composer.json:5-39` 无 flysystem-aws/oss/cos | ❌ 本地盘 `common.go:476-509` |
| CDN 失效/刷新调用 | ❌（`cdn_enabled` 只管真实 IP 解析 `main.go:192-208`） | ❌ | ❌ | ❌ `cdn_address` 只改前端资源域名，从不作用于材质 URL（`config/options.php:45`） | ❌ |
| 内容寻址 URL + `immutable` | ✅ `main.go:793,965` | ⚠️ `/static/textures/{hash}.png` 由 Nginx 直出，Go 侧不发缓存头（`service/yggdrasil/profile.go:30-34`） | ⚠️ `public, max-age=604800`，且 `config.yaml:31` 说基址取 `api_url`、代码实际用 `site_url` → 告诉客户端的地址与挂载点不一致（`routers/yggdrasil_routes.py:58-59` vs `:109-112`） | ✅ `routes/static.php:8` + `cache.headers:...max_age=31536000` | ⚠️ 内容寻址 `model.go:292-294`，但 web 解析路由发的是 `private, no-cache`（`front.go:1200-1206`） |
| 删除材质：行 + 文件 + 引用计数 | ✅ `server/internal/account/delete.go:47-51` + `store/texdelete.go:152-155`，同 hash 归零才 unlink | ❌ 只删行；`TextureStorage.DeleteFile`（`storage.go:70`）在所有删除路径上**零调用**（`service/texture/admin.go:150-195`）→ 文件永久堆积 | ❌ 只删 `user_textures` 行，不计数不 unlink（`texture.py:71-85`） | ✅ `SkinlibController.php:337-356` 计数为 1 才删文件 | ✅ `DeleteSkinIfUnused` `common.go:627-655` |
| 删除留证（墓碑表） | ✅ `server/migrations/0019_texture_tombstones.sql` | ❌ 无相关表（`internal/database/schema.go` 建表清单） | ❌ | ❌ 举报行留着但 `tid` 悬空（`ReportController.php:115-124`） | ❌ |
| 删除并发收敛 | ✅ 按 hash 的 `GET_LOCK` 跨上传/删除共用 `store/texdelete.go:48-78` | ❌（无删除路径可谈） | ❌ | ❌ 举报处置的 delete **跳过**引用计数守卫（`ReportController.php:112-113`）→ 共享内容被销毁 | ⚠️ 依赖 SQLite/WAL 串行化 |
| 手臂模型来源 | ✅ **像素判定**（`server/internal/texture/model.go:38-57`），客户端声明仅建议值、不一致写日志（`account/textures.go:167-169`） | ❌ 采信表单：`NormalizeModel(input.Model)`（`database/profile/store.go:24-29`） | ❌ `model: str = Form("default")` 原样入库（`routers/site_routes.py:158` → `texture.py:57`） | ❌ 表单枚举 `Rule::in(['steve','alex','cape'])`（`SkinlibController.php:206`），读时由 `type` 反推（`app/Models/Texture.php:50-53`） | 未取证 |
| 旧 22×17 披风入库补边 | ✅ 新上传补边、按类型门控（`pipeline.go:141-143,163-189`）；存量不改写是明示取舍（`pipeline.go:12-16`） | ❌ 收下 22×17 族但不归一（`storage.go:82-84`） | ❌ 同样收下不归一（`utils/image_utils.py:23`） | ❌ 直接拒收：要求 `w%64==0 && h%32==0` 且比例==2（`SkinlibController.php:234,255-256`） | 未取证 |
| 违规处置状态化（禁止再公开） | ✅ `textures.moderation` 列（`0018_texture_moderation.sql`）+ 在 UPDATE 的 WHERE 里拦（`store/skinlib.go:314-317`）；**按行不按 hash** 是已知限制 | ❌ 只有 `is_public` 布尔（`service/texture/management.go:55-63`），可无门槛转回公开 | ❌ **没有任何管理端材质路由**（`routers/admin_routes.py:39-286`） | ⚠️ 举报状态是持久的，但行删了重复检查就放行（`SkinlibController.php:282-291`） | 未取证 |

### 2.2 契约与工程纪律

| 能力 | SkinNexus | element-skin | vSkin | blessing | Drasl |
|---|---|---|---|---|---|
| 错误体带稳定 code | ✅ `{error=code, code, message}` `adminapi/errors.go:31-35`；码目录 `server/internal/i18n/codes.go` | ✅ 三元组 `{object, operation, reason[, params]}`，reason 是闭合的 18 值枚举、**完全不含文案**（`internal/util/response.go:18-37,107-124`） | ❌ `{"detail": "<人话>"}`，无码（`routers/site_routes.py:168`） | ❌ `{code, message}` 的 code 是粗粒度整数 0/1/2，且 message 由后端 `trans()` 本地化（`app/helpers.php:27-46`） | 未取证 |
| 错误契约被机器强制 | ✅ AST 闸：无码、码非常量、文案含中文一律失败（`server/internal/errorgate/errorgate.go`，两个包的 `errorgate_test.go`） | ⚠️ 未见对应闸 | ❌ 无 CI 可言（`.github/` 不存在） | ❌ | 未取证 |
| 覆盖率阈值 | ⚠️ 地板 45.0（本地实测 52.5，2026-11-09）+ 0% 包集合钉死，`.github/workflows/ci.yml:35-53`、`server/testdata/coverage-floor.txt` | ✅ **硬阈值 90%**（`.github/workflows/docker-publish.yml:75-83`） | ❌ | ⚠️ 上传 Codecov 但**不设阈值**（`.github/workflows/CI.yml:78-85`） | 未取证 |
| 集成测试真库矩阵 | ✅ 六条腿：MySQL 8.0/5.7 + MariaDB 11.4/10.11 + PostgreSQL 16 + SQLite 文件库，每腿都跑 `./internal/store/ ./internal/account/`（`ci.yml:72-180`） | 未取证（CI 只有一个 docker 文件） | ❌ 无 CI | ⚠️ 有 PHPUnit 矩阵（其 CI `:78`） | ✅ SQLite/MySQL 双后端（`config.go` 方言分支） |
| 前端测试/lint 门禁 | ❌ 无 vitest/eslint 配置 | ⚠️ 有 `eslint.config.ts` 但无对应 job | ⚠️ devDeps 声明了 eslint/prettier，无执行入口 | ✅ `yarn lint && fmt:check && type:check`（`CI.yml:87-92`） | 未取证 |
| 暴露给前端的公共配置是固定白名单 | ✅ 5 个键硬编码 `webapi/webapi.go:414-431` | ✅ 17 键显式列举并校验数量（`internal/service/publicsite/public.go:64-69`） | ⚠️ 有白名单但内联在一个大 handler 里（`routers/site_routes.py:318-366`） | ✅ 只有 4 个键（`HomeController.php:35-40`） | ❌ 模板服务端直读 Config，无前导白名单面 |
| env 唯一清单 + 未知变量告警 | ✅ `server/internal/config/env.go`，AST 双向对齐 `env_test.go:86,143` + 文档表 `scripts/check-docs.mjs:122-150` | ❌ | ❌ | ❌ | ❌ |
| 迁移前自动备份 | ❌（回滚口径是「只换镜像不回滚 schema」，见 `docs/deploy.md` §2.5） | 未取证 | 未取证 | ⚠️ 依赖运维手工 | ✅ 默认开启：WAL checkpoint 后复制 `drasl.{version}.{timestamp}.db`（`db.go:273-289`，配置项 `config.go:310`） |
| 未实现即诚实返回 501 | ✅ 无此类端点（宁可不注册：Union 关闭时全路由 404） | 未取证 | 未取证 | 未取证 | ✅ `DiscoveryNotImplemented` 501（`discovery.go:185-188`），文档里 6 个端点显式标未实现（`:111-176`） |
| 多前缀挂同一 handler（协议方言） | ✅ 单 mux 分面（webapi/publicapi/adminapi/ygg 各一张表） | 未取证 | 未取证 | ⚠️ 路由分 `web/api/static` 三表 | ✅ 同一组 handler 挂 `""`、`/auth`、`/authlib-injector/authserver` 三前缀（`main.go:396-438`） |

### 2.3 账号、内容与生态

| 能力 | SkinNexus | element-skin | vSkin | blessing | Drasl |
|---|---|---|---|---|---|
| 邮箱验证流（发信 + 校验路由） | ❌ 只有 `email_verified` 标志与进服门禁，**无发信、无 /verify** | ✅ Redis 8 位码 + TTL 300s（`internal/service/verification/verification.go:100-121`），重发限流 5 次/15 分钟（`rate_limit.go:16-34`） | ✅ SQLite 码 + TTL 300（`database_module/modules/verification.py:10-15`、`utils/email_utils.py:24`） | ⚠️ 有，但**验证链接永不过期**：`URL::signedRoute(..., null, false)`（`app/Listeners/SendEmailVerification.php:15`），重发限制只存 session（`UserController.php:148-152`） | ❌ 改用邀请码注册（`main.go:673-722`） |
| 找回密码令牌强度 | ✅ 单次消费、DB 时钟比较过期、只存 SHA-256 摘要、多种失败合并为一个码（`webapi/password.go:121,204-246`；`store/password_reset.go:45-53`） | 未取证 | 未取证 | ✅ 1 小时过期 + 每 IP 缓存节流（`AuthController.php:270-294`） | 未取证 |
| 站内通知中心 | ❌ 无表无路由（只有瞬时 toast） | ✅ notices + receipts + targets 三表（`internal/database/schema.go:158-198`），读详情即标已读 | ❌ | ✅ `notifications` 表 + SiteMessage（`app/Notifications/SiteMessage.php`、`NotificationsController.php:13-56`） | ❌ |
| 皮肤库检索 | ⚠️ 关键词只 LIKE `description`、2 种排序（`store/skinlib.go:103-129`）；无标签 | ✅ hash/名称/上传者 + 2 排序 + 游标分页（`database/texture/library.go:41-101`） | ❌ 只有 page/limit，`ORDER BY created_at DESC`（`routers/site_routes.py:225-255`） | ⚠️ filter/keyword/uploader + **任意 sort 列直接进 `orderBy`**（`SkinlibController.php:69-71`）——列名来自请求，靠 `Texture` 的列白名单兜底 | 未取证 |
| 服务端渲染内容页 | ✅ 首页 + 详情页 + `llms.txt` + sitemap（`cmd/skin-nexus/texturepage.go`、`landing.go`、`main.go:1079-1118`） | ❌ SPA 壳 | ❌ SPA 壳 | ⚠️ Twig 壳，正文客户端渲染 | ⚠️ Go 模板整页（`front.go`），但 SEO 结构未取证 |
| 服务端材质渲染 | ✅ Go 立方体光栅，输出带版本号（`avatargen.RenderVersion`，`avatargen.go:37-44`） | ❌ 只有客户端 WebGL（`package.json:32-33`） | ⚠️ 头像有服务端截脸（`utils/image_utils.py:114-144`），预览仍走 WebGL | ✅ 独立包 `blessing/texture-renderer` + WebP 协商 + 渲染缓存（`TextureController.php:61-79`） | 未取证 |
| 签名插件市场 / 任务运行器 | ✅ Ed25519 包 + 注册表 + 持久任务 lease（`internal/signedpkg/`、`plugin/jobs_worker.go:22-36`） | ❌ | ❌ | ⚠️ 插件目录制、无签名链（Composer 式） | ❌ |
| 正版（微软/Mojang）账号绑定 | ❌ 仅 `internal/auth/authsource.go` 接口占位，且该文件当前无人引用 | 未取证（本次矩阵未覆盖其 provider 侧） | ⚠️ 只接 Microsoft OAuth 登录，不做「正版资格」校验 | —（老站无此概念） | ❌ **无 MSA/XBL/XSTS**：只有 Yggdrasil 风格 `/authenticate`，MSA 仅作回源服务器配置出现（`config_test.go:202`） |
| 正版资格校验（第三方生态件） | — | — | — | — | 参考 LittleSkin `premium-verification`：一次性 MSA→XBL→XSTS→entitlement，落 `premium(uid,pid,uuid)`，**不保存任何令牌**（`src/Providers/PremiumProvider.php:58-142`、`callbacks.php:16-24`）；Heimdall 侧 90 天周期性重验（`Configuration.java:32-36`） |
| `/publickeys` + `POST /player/certificates` | ❌ 未挂（单一签名密钥 + metadata 里的 `signaturePublickey`，authlib-injector 口径） | 未取证 | 未取证 | ❌ | ✅ 整族挂载 `main.go:454-470`（实现 `services.go:171-293,546`）。**注意消费方**：authlib-injector 对这两者都用本地实现回应——`/publickeys` 取元数据里的 key（`httpd/PublickeysFilter.java:40-61`），profile key 用本地生成的假密钥对（`httpd/ProfileKeyFilter.java:48-82`），因此真正的消费者是**不挂 agent 的原生客户端**（`-Dminecraft.api.*.host`），不是老启动器 |

## 3. 取舍与不做清单（含理由）

| 判定 | 项 | 依据 |
|---|---|---|
| 取舍·生态级 | 对象存储 + CDN 刷新 | 五家全无（§2.1 首两行）→ 生态级工程；我方以 `immutable` + 插件 URL 改写器换取 5182 RPS 与零依赖，后果与边界记在 `compat-matrix.md` §4 |
| 取舍·消费方缺失 | services / discovery / certificates 族 | 只有不挂 authlib-injector 的原生客户端需要（§2.3 最后一行）；面向「社区启动器 + 正版混合」才成立，现无此类接入方 |
| 伪需求 | 签名密钥轮换 | authlib-injector 的 key 来自服务端元数据并本地缓存，无轮换消费路径；规范本身建议「尽量不要换 key」 |
| 伪需求 | 像素级材质 hash | 与 blessing 迁移的字节对账冲突（`internal/migrate/scan.go:66-80` 按原文件字节哈希） |
| 伪需求 | three.js 全景首页 / PostgreSQL·Redis 换栈 | 与 SSR SEO 优势冲突；单站规模无收益 |

## 4. 可借鉴的工程纪律

1. **闭环后再宣传**：element-skin 的「材质文件 LRU 缓存」README 有、源码无（只有 avatar/快照两桶），
   而其 90% 覆盖率闸是真的——`docker-publish.yml:75-83`。反面教训同时提醒我们：本轮把「错误统一带 code」
   从文档承诺变成失败即红的闸。
2. **错误契约不带文案**：`{object, operation, reason, params}`（`util/response.go:18-37`）把「哪一类失败」
   与「怎么说给人听」彻底分开。我们的 `{error, code, message}` 保留了 message 兜底，方向一致。
3. **公共配置是白名单不是对象**：`publicsite/public.go:64-69` 列举 17 个键并**校验数量**，多一个键就报错。
   我们的 `site-config` 同样硬编码字段，`env.go` 那套双向对齐是同一纪律。
4. **迁移前自动备份**（Drasl `db.go:273-289`，默认开）值得抄：我们的迁移引擎只进不退（`docs/deploy.md` §2.5），
   破坏性变更前只能靠 `docs/deploy.md` §2.6 的手工备份。
5. **未实现就 501**（Drasl `discovery.go:185-188`）：声明面与实现面分离但不伪装成功。我们当前的做法是
   宁可不注册（Union 关闭 = 全 404），同样不留「看着像有」的空壳。
6. **多前缀挂一个 handler**（Drasl `main.go:396-438`）：协议方言差异集中在挂载层，业务实现只有一份。
7. **Webhook 投递器**（element-skin `service/webhook/worker.go`）：两阶段 DB claim + 30s lease、
   30s→6h 退避、12 次/72h 死信、SSRF 防护 DialContext、投递前权限复核——我们的 `internal/jobs`
   已有 lease/退避，DLQ 与投递前权限复核是明确的下一步量级。
8. **不留长期兼容分支**：两家 Go 竞品都没有「v1/v2 双写」的包袱。我们 `hookApi: 2` 单版本、
   删兼容层的口径与此一致。

## 5. 各家安全与正确性反面教材

仅作技术对照，不作宣传素材。

- **vSkin**：限流用进程内存 dict（重启/多 worker 即失效）；OAuth state 同样只存内存（源码注释自认应上 Redis）；
  告诉客户端的材质基址与 FastAPI 实际挂载点不一致（§2.1 第 3 行）；删除接口即使没删到行也返回 `{"ok": true}`
  （`routers/site_routes.py:209-215` 丢弃返回值）。
- **element-skin**：`DeleteFile` 在所有删除路径上零调用 → 行删了、文件永久留在盘上；
  `RecountUsage`（`database/texture/library.go:141`）无生产调用方。
- **blessing**：举报处置的 delete **跳过**用户侧的引用计数守卫（`ReportController.php:112-113`），
  共享同一内容的行会被一起毁掉文件；邮箱验证链接**永不过期**（§2.3）；列表排序列名来自请求参数直进 `orderBy`。
- **Drasl**：`/web/texture/resolved/{token}` 的 AEAD token 里没有时间戳（`common.go:52-73`）→
  名为「防缓存」的 URL 实际永久有效；`services.go:69` 的 cape 仍是 TODO。

## 附录 A：2026-10-26 快照（历史，element-skin 与 vSkin 两仓）

> 以下为上一轮取证正文，保留以说明结论如何随源码演进变化；其中「SEO 真空」「启动器深链真空」两项
> 在 2026-11-01 复测中仍然成立，「正版绑定量级」一条已由 §3 接管。

### A.1 总判断

功能广度：element-skin > vSkin > SkinNexus（快照当时）。但「搜索引擎可见性」与「启动器路由兼容」
两个维度两家都是真空——服务端渲染 SEO 体系（sitemap/robots/逐页 OG+JSON-LD/单品详情页 URL）是差异化资产。
由此定的策略：守住 SEO 赛道，先补真实可用性短板（找回密码、正版 Fallback、头像闭环），装饰性功能按性价比排队。

### A.2 实证领先区（SkinNexus 有、两家确认没有）

| 能力 | element-skin | vSkin | SkinNexus | 证据 |
|---|---|---|---|---|
| sitemap.xml / robots.txt | ❌ | ❌ | ✅ | 直接 grep 两仓库 `sitemap\|robots` 零命中 |
| 逐页 title/meta/OG/JSON-LD | ❌（全站共用一个 `<title>皮肤站</title>`） | ❌ | ✅ 逐页 + JSON-LD | 两家 `index.html` 均为裸 SPA 壳；element-skin `useAppLayoutState.ts:196` 客户端改名 |
| 服务端渲染页面 | ❌（Nginx 纯静态托管前端） | ❌（仅 FastAPI SPA fallback） | ✅ landing + 皮肤详情页 | 两家均无 SSR/prerender（package.json/router 实证） |
| 皮肤详情页（单品 URL） | ❌ 只有 `/skin-library` 列表 + 页内弹窗 | ❌ 只有列表 | ✅ `/skinlib/{tid}` 服务端渲染 | 两家路由表均无 `:tid`/`:hash` 详情路由 |
| 启动器「换皮肤/改密码」深链兼容 | ❌ | ❌ | ✅ `/user/closet`、`/user/profile` | PCL 由 API 根替换 `api/yggdrasil/authserver` 派生 URL（`PageLoginProfileSkin.xaml.cs:151`、`ProfileUi.cs:291`）；两家 API 根与路由均不含该约定 |
| 登录/认证即查封禁 | 权限化登录 | ❌ 只挡 hasJoined | ✅ | SkinNexus `ygg/service.go:342-346,492`；vSkin `is_banned` 全项目仅 1 处调用 |
| blessing 迁移工具链 | ❌ | ❌ | ✅ 带 ID 保全的导入 CLI | 两家迁移为自建幂等 DDL / 硬编码 SQL 探测补列 |
| OIDC provider 完整度 | discovery 公告不全（device_code/client_credentials 未列入 `grant_types_supported`） | Janus 兼容层 + 设备流完整，但 union 联盟只是配置壳（无出站同步实现） | ✅ 完整 provider + 联盟实装 | element-skin `oidc_routes.go:24`；vSkin grep union 仅配置存取 |

### A.3 差距处置结果

取证后定的四项「本轮要做」全部落地（逐项验证记录见 [`CHANGELOG.md`](CHANGELOG.md)
2026-10-26 条目）：找回密码 + SMTP 邮件能力、Mojang 最小 Fallback、安全加固（单次令牌 / 限流 / 会话属性）。

一项判为**不重复建设**：方形头像。vSkin 的截脸闭环（`utils/image_utils.py:114-144`）本站已有等价实现——
衣柜「设为头像」+ `PUT /api/v1/me/avatar` + `/avatar/user/{uid}` 服务端渲染（头+帽层、2D/3D、尺寸参数），
按需渲染优于烘焙独立文件。

**明确不做**：

| 项 | 理由 |
|---|---|
| three.js 全景首页 / 轮播背景 | JS 重、伤 LCP，与服务端渲染 landing 的 SEO 优势冲突 |
| 像素级材质 hash（Littleskin 系） | 与 blessing 兼容迁移冲突（迁移按文件字节 hash 对账） |
| PostgreSQL / Redis 换栈 | 单站规模 MySQL 足够，换栈无收益 |

### A.4 竞品弱点备忘（两家，2026-10-26）

- **vSkin**：封禁只挡 hasJoined（站点登录与 authenticate 均不查）；限流用进程内存 dict（重启/多 worker 失效）；
  微软 OAuth state 存进程内存（源码注释自认「生产应上 Redis」）；OAuth redirect_uri 单值、client_id 自增 int、
  shared client 刷新免 secret；无皮肤库详情页/搜索/收藏计数；无审计日志；死代码 `AuthCallback.vue` 未注册路由。
- **element-skin**：OIDC discovery `grant_types_supported` 只公告 authorization_code/refresh_token
  （device_code/client_credentials 实装但未公告）；「材质文件 LRU 缓存」README 宣称与源码不符
  （实际只有 avatar/viewer-snapshot 两桶）；无皮肤库单品页；前端无独立 CI job。
- **共同**：无皮肤库单品详情页、无 sitemap/robots/OG、无服务端渲染。
