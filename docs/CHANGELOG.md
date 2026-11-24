---
description: "SkinNexus 变更日志：按日期倒序的迭代历史，只记要点，开发细节见对应 commit。"
---

# 变更日志

> 迭代历史，按日期倒序。**格式规范**：每天一条 `## YYYY-MM-DD · 主题`；跨天开发周期合并为一条
> 并在标题注明区间；同日多轮用粗体行内标记（**同日N轮（主题）**），不进目录；体量与当日提交量
> 相称——只记「改了什么 / 为什么 / 行为变化」，验证过程一句话。开发细节见对应 commit；
> README 状态行只留一句话摘要。

## 2026-11-24 · 租户数据导出与宽限期删除（同轮）

- 新增 owner/admin 租户 JSON 附件导出，按字段白名单返回租户、服务器/策略、白名单、封禁、owner 可见进服事件与凭据元数据；不返回邮箱、IP、token、secret/hash、邀请或全局用户/玩家/纹理，审计不记录导出内容。
- 新增 owner 删除请求：在一个事务内把租户置为 `deleting`、暂停全部服务器、吊销全部凭据并固定截止时间；`SKIN_SERVERACCESS_DELETION_GRACE_HOURS` 默认 168 小时，修改配置不改变已请求的期限。
- 新增平台超管到期删除：未请求或未到期返回 409，到期后事务执行既有 `sa_*` 级联，重复删除返回 404；全局用户、玩家和纹理保留。删除中的租户与服务器不能被重新启用。
- 三引擎加法迁移 `0036_serveraccess_lifecycle.sql` 新增独立 `deletion_requested_at` / `deletion_due_at`，不复用 `deleted_at`；模块关闭时新路由统一 404。

验证：S6a 红绿 API/store/migration/config 测试、SQLite 三引擎夹具集成、Go 全量 test/vet/build 与 locales/docs 闸通过。

## 2026-11-24 · 可选验证代理与治理加固（同轮）

- 新增可选 `skin-nexus verify-proxy` 子命令：不连接数据库、不加载主站配置，只凭 public server id、upstream API root 与 Bearer credential 转发标准 `hasJoined`；S3/S4 不依赖它。
- proxy 支持 env/flag、0600 凭据文件、系统或私有 CA 的 TLS 校验、本地监听与读写/请求头超时、请求/响应体上限；仅网络瞬断、超时和 5xx 有限重试，204/4xx/鉴权失败不重试，所有上游失败保持空 204。
- 官方 fallback 增加按服并发与熔断：连续 5 次上游故障打开 30 秒，半开仅允许 1 次探测；健康 204 不算故障，本地 session、白名单、封禁、停服与吊销判定不被熔断旁路。
- 按 `EventRetentionDays` 每小时批量清理进服事件；模块关闭时不创建 worker，面板热开关与进程退出显式停止，单服失败不会阻断其他服务器清理。
- 新增 proxy 上游失败、fallback 熔断打开与 retention 清理失败指标；日志与指标不使用 secret、query、body、玩家身份或 URL 标签。

验证：S5 红绿单测、真实 S3 proxy/URL-secret 决策与事件一致性、紧急吊销/停服、Go 全量 test/vet/build、前端构建与 docs 闸通过。

## 2026-11-24 · 服务器接入服主门户与平台管理（同轮）

- 新增默认关闭模块下的独立 `/servers` 服主门户：租户/成员/邀请、服务器状态、按服策略、白名单、封禁、Premium fallback、进服时间线与凭证轮换/吊销；viewer 只读，owner/admin 可写，全部沿用普通同源网页会话。
- 平台超管视图提供租户/服务器状态、租户与按服配额、紧急停用/启用、全局 fallback、凭证吊销与审计；按服配额只提交已填写字段，不改后端响应契约。
- 补齐两个租户只读端点：按服脱敏进服事件游标分页与玩家 `{uid,name,uuid}` 精确解析；服务端强制会话成员租户 scope，跨租户返回 404。
- `/servers` 在模块关闭时仍提供 SPA 不可用说明，模块 API 全部 404，未知子路径保持 404；公开站点配置实时发布能力位，面板热切换同步更新导航。凭证明文只展示一次，关闭、切换服务器或过期请求都不会重新显示已丢弃的 secret。
- 本切片不实现验证代理二进制、导出/租户删除或公测运维。

验证：Go 全量 test/vet/build、Yggdrasil 契约测试、前端构建、composable 测试、locales/a11y/theme/modal/docs 闸与文档站构建通过。

## 2026-11-24 · 服务器接入可信验证链（同轮）

- 新增 URL secret 与 Bearer Header 两种按服 `hasJoined` 入口；无效、吊销、过期、跨租户或模块关闭统一 404，
  有效凭证的策略拒绝与故障保持标准空 204，响应禁止缓存与 Referrer 泄露。
- 验证链按模块/服务器状态、凭证、平台封禁、本服封禁、UID 白名单、配额/并发、本地 session、官方兜底依次执行；
  只有真实 session miss 且两级开关允许才回源，官方响应原样透传，本地 profile 继续使用既有签名格式。
- 三引擎加法迁移 `0035_serveraccess_events.sql` 新增按服进服事件与数据库原子配额窗口；事件记录身份、结果、来源、
  耗时和请求关联 ID，同一本地 join 重放去重，写入失败只记固定标签结构化错误、指标与告警 hook，不改变验证结果。
- 验证路由在组装层提前关闭诊断采集，原始 secret path、query、Authorization、body、IP 与邮箱不进入应用日志或明细；
  旧 `/api/yggdrasil` 路由、DTO、错误体、纹理与签名契约保持不变。

验证：`go test ./...`、`go vet ./...`、`go vet -tags integration ./...`、`go build ./...` 与 SQLite S3 集成测试通过。

## 2026-11-24 · 服务器接入模块骨架与零表面开关

- 新增默认关闭的 `serveraccess` 模块栈：面板 `serveraccess_enabled` 优先于 `SKIN_SERVERACCESS_ENABLED`，
  支持热切换；关闭时 `/api/serveraccess`、`/v`、`/servers` 与平台模块子树统一 404，不落入 SPA。
- 开启后提供租户、成员、邀请、服务器、凭证、策略、白名单与本服封禁控制面，以及仅超管网页会话可用的平台紧急控制；
  所有租户资源由会话成员关系推导并在 store 层强制 scope，跨租户读写返回 404，viewer 只读。
