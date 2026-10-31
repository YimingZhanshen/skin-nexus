# 竞品源码分析：element-skin 与 vSkin

> 快照口径：2026-10-26 对两个开源竞品做源码级取证（浅克隆，不入库），结论只在那一刻成立——
> 竞品随时在演进，引用前重新取证。
>
> 分析对象：
> - [water2004/element-skin](https://github.com/water2004/element-skin)（v4.x，Go + PostgreSQL + Redis + Vue 3/Element Plus，MIT）
> - [iJunecn/vSkin](https://github.com/iJunecn/vSkin)（FastAPI + Vue 3 + SQLite，GPL-3.0）
>
> 方法：两路子代理深读（结论附文件路径）+ 对 SEO / 路由 / 封禁 / SMTP 四个维度的第一手 grep 复核。
> 凡「README 宣称 ≠ 源码实证」处均已标注。
>
> **范围**：本文只覆盖上面两个竞品。与 blessing-skin-server 的协议兼容事实见
> [`compat-matrix.md`](compat-matrix.md)，不在本文证据范围内。

## 1. 总判断

功能广度：element-skin > vSkin > SkinNexus（快照当时）。但「搜索引擎可见性」与「启动器路由兼容」
两个维度两家都是真空——服务端渲染 SEO 体系（sitemap/robots/逐页 OG+JSON-LD/单品详情页 URL）是差异化资产。
由此定的策略：守住 SEO 赛道，先补真实可用性短板（找回密码、正版 Fallback、头像闭环），装饰性功能按性价比排队。

## 2. 实证领先区（SkinNexus 有、两家确认没有）

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

## 3. 差距处置结果

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

**缓行候选（附来源与量级，未排期）**：逐用户通知中心（element-skin `service/notice/`，2–3 天）·
设备授权流后台可视化配置（vSkin `AdminOAuthApps.vue`，需先核对差量）· 农历节日彩蛋（装饰性）·
浏览器 IDB LRU 渲染缓存（性能细节）· `/v2/capabilities` 发现面（生态向，单站收益低）·
webhook-worker 独立进程队列（设计优秀但对当前规模过度工程）· Python SDK（生态向）·
**Microsoft 正版账号绑定**（两家主打，本站仅 `internal/auth/authsource.go` 接口占位；量级大——
MS OAuth 注册/回调/安全审查/角色关联，需独立拍板分段实施，已进 README 路线图）。

## 4. 竞品弱点备忘

仅作技术对照，不作宣传素材。

- **vSkin**：封禁只挡 hasJoined（站点登录与 authenticate 均不查）；限流用进程内存 dict（重启/多 worker 失效）；
  微软 OAuth state 存进程内存（源码注释自认「生产应上 Redis」）；OAuth redirect_uri 单值、client_id 自增 int、
  shared client 刷新免 secret；无皮肤库详情页/搜索/收藏计数；无审计日志；死代码 `AuthCallback.vue` 未注册路由。
- **element-skin**：OIDC discovery `grant_types_supported` 只公告 authorization_code/refresh_token
  （device_code/client_credentials 实装但未公告）；「材质文件 LRU 缓存」README 宣称与源码不符
  （实际只有 avatar/viewer-snapshot 两桶）；无皮肤库单品页；前端无独立 CI job。
- **共同**：无皮肤库单品详情页、无 sitemap/robots/OG、无服务端渲染。

## 5. 参考实现索引（将来动手时按图索骥）

| 将来要做 | 参考实现 |
|---|---|
| Fallback 完整版（健康检查/公钥轮换/玩家白名单） | element-skin `internal/service/probe/probe.go`、`service/fallback/public_keys.go`（`LongestCommonYggdrasilRoot` 推导发现根、MergePublicKeys）；vSkin `fallback_backend.py`（serial/parallel + as_completed） |
| 通知中心 | element-skin `internal/database/schema.go:158-198`（notices + notice_receipts + notice_targets）、`service/notice/user.go`（详情读取即标已读） |
| Webhook 队列 | element-skin `service/webhook/worker.go`（两阶段 DB claim + 30s lease、30s→6h 退避带 SHA256 抖动、12 次/72h 死信、SSRF 防护 DialContext、投递前权限复核） |
| 权限模型升级（allow/deny 覆盖 + 会话×入口策略） | element-skin `internal/database/permission/effective_compute.go:28-83`（角色并集 ∪ allow − deny）、`session_permission_policies` |
| 浏览器渲染缓存 | element-skin `src/storage/indexedDbCache.ts`（LRU 逐出保护刚写入 key）、`renderCache.ts`（IDB→内存降级） |
| 方形头像 | vSkin `utils/image_utils.py:114-178`（含默认 Steve 头像硬编码生成） |

> 上表的文件路径属于**对方仓库**（未随本仓库分发），行号只作定位线索；动手前请重新拉取源码取证。