- 凭证使用 256 位随机 secret、只展示一次明文、数据库仅存 SHA-256 摘要；支持 24 小时重叠轮换与立即吊销。
  邀请原子单次消费，过期、重放、邮箱不匹配与停用租户均拒绝。
- 三引擎加法迁移 `0033_serveraccess_core.sql` 新增租户、成员、服务器核心表；`0034_serveraccess_control.sql`
  新增邀请、凭证、策略、白名单、封禁与租户服务器配额；表、列、索引与外键名一致，租户删除只级联 `sa_*`，不删除全局账号。
- 本切片不包含验证链、events 0035、前端、proxy 或导出实现；关闭时仍统一 404，不写库、不生成密钥。

验证：SQLite 迁移/仓库/API 红绿测试、角色矩阵与跨租户负向测试、邀请并发消费、凭证轮换吊销、双语与文档闸通过。

## 2026-11-22 · README 动图、面板自检卖点入表与 35 秒宣传片

宣传素材这一轮换了个出发点：先归纳「服主为什么会换」，再决定拍什么，而不是先看手上有什么素材。

- README 主图与三张功能卡全部改成动图（GIF，1920×1080 帧源出，主图 960×540 / 卡片 400×300），
  每处用 `<picture>` 在 `prefers-reduced-motion: reduce` 下回落到该动画自己的静帧。
- 三张卡片换成三个「bs 做不到或要额外部署」的点：**面板部署自检**（八项四态 + 修复话术 + 测试邮件）、
  **违规处置引用清点**（仍有引用就拦下来，改为下架留证）、**拖拽上传换肤**（本地预览 + 服务端渲染）。
- 对照表补一行「站半坏不坏没人告诉你」，把内置部署自检这个卖点写进 README；
  同时把「封禁换个名字就复活」一行的 bs 侧措辞收紧：裸站只在站内登录拒绝，进服面要靠 Yggdrasil 插件补——
  装了该插件的形态也拦 authenticate，不能写成「完全拦不住」。
- 迁移素材改成终端窗口形态。迁移预演只有 CLI、没有 Web 界面，上一轮把它画成产品表格是不诚实的；
  现在画面里的 `== import report ==` 与逐表 `selected/inserted/skipped` 形状直接取自 `printImportReport`。
- 新增 35 秒无声宣传片 `docs/public/promo/skinnexus-tour.mp4`（1920×1080/20fps，935 KB），
  文档站首页嵌 `<video>`；字幕已烧录，无音轨，配乐与配音留给后续人工步骤。
- Mojang 兜底静态卡撤下（该卖点仍在对照表里有一行），登录近景与迁移表格两张 PNG 一并退役。

## 2026-11-21 · README 视觉素材与 og:image

README 此前只有文字与徽章、缺视觉主图，文档站 og:image 一直留空（config 注释「品牌视觉素材留站长定」），
本轮用 stageshow 产出的实拍素材补齐两者。

- README 双语接入主图（登录直进服场景，1000×480）与三张特性卡（外置登录近景 / 迁移报告 / Mojang 兜底，
  各 400×300），落 `docs/assets/`，八条 alt 中英各一套、全部描述性非空。
- 对照表补两行（中英行集一致、保持三列）：账号级封禁（站内与 Yggdrasil 进服都拒，改名不复活）；
  Mojang 兜底默认开。bs 格按源码取证填写——`RejectBannedUser` 按 `users.permission` 拒绝站内登录，
  故写「站内登录按账号拒绝」而非「—」；皮肤兜底经查 bs 核心与 blessing-skin-plugins 均无
  sessionserver 查询，才写「—」。
- `README.zh.md` 首句补中文长尾词（我的世界皮肤站 / 外置登录 / Yggdrasil / 免 /login / blessing 迁移）。
- 文档站 og:image 上线：`docs/public/og-image.png`（1200×630），config 全站注入
  og:image / width / height + twitter:image，twitter:card 升 `summary_large_image`；
  同图就位 `web/public/og-image.png`（主站面板 `seo_og_image` 指向即生效）。

验证：check-docs（19 文件 / 53 环境变量双向）+ check-a11y + theme-contract + modal-chrome +
diag-model 12/12 + check-locales（126 码×2）全绿；VitePress 构建无死链，dist 含 og-image.png
与 summary_large_image；gh api markdown 渲染 README：12 张 img 全带 alt、5 个本地资源引用齐全；
五张素材经 stageshow film 静拍全部 no-issues-detected。

## 2026-11-19 · 文档状态对账（手册基线纠偏）

站长提出「不许出现文档说没实现、其实已实现」。全仓 100 篇 md 里的状态类断言（未实现 / 尚未 /
不支持 / 已知限制）逐条回代码取证，改 11 处。

- **反向错误（说没做、其实做了）**：玩家上限早已在建角时强制（`internal/account/players.go`
  超限回 `player.limit`），手册与 `store/web.go` 的注释都还写着「never enforced」；面板「联盟」页的
  健康状态与 HTML/JSON 诊断导出、访问日志明细的首次展开拉取，管理面板篇与审计篇仍列为「尚未」。
- **正向错误（写了、其实没有）**：监控篇把「邮件队列积压」列为关键指标（发信是同步 SMTP，没有队列），
  又说指标要靠「自行增加 metrics exporter」——零依赖 `GET /metrics` 已内置、`SKIN_METRICS` 默认关
  （关闭时回 404），`/healthz` 也早回 `version` 与 `uptime_seconds`；`deploy.md` 一处把面板的 CDN
  开关写成 `SKIN_CDN_*` 环境变量（该前缀不存在）。
- **会烧人的描述**：手册把 `SKIN_MOJANG_FALLBACK` 写成「Mojang 回源地址」并给了 URL 示例——它其实是
  布尔，照手册填 URL 等于把正版兜底关掉；`SKIN_PLUGIN_SECRET_KEY` 写成「插件签名密钥」（实为插件私密
  配置的 AES-256-GCM 主密钥，需一并备份）。两键按 `deploy.md` 真源改写，并补上手册缺的 `SKIN_METRICS`
  与 IndexNow 两键。
- **口径与自相矛盾**：手册基线声明由「快照 2026-11-15」改成实际钉住的 `797ae1a`（v0.6.0 / 11-12）并
  声明基线后能力以本文件为准；事件总线「不支持取消」加限定（通用事件不可取消，任务步内可自查
  `skin.jobs.cancelRequested`）；迁移编号「五位前缀」改四位；站点设置把 CSP 两键移出「面板键」表
  （env-only、非法值启动即失败），并标明该表只覆盖 43 个面板键中最常调整的 15 项。
- **已核实、不动**：`migrate` CLI 仍只有 `preflight`/`uuid`/`import`（"apply" 是 import 的文义，不是
  缺失动作）；任务队列确无删除端点；插件 schema 渲染、JWKS 单 key、ugctext 无敏感词表、`hookApi`
  固定 2、面板无 purge 卸载入口、用户管理无批量勾选——这些「未实现」仍然成立。

- **依赖（dependabot #17 / #18，minor+patch 政策内）**：`pgx 5.12.0→5.13.0`（修畸形服务端响应在 `Rows.Values`
  的 panic 与连接失败后的不安全重试）、`zitadel/oidc 3.51.17→3.52.0`、`x/crypto 0.58.0→0.59.0`、
  `modernc.org/sqlite 1.61.1→1.62.0`；前端 `vue-i18n 11.4.18→11.4.19`（`sanitizeTranslatedHtml` 安全修复）、
  `vite 8.3.6→8.3.8`、`@vitejs/plugin-vue 6.0.10→6.0.11`。vue-i18n 那条改的是翻译串里 HTML 的净化规则，
  本站 `v-html` 只用于品牌 SVG 与图标、不渲染词条，行为面不受影响。

验证：check-docs（19 文件 / 53 环境变量双向）+ check-a11y + gofmt/vet/build 全绿，VitePress 92 页
构建无死链；合并两 PR 后复跑 `go build`/`vet`/`go test -count=1`（全绿）+ SQLite 真库集成腿 +
`npm ci`（0 vulnerabilities）与 `npm run build` 全绿，五道自写闸（locales/a11y/modal-chrome/
theme-contract/docs）逐条绿。

## 2026-11-16 · 品牌图 alt 补描述性文本

必应站长工具报「img 缺 alt」。逐面实抓排查：可爬渲染面（首页/皮肤库/材质页/SPA 壳）没有缺 alt
属性的图，被报的是两处空 alt 品牌图——必应把 `alt=""` 当缺 alt 计。

- **修复**：SPA 壳启动占位的 logo（`web/index.html`）与材质页顶栏站点角标（`texturepage.go`）
  空 alt 改为描述性文本（产品名 / 站点名）；材质预览图等真装饰缩略图维持 `alt=""`（WCAG 正解）。
- **闸补漏**：`check-a11y.mjs` 此前只扫 `.vue`/`.go`，`web/index.html` 恰好在盲区——纳入扫描，
  并新增「favicon.svg 品牌图禁空 alt」规则（变异取证：回退 alt 即红且点名文件）。

## 2026-11-15 · UI 打磨轮与文档站建设

站长验收标注驱动的 UI 打磨（8 片）+ 查漏补缺 + 推广基建 + 文档站建设，同日 13 轮。

- **UI 打磨轮**：/user/apps 裸 404 根治（OIDC 未开时路由不挂载，site-config 增 `oidc_enabled`
  能力位按能力收放）；玩家侧列表去指纹 + 类型/模型标签本地化；上传表单拖放区合一 + 模型判定三态
  （二次精修换「皮肤|披风」分段按钮）；个人资料徽章化、admin 衣柜同级 chrome、举报/编辑弹窗套 SPA
  表单语言；全站清扫（横滚护栏、状态徽章、♥ 线性图标）；`/skinlib` 登录态双份闪修复。

**同日二至五轮（晴空琉璃 4.0.x 与宿主视觉）**：明暗刷新反色真因 = Cloudflare Rocket Loader 把主题
JS 推迟到首绘后执行，主题/自定义 JS 标签豁免改写（`36b7704`）；晴空琉璃 3.3→4.0「薄光」动效层
（reduced-motion 全静、高频面零循环）→ 4.0.1 色带分割（径向层裁断改渐融化）→ 4.0.2 导航窄屏重做
（菜单抽屉）→ 4.0.3 启动占位与加载屏同貌 → 4.0.4 按钮四款同盒（宿主 `415e275`）。演示站「修复不生效」
真因 = CDN 忽略查询字符串破防缓存键，治本为主题资源 URL 版本进路径（`9c4bfb7`）。

**同日六轮（查漏补缺）**：外部审查 4 条发现全修——插件桥 request() 点段逃逸改「解析后 pathname
必须留在插件前缀下」断言（`b21c9fc`，高危）；令牌吊销失败静默吞错 ×4 补审计留痕（`a7eeae4`）；
clientToken 比较改常量时间（`ef5bc97`）。新功能 **IndexNow 收录通知**（`43278b8`，默认关 + 面板
开关 + 密钥校验文件，事件批量提交 ≤10/批，env 同批改齐代码/compose/变量表三处）。

**同日七轮（推广基建）**：README 英文转主 + 长尾词与 topics（`69b27e9`/`c9893ba`/`a8765f3`）；
迁移演示夹具 `demo/legacy` 全虚构老站数据、preflight→import 全流程实测（`3b604b6`）；文档站上线
GitHub Pages（`ee4d429`）；竞品分析补录 chrly / HRPAuth / PigeonSkin 三家（`bfff63b`）。

**同日八轮（wiki 关停，手册迁入）**：知识手册 80 篇整册迁入 `docs/handbook/`（`6fbc9b1`），
GitHub wiki 关闭，文档单一真源 = 仓库 `docs/`。

**同日九轮（文档站迁 VitePress + Cloudflare Pages）**：SSG 拍板 VitePress（`5f15feb`）——Pagefind
中文搜索、mermaid、sitemap、llms.txt（AI 答案引擎入口）；正式域 docs.skin.yimingzhanshen.cn
（CF 管 TLS）。

**同日十轮（首页介绍页 + 修插值吞页）**：首页改 home 布局（hero + 六特性 + 文档导航）；正文
双花括号插值序列被 Vue 编译层当插值、整页正文静默丢失的根因修复（v-pre 包裹）+「空页即红」闸
（`79d8ad5`）。

**同日十一轮（九篇文档人读化重排）**：导读 + 章节导航表 + 深水折叠 + 表格瘦身，事实一条不删
（`b9fda16`）。

**同日十二轮（截图缺陷 + SEO 快赢 + 本文重排）**：migration checklist 代码围栏被解析成缩进代码块
的渲染缺陷修复（`e28074a`）；11 篇逐页 meta description、titleTemplate、lastUpdated、editLink
（`4ee1289`）；本文按格式规范全文重排。

## 2026-11-13 – 11-14 · v0.7.0 开发周期（W0–W7）

46 提交、八波推进（技术债清查 → 可观测/结构闸 → 功能/重构 → 视觉翻修 → 收尾）。

1. **技术债清查**：OIDC 错误分层（真 DB 错返 500 不再吞成 unauthorized_client）、认证服务器 URL
   归一化、三引擎版本谓词统一到 plugin/semver 单一真源、版本号一致性闸。
2. **可观测**：零依赖 `/metrics`（HTTP/队列/上传/Ygg 验签计数）+ healthz 版本与 uptime；
   slog 结构化日志迁移（4 批消裸 log）+ 裸 log 闸常驻 CI 防回潮。
3. **结构闸**：adminapi 注册即认证闸；settingsWhitelist 与错误码目录双向接线闸。
4. **前端重构**：抽 useSession/useGoto/useSiteConfig、认证五卡组件化、App.vue 拆壳层与路由，
   provide 键名冻结为四个。
5. **功能**：我的举报跟踪页（只看本人 + 分页）、admin 衣柜管理（改名/移除）、设备码有效期与
   轮询间隔面板可配（即存即生效）。
6. **视觉翻修**：theme-color 按有效 `--bg` 服务端计算、12 位等宽哈希铭牌贯穿三面、落地页首屏精选
   上场景板 + 文案去通用化、脱格值收进 4px 栅格并立栅格闸、管理面板密度统一、零依赖 check-a11y
   闸（th scope/table caption/img alt）接进 CI、默认 + 晴空琉璃双主题真机验证。
7. **收尾**：W7、登录态 `/skinlib` 双份闪修复、授权管理页 404 收口（OIDC 能力门控，G6 同批）；
   UI 打磨轮前两片（上传表单排版、玩家列表去指纹）同批落地并入 11-15 轮次。

工程健康：全闸绿（gofmt/vet/test + check-locales/docs/modal-chrome/theme-contract/a11y + web build）；
每片先红后绿 + 变异复证。发版门槛（演示站真机验收后打 tag）由站长拍板。

## 2026-11-12 · v0.6.0 发布

自 v0.5.0（11-01）以来 234 个提交的汇总发布。镜像 `ghcr.io/yimingzhanshen/skin-nexus:sha-<发版提交>`
（`:latest` 同指）；升级前备份数据卷与数据库。十组主线：安全与信任边界（私有渲染泄露、安装抢注
改一次性令牌、分页溢出与 LIKE 转义、令牌只存摘要、空摘要登录洞）；邮箱验证闭环（单次链接令牌 →
点击消费防预取、三封信双语 + HTML 副本、SPF/DMARC 自检，**验证门从只拦进服扩成注册必填 + 网页
身份操作 + 插件会话档同拦**）；四引擎与数据完整性；上手门槛（弹窗体系、穿到角色闭环、帮助中心、
管理面板真实 URL）；双语与 SEO/GEO（webi18n、两种语言两种 URL、hreflang）；插件与主题（市场、
init 幂等、任务取消感知）；Union 对齐（改名/自删通告 hub、SL 徽章、站名服务端读、CORS/HSTS 取证）；
可观测与运维（部署自检、≤24h 诊断模式、落地页统计缓存）；工程门禁与供应链（dependabot、CodeQL
清零、alpine 3.24）；视觉（默认主题 v3.3、晴空琉璃 3.3.0）。

**升级提示**：`require_verification` 覆盖面变了——开门前先看部署自检「邮箱验证门」行的未验证账号数
（blessing 迁入账号基本未验证，一键开门会全站落在验证引导屏）；管理面板新地址 `/admin/<tab>`。

同日站长标注三件：精选标题压邻卡（宿主 `max-width: 100%` 修复）；首页撤下说明书式事实块（真源在
契约文档）；CSP 放行站长第三方统计（`SKIN_CSP_SCRIPT_EXTRA_SOURCES` / `SKIN_CSP_CONNECT_EXTRA_SOURCES`，
默认留空、值启动时严格校验），连带教训：**部署侧加 env，代码、compose 示例、变量表必须三处同批**。
部署文档改「最少 env」口径（变量表加必填性列）。验证门补注册强制邮箱 + 判定收敛 `internal/verifygate`
（deny-by-default + 豁免表；超管是这扇门唯一的安全出口），自救三条路（重发/换地址/退出）在拦下屏。

## 2026-11-11 · 邮件改进轮（9 提交）

起因是演示站两次真实故障与站长三个问题。五片落地，验收在本地 rig + 假 SMTP + 真浏览器。

- **验证链接改点击消费**（邮件网关预取会烧掉一次性令牌）；占位邮箱 `admin@localhost` 止血（留空
  改 NULL，拨号前拒绝并给自救文案）。
- **三封信双语 + HTML 副本**：主题只烤 accent 进色带与按钮，正文恒浅底深字（邮件客户端暗色反转
  不可控）；补 Message-ID / Auto-Submitted，From 显示名缺省站点名。
- **部署自检加发件域 SPF/DMARC**（封闭集六项→七项）；DNS 改问公共 resolver（云内网 resolver
  缓存陈旧，SPF 加好了自检还报没有）。
- **瞬断重试一次**（DATA 被接受后绝不重试）；审计时间戳改 store 钟，玩家侧发信失败落审计并进
  运行状态「近 24h 发信失败」。

## 2026-11-10 · 联盟自助诊断轮（8 提交）

站长反馈「弹窗位置不对、诊断展示可升级」，两条都落到根因而不是改数值。

- **弹窗错位**：根因是 `.user-main > *` 的入场动画留下非 none transform，抢走 fixed 后代的包含块；
  改原生 `<dialog>` + showModal() 进 top-layer，白拿焦点归位、Esc 与可访问名。
- **诊断报告按腿分家**：hub 载荷里 `status_code/headers/body` 描述的是 hub 回探那一跳，第一跳失败时
  平铺读会产出自相矛盾的表——现按键分家、未测写「未测」不编结论；本站那跳由本站自观测 TLS 版本/
  证书到期/时钟偏差（判据复用入站验签常量）；失败原因给稳定 token 三态（通过/注意/失败）。
- **顺带修**：Diagnose 外层 10s 被内层默认 5s 嵌套吞掉的超时缺陷；`check-modal-chrome` 闸
  （第一版检查打在散文上，剥注释必须做在入口）；真机抓到 `close` 事件不派发改状态自置。

## 2026-11-09 · 收口优化轮（S4–S23 + 联盟链，44 提交）

三条挂账结清（D9 任务取消/恢复走通整圈、英文管理面板 40 条说明改键、明细 upstream_code 假 hub
读数）；插件长步内可自查取消（`skin.jobs.cancelRequested` 加法 API，不动 hook 合约字段）；机器名
双语（审计动作 53+ 与市场名按 locale）；可访问性（107 个 th 补 scope、表补 caption、两处对比度
令牌回填并手算复核）；死代码六处清零；CI 红过 8 提交的两条自造原因记账（真库腿须 `-p 1`、gofmt
债）；性能只量不改（落地页 likes 榜与 `/me` 成本随库线性增长，数字入仓外待办等拍板）。

**同日第二轮（视觉去生成感，默认主题 + 晴空琉璃）**：令牌契约两条窄闸（§3 默认值七处全是旧色板
抄本，当场纠偏）；层次不再用品牌色画（glow 改中性、`--shadow` 改 inset 顶边高光）+ 补 `--mono`；
越格值归 4 阶、场景雾永动循环删除；404 页第二真源并入令牌注入；诊断导出去 Bootstrap 色板；
晴空琉璃 3.3.0（7 个渐变字块整块删——只换值会留下隐形文字、玻璃收三站、字重字号归阶）。

**同日第三轮（主题规范三处）**：`.admin-nav-item` 幽灵类移除（全仓零命中）、SSR 皮肤库列表特异性
陷阱写进 §4、自托管像素字体栅格纪律写进 §12。

**同日第四轮（联盟 hub 站名消失）**：不带斜杠的规范地址被 ServeMux 回 307 且无 CORS 头，浏览器
整条按 CORS 拦掉；yggdrasil root/包内两层 + union 包内补规范地址注册，断言钉「恰好一个 ACAO」；
真机 A/B 收官（rig 两腿 200，生产两腿 Failed to fetch）。

**同日第五轮（名字该是人看的字）**：联盟列表列名改实话（浏览器端取名被自家 CSP 结构性拦死，删掉
不可能成功的代码）；衣柜别名优先有名字（分组取名与排序键错配，重复条目真实形状是一有名一 NULL）；
选皮肤弹窗去哈希 + 就地改名；GBK 别名静默坏数据改拒收（Go JSON 解码对非法 UTF-8 是替换不是报错）。

**同日第六轮（站名仍不显示）**：第二个 ACAO 来自宝塔 WAF「跨域设置」在响应阶段统一注入（nginx -T
搜不到、删站点配置没用），关掉即闭环；顺带量出 location 自写 add_header 会吞掉 server 层继承——
定位方法与验收进 deploy.md。

**同日第七轮（弹窗白底白字）**：`.picker-pick` 只改背景没改 color，名字一直在 DOM 里就是不在屏幕
上；同族规则扫描清零。HSTS 收进服务端 securityHeaders（https 判据，纯 http 不发一年期）。

**同日第八轮（联盟链整条对齐）**：改名/自删从不通告 hub（`SyncProfile*` 写好两周零调用者）——
三条腿统一挂 `account.Service` + 组装层守卫；SL 徽章补齐（读不到绝不显示 SL0，那是真实结论）；
`enabledFeatures` 补两项且按请求读；成员站名改服务端受控出站读取（可关）。CI 拦下重构的教训：
审计动作名必须出现在 emitter 调用里，双语闸才看得见。

## 2026-11-08 · 半套守卫与门禁盲区清账（10 提交）

- **两处「谎报完成」**：OIDC 吊销一半就报成功（错误被 `_ =` 吃掉照回 ok）；join session 表无索引
  也没人删行，hasJoined 每次全表扫（0030 补索引 + 挂进清扫器）。
- **入站校验缺口**：命名规则只管上传不管改名（改名可绕过规则）；衣柜别名不封顶不去隐形字符。
- **半套守卫三处**：SMTP 卡住时找回密码/邮箱验证两个请求挂 40 秒（注册路径早已 detached）；
  三处时间戳交列 DEFAULT、读取端按应用钟算窗口（统一到注入钟）；SPA 只写不读 `skin_lang` cookie
  把 SSR 页选的英文抹回中文（读序改 cookie 优先）。
- **门禁盲区**：check-locales 三处（seo 不在扫描面、未知前缀静默跳过、拼接键不查）+ UI 键地板
  （清 22 个死键后补）；errorgate 吃进 internal/oidc 当场抓出设备码同意页四条裸文案；
  UUID 铁律唯一回归接进真库腿。

## 2026-11-07 · 诊断模式与安全收口（18 提交）

- **真机走通诊断模式抓到三件事全修**：明细正文落库前脱敏（明文口令跟的是 90 天保留期不是 24h）；
  状态码恒 0 改 Observe 响应结束后回填；面板展开明细必 500（占位符双层套括号，此前从未真库跑过）
  补三引擎真库腿。
- **日志诊断模式 5a–5e 落地**：超管 ≤24h 自动过期诊断窗口（仅超管网页会话可开关）、明细表 0029、
  两条留存时钟分开（凭据列固定 24h、行 90 天）、面板逐行展开、读取端点不含凭据值。
- **空摘要登录洞**：未计算的 legacy 算式 + 空密码列 = 任何密码都能登录——一律先拒空摘要
  （行为变化：这类行走找回密码或管理员代设）。
- **skinDomains 带端口条目补去端口孪生**（只增不改，客户端比对的 getHost() 不含端口）；复用名重建
  收敛 ErrDuplicate 不再以内部错误冒出；SSR 页可点中·EN 对照 + `skin_lang` 服务端持久化；落地页
  answer-first 事实块；协议/旧站算式/UUID 链特征测试钉现状。

## 2026-11-05 · 安全加固复盘与 SEO/GEO（35 提交）

- **插件页主题令牌注入**（首帧即站点配色）：只注根令牌——第一版把整张主题表 link 进插件文档是
  回归（裸元素规则一起压进插件页）；抽取器防漂移闸管住九个回退值。
- **加固复盘九处「守卫只落了一半」**：主题市场三路由漏认证（面板里对谁都 403）、主题上传 16 MiB
  限额没机会生效、X-Admin-Token 压过会话把人判成机器、`skin_csrf` 多页互相顶掉、`/avatar/hash`
  兄弟路由漏私有门禁、LikePrefix 写好零调用点、登录日志留明文邮箱、安全头包装顺序漏 CustomSkinAPI、
  navigate 桥过滤可被反斜杠绕过。
- **E1 补到 OIDC**：`oidc_refresh_tokens` 并入 token_digest_v1（30 天有效期比原三列更长命，抓住
  唯一一次免二次不可逆重写的窗口）；**会话/启动器令牌只存摘要**（不可回滚升级，回滚语义写进
  deploy.md §2.5）。
- **SEO/GEO 两片**：两种语言两种 URL（`?hl=` 一等公民、hreflang、按语言列 sitemap）；皮肤库列表页
  第一屏服务端渲染（对不执行 JS 的爬虫此前这页是空的）。分页负 OFFSET 严重性上调（SQLite 会把
  超末尾页当第 1 页返回）；两个零测试的包补测（jobs 92.4% / errorgate 82.8%）；sitemap 上限单真源
  + 方向改最新优先；双语门禁第 6 条管到 `.vue` 模板区。

## 2026-11-04 · 安全段 A1–A10 与体验修正（19 提交）

- **三路审计定性**：确认无「可被外部输入驱动」的 SQL 注入（全仓 `?` 占位符 + dbx 统一改写），但补
  十个真实缺陷。两个高危：私有材质渲染面（tid 可枚举、私有图打 immutable 可推上 CDN）改本人/
  管理员 + `private, no-store`；安装抢注改一次性安装令牌 + 同事务原子占位。其余八个：分页溢出、
  LIKE 转义、source_url 校验时机、安全响应头基线、受控出站过渡地址、XFF 信任面收窄（默认行为不变）、
  错误体回显、postMessage 策略。
- **安全头两次打死自家功能被真机抓住**（plugin-assets 自己的 JS 被 CSP 拒、XFO 连自家 iframe 一起
  拒）——安全头这类全站中间件，验收必须包含被它约束的那个功能。
- **体验批（B1–B6）**：皮肤库上传入口、我的材质搜筛、穿到角色闭环、选人弹窗、面板「站内材质」
  工作面（行上下架/撤销判定/删除 + 新开 strike 端点）、任务队列取消恢复、审计可查；措辞与错误码
  修正（材质指纹、删除不存在的材质不再答「角色不存在」）。

## 2026-11-03 · 管理面板补页与材质生命周期（13 提交）

- **面板补三页**：材质、操作审计、任务队列（此前只有接口无前端消费者）；双语门禁第五条（视图
  引用的键必须真的存在，当场抓到三处裸键）。
- **真机抓出两处语义错位**：上传可见性复选框极性反了；重载按钮文案像加载状态。
- **违规内容地址上传硬拦**（一处覆盖 web 表单/插件桥/私有导入/launcher-api 四条路径，新码
  `texture.violation_reupload`）；**SQLite 行 id 不再回收**（AUTOINCREMENT + 已装库幂等重建，
  回收的 id 会把处置历史挂到无辜新材质上）；`fsck` 体检命令；空库安装向导 503 不再说「昵称被占」；
  面板测试发信 12s 内给结论。

## 2026-11-02 · 多引擎、错误契约与上手门槛（67 提交）

当天体量最大的一轮：四引擎落库、错误契约不可逆变更、双语 SSR、上手门槛两轮，全部逐片实测。

- **数据库引擎扩到三种（MySQL/MariaDB、SQLite、PostgreSQL）**：`internal/dbx` 统一占位符改写与
  时钟、`internal/dialect` 收全部引擎差异、DDL 拆三套等形树（列与索引逐棵比对由测试把关）；方言
  AST 门禁守住方言层之外的 MySQL 专有写法；CI 集成矩阵从 2 条扩到 6 条。blessing 导入源/目标各自
  选引擎，PostgreSQL 目标显式 id 导入后自动对齐序列。
- **错误契约与本地化（两条口径变更，不可逆）**：每个非 2xx 带稳定 `code`（errorgate AST 闸
  钉住，webapi 167 + adminapi 184 处归零）；`message` 改英文、`error` 等于 `code`，中文文案由
  `errors.<code>` 词典承担（103 码 × 2 语言，check-locales 双向钉）。凭据类错误文本只进日志；
  account 的 code→status 映射归位到自己的类型。
- **材质闭环三件套**：手臂模型按像素判定（不再采信表单声明）；旧 22×17 披风入库前补边（只作用
  新上传）；材质生命周期——举报处置状态化（下架单事务、违规不可转公开）、彻底删除留墓碑 +
  引用清点 409/`?force=1`、上传/删除按内容地址取 advisory lock 收敛并发。
- **邮箱验证闭环 + 站内通知中心**：单次链接令牌 → 点击消费 → 置标志，宽限期让门真能开；
  **改邮箱清验证标志并作废在途链接**（SQL 赋值顺序是坑）；notifications 表（封闭 kind + 事发时
  材质名快照 + 90 天保留）+ 拉取式未读角标。
- **衣柜塌缩 + 叫法统一**：换肤可穿集合 = 自己上传 ∪ 衣柜收藏（对齐 blessing「收藏即能穿」），
  上传即自动入柜 + 0022 回填；纹理→材质、♥→收藏，一个「改名」拆成三件具体的事；**封禁补 Web
  登录拦截**（原先 -1 只挡启动器和后台）。
- **上手门槛两轮**：上传限额理顺（1 MiB 传输层截杀大皮肤改路由放行 + handler 收口）、失败原因
  分码（尺寸/像素炸弹/元数据/非 PNG 四因四码）、登录锁定倒计时（429 带 `retry_after_seconds`）、
  42 处原生对话框全部改应用内 `<dialog>`（沙箱 iframe 没有 allow-modals，confirm 必须委托宿主）、
  待决态与 in-flight 守卫、58 处横幅补 live region、玩家帮助中心 `/help`（未登录可读）。
- **服务端渲染双语（U12/U13）**：`internal/webi18n` 两份目录 + 语言判定（cookie → Accept-Language
  → zh-CN），落地页/详情页/设备授权页/404 全入表；check-locales 扩 SSR 四条检查；「换肤下拉改读
  /me/wearable」「列表改服务端分页与搜索」同批。
- **管理端「角色管理」四条路由从来没通过**（路由绑 `{pid}`、handler 读 `"id"`，静默 400）——补
  routeparams AST 闸：路由声明的每个参数必须被 PathValue 读到，反向也查。
- **工程与供应链**：前端工具链三条大版本同批落地（vite 6→8 / plugin-vue 5→6 / vue-i18n 9→11，
  npm ci 归零 vulnerabilities）；CodeQL 首轮清零（4 条改代码、11 条带 `文件:line` 理由按误报关闭，
  不为讨好扫描器改写足够强的守卫链）；Dependabot 首批 6 条按 `not_used` 关闭并记判定依据；
  运行时 base 镜像 alpine 3.20（支持期已过）→ 3.24 + CI `pull: true`；dependabot.yml 上线四类
  更新周检；README 大幅精简（中 217→115 行）。
- **发版门禁与五项修正（v0.5.0 前置）**：覆盖率地板 40.0 进 server job（含 0% 包白名单）；
  面板密钥只写不读（空值=保持、清除显式 clear）；纹理 URL 基址单一真源（`internal/siteurl`，
  site_url 覆盖发生在监听之前）；未知 SKIN_* 变量启动即警告（KnownEnv 三向闸）；全量文档清查 +
  deploy.md 环境变量重建为 50 键分组全表。

## 2026-11-01 · v0.5.0 发布（47 提交）

- **v0.5.0 发布**：tag `v0.5.0`，镜像 `ghcr.io/yimingzhanshen/skin-nexus:sha-<发版提交>`；本轮范围 =
  公共纹理哈希去重、受控出站、持久任务队列与 skin-importer、主题市场、材质生命周期闭环、错误契约
  与质量闸。**口径变更两条不可逆**：非 2xx 带 `code` 且 `error == code`；`message` 改英文、界面
  文案由词典承担。
- **真机验收（生产）当场抓四项已修**：管理员无筛选材质库 500（空 WHERE）；`error`/`code` 两面
  不一致；33 处 500 吞原因（新增 writeLogged/writeInternal）；message 中文漏网两条。
- **市场零配置 + 主题市场上线**：官方注册表地址与验签公钥编译内置（零 env 开箱可用）；主题市场
  （独立 Ed25519 密钥、四道校验、热生效）+ 本地上传 + `pack-theme`/`registry --kind theme`。
- **默认主题 v3.3 六阶段**：令牌层补齐（--panel-2/--accent-ink 死引用激活、间距/层级刻度）、
  组件层统一（Pager/EmptyState/ToastHub/.tabs）、响应式（汉堡抽屉、44px 触摸）、可访问性
  （label/skip-link/alt）、打磨、单一真源（theme-base.css，SPA @import + SSR embed）。
- **私有预览授权修复**（/preview 对所有者/管理员放行、其余 no-store）；官方插件 UI 统一主题令牌；
  自定义页脚与贴底布局；运行状态扩展（构建信息/部署环境/版本注入）；竞品取证第三轮（快照口径
  重写、Drasl 补录、正版绑定出方案不出实现）；复选框拉伸修复。

## 2026-10-31 · 任务队列、材质元数据与渲染修正（43 提交）

- **插件 iframe 主题令牌**（`init.theme`）与插件资产缓存失效（URL 带 `?v=` + must-revalidate）；
  插件 i18n（`nav.<page-id>` 随宿主语言）。
- **持久任务体系补全**：每步结束释放租约并重排（取消/续跑不再等 30s）、`delayMS` 步距、取消中任务
  不被 Fail 复活、worker 按插件串行持 90s 租约、MySQL 5.7 兼容（保留字列、datetime 精度）、
  `run_after` 改 Go 时钟（+08 会话推迟 8 小时的根因）；`skin-importer` 改每条皮肤一个 step 防限速。
- **材质元数据与举报**：`detail_md/source/source_url` + texture_reports（0017），二创/转载必须给
  http(s) 来源；详情页渲染消毒描述 + 举报 dialog；管理端举报处理页；非 UTF-8 元数据拒收。
- **渲染修正**：legacy「背景透明」规则复刻（白 overlay 不再渲成白头）、躯干背面 UV 修正（u36→u32），
  预览统一带 `?v=5`；详情页 3D 查看器改用 `/texture-render/{hash}`。
- **公共纹理哈希去重**（`public_dedupe` + 唯一索引，0015 回填）；受控出站 `net:http`；持久任务
  `schedule:jobs`（0016）；`skin-importer` 上线（第 12 个官方插件）；模块化 UI 入口（邀请码/Union
  关闭即隐藏）；CDN 真实 IP 解析面板化；初始化账号升超管（幂等）；KV 配额 opt-in；
  前端启动并行请求 + 路由拆 chunk（入口 439→207 KB）。

## 2026-10-30 · SEO/GEO 基建（5 提交）

SEO 标题长度引导（不足 15 字符自动补全 + 字符数提示）；WebSite + Organization JSON-LD、
`og:site_name`、Twitter Card、`/llms.txt` 公开页面地图；README 界面预览四张截图；
零编辑部署 `docker-compose.quickstart.yml` + README 首屏可复制命令。

## 2026-10-29 · 打磨与社区基建（3 提交）

插件管理页重排（自适应卡片行、权限折叠）；CI 修复与提速（签名脚本、依赖缓存、concurrency）；
社区准则补齐（CoC/SECURITY/无障碍声明/issue 与 PR 模板）、`<html lang>` 随语言更新。

## 2026-10-28 · v0.4.0 与通用扩展面（10 提交）

- **v0.4.0 / 通用扩展面**：slot 全接线（新增 3、删 3 未接线 ID）、`ui.forms` 宿主表单字段、
  `media:avatar` 头像 302、`profile.update.check`/`player.rename.check` guard、`skin.host.info()`
  内省、本地上传信任分级（verified/unknown-key/unsigned/invalid）+ 摘要绑定风险确认；新增 4 个
  官方插件；0014 plugin_meta.trust/signer。
- **Launcher API v1**：bearer 路由档（Ygg/OIDC token→uid，无 cookie/CSRF）、`internal/account`
  共享账号服务、插件账号桥、base64 二进制上传；官方 `launcher-api` 插件 16 端点 + OpenAPI。
- **插件市场仓库化**（源码迁 skin-nexus-plugins，PR 投稿 + CI 签名）；**v0.3.0 签名市场首发**
  （skin-plugin CLI、registry、Pages 分发）；插件平台 v2（P0+P1：manifest v2、四档路由、
  命名空间数据、sandbox iframe、健康/路由/日志）+ P2 Core（Ed25519 registry、原子替换、
  升级回滚）；API 收敛（只认 hookApi: 2）；加载硬化。

## 2026-10-27 · 插件宿主落地与 v0.2.0（13 提交）

- **插件宿主 v1（goja）**：manifest 校验、单插件 VM 超时/panic fail-open、6 hook 挂载、
  只读动态路由、启停/重载 + 审计、管理面板「插件」页；examples 两个离线示例 + plugin-spec。
- **v0.2.0 发布**：A1 自助找回、A2 Mojang 兜底、C1 登录/通信日志、SSR SEO 内容页、v3 视觉体系、
  HTTP 超时与优雅停机（SIGTERM 8s 排水）。
- 前端逻辑修复批 A–C、admin 视图 lint 存量清零（31 条）、晴空琉璃 3.0→3.2 迭代、主题上传脚本
  加固（PS5.1 编码事故驱动）、开源仓库整理（删 PoC/蓝图/重复规范）、隐私修复（学号样例假值化）、
  全量文档审计 25+ 处。

## 2026-10-26 · v3 视觉体系与 A1/A2/C1（28 提交）

晴空琉璃「差异最大化」样板 v3 + 主题能力边界规范终版；v3 全站视觉「月下仙山」（token 架构、
分栏场景认证、图标导航）；上传拖放 + 选肤 3D 预览、Union 黑名单翻页、公告 markdown 渲染；
**A1 找回密码**（SMTP 全链路：防枚举/单次令牌/面板热配置）、**A2 Mojang 兜底**（出站防护 +
热开关）、**C1 运维观测面**（登录与通信日志，90 天保留）；竞品对照矩阵上线。

## 2026-10-25 · 正式切流与 v0.1.0（24 提交）

- **正式切流完成**：67 用户 / 61 纹理零丢行、UUID 零漂移、签名钥连续性实测。
- **性能实测**（2 核 / 3.5GB 生产 VPS）：落地页 ~600 RPS、纹理分发 5182 RPS、Yggdrasil 元数据
  2365 RPS，全程零错误。
- 站点 Logo 全家桶、服务端渲染访客首页 + 逐页 SEO meta、高清皮肤原生分辨率渲染 v3 + UV 亚纹素
  采样（撤销降采样方案）、预览 URL 版本化（破一年 immutable 缓存）；启动器深链/拖拽载荷修复、
  UI 自定义体系修复 + CONTRIBUTING、三路文档静态审计、英文 README、**v0.1.0 首发**。

## 2026-10-24 · 管理面板对齐与用户中心（31 提交）

- **管理面板对齐 blessing B1–B5 全批**：侧边栏/仪表盘/运行状态（B1）、用户管理表 + 权限三护栏
  （B2）、角色管理表（B3）、站点配置扩白名单（B4）、Union 运维按钮 + 联合黑名单代理（B5）；
  令牌双档 TTL；settings 审计不落密钥明文。
- **用户中心 A 级批次**：自助端点 + 侧边栏多页（仪表盘/衣柜/角色/资料）；公告 Markdown 双层消毒 +
  site_url 接线。
- **皮肤库全栈**：列表筛选/搜索/排序/分页、点赞=入柜、可见性三档；服务端头像/预览渲染接口
  （对齐 blessing TextureController）；头像面部裁剪对齐老站语义。
- **Union 配置 options 化 + 热切换**（关态门禁先于鉴权、hub 公钥现取）；MySQL 5.7 兼容两处
  （COLLATE 列侧、prepared 1267）；公开迁移手册 `docs/migration.md` 上线。

## 2026-10-23 · P1 主干与迁移导入器（60 提交）

立项后最密集的一天：兼容面收口、P1 功能主干、迁移导入器与四路审查修复同时推进。

- **P1 功能主干四件**：改密端点 + 面板；衣柜 closet（1:1 legacy user_closet）；邀请码模块（1:1
  线上魔改版：存储/注册流/管理面，CSV 发码归插件候选）；ygg_rate_limit（滑动窗口 + 失败锁定
  双层）——限流 429 偏差备案同步。
- **迁移导入器 apply 路径**：ID 1:1 保留、幂等、五步清洗接线；启动追踪迁移（方案 A，既有库自动补
  新迁移）；加密数据兼容加固（PHC 短路 + 密码算法分类）；duplicate-collision 匹配带 SQLSTATE
  真实格式（崩溃循环根因）；生产 dry-run 对账实录入盘。
- **Yggdrasil/OIDC 面**：`require_verification` 门改配置开关（默认关，拆除无验证邮件流时的进服
  死胡同）；skinDomains 对齐旧站公式（Union 成员域聚合白名单）；双态 token resolver 补不透明
  token 回落；OIDC 路由被 webapi 子树抢路由修复（device 页/授权管理页 404 根因）。
- **四路审查修复**：迁移链加固 + 凭据/令牌生命周期（切流验收前置）、限流器防护 + userapi 真状态码、
  UI 审查两批（衣柜 i18n 键、错误信封全解析、remap 目标域）、union_kv 持久化（hub 密钥跨重启
  续存）、管理端删号通告 hub（SyncProfileDelete 落地）；切流阻塞项清零收官。
- 兼容矩阵文档上线（实测基线 2026-10-23）；MUA remap 收敛；测试夹具唯一化（撞昵称/撞身份修复）。

## 2026-10-22 · 立项与一期 M0–M5（15 提交）

- **立项**：三仓库（blessing-skin-server / yggdrasil-connect / authlib-injector）源码级分析、
  技术栈冻结 **Go + Vue 3**、插件 v1 = goja；建仓 `YimingZhanshen/skin-nexus`（AGPL-3.0，署名 YMZS）。
- **一期 M0–M5**：Yggdrasil 全端点契约测试、密码 12 算式移植、纹理管线、migrate 引擎与 CLI、
  Vue 前端与管理面板、公开 API、CI 与文档。
