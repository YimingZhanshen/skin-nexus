# 变更日志

> 迭代历史（按日期倒序，每天一条，只记要点）。开发细节见对应 commit；README 状态行只留一句话摘要。

## 2026-11-02

- **两个列表改服务端分页与搜索，换肤下拉改读「可穿集合」接口**：`/me/textures` 此前无 LIMIT，
  738 条材质一次渲染；衣柜的 tab 与搜索是客户端在整份列表上做的，分页后会静默漏掉不在当前页的命中。
  现在 `/me/textures?page=&pageSize=`（默认 50、上限 200）带 `total`，每行带 `inCloset`
  （EXISTS 子查询，走 `user_closet` 已有的 user 索引），仪表盘的「入柜/已入柜」不再需要把整个衣柜拉到前端；
  `/me/closet` 的 `q`/`type`/分页全部下推 SQL（大小写折叠用 `LOWER() LIKE`，三家一致）；
  新增 `/me/wearable?type=skin|cape` 返回「自己上传 ∪ 衣柜收藏」的并集（别名优先做 label），
  角色页下拉改读它。衣柜表补「材质名称」列——备注名为空时整行原本没有任何可读名字。
  实测：`pageSize=1` 时 `total=2` 且两页各一条、`inCloset` 分别为 true/false；`?q=BOBS` 命中
  `Bobs Public Skin`、`?q=zzz` 返回 0；角色下拉给出 `Bobs Public Skin` + `Named Skin Alpha`
  且已挂载值 3 正确回填。

- **预览与操作口径收口（衣柜/我的纹理/角色卡）**：预览 URL 此前有两套拼法、且只有皮肤库用服务端渲染的
  小图，「我的纹理」和衣柜直接拿原图当缩略（64×64 摊在行里既糊又看不出画了什么）。现在
  `/me/textures` 与 `/me/closet` 都带 `previewURL`（皮肤给 `/preview/<tid>?v=<渲染版本>&small=1`，
  披风为空——渲染器只画皮肤），拼法收敛到一个 `previewThumbURL`。顺手两处：衣柜里披风行的
  「设为头像」不再渲染（后端本就 422，那是个死点击），角色卡里 PID 与 32 位 UUID 改为上下两行
  （原来左右挤在一行，UUID 断成 "PID 1 / 2" 那种难看折法）。实测：衣柜披风行按钮只剩改名/移出，
  皮肤行缩略图为 `/preview/3?v=5&small=1`，角色卡 UUID 独占一行不换行。

- **衣柜接回换肤链路（对齐 blessing 语义）**：老站上「收藏」就是「能穿」——换肤强制校验材质在不在
  衣柜里（`PlayerController.php:216-218`），上传即自动入柜（`SkinlibController.php:327`）。
  我们此前把这两条拆了：衣柜只是收藏夹，换肤读的是「我自己上传的」，后端还硬性要求 owner 是本人，
  于是收藏进来的社区材质**永远穿不上**，只能重新上传一份。现在换肤可穿集合 = 自己上传 ∪ 衣柜收藏，
  上传成功后为 owner 补一条衣柜行（衣柜写失败只记日志，不让上传跟着失败），
  `/api/v1/me/closet` 带上材质本名以便给非自有条目命名，角色页下拉改读这个并集。
  实测：A 收藏 bob 的公开皮肤后 `PUT /me/players/1/skin` 由 `texture.not_owned` 变成 `ok`，
  下拉里出现 `Bobs Public Skin` 且与已挂载值对得上；bob 的**私有**皮肤 A 既收藏不到（`texture.not_found`，
  与不存在同答复，不做状态预言）也穿不上。`InCloset` 与衣柜 join 补了真库集成用例。

- **纹理命名与显示对齐 blessing（第一片）**：「我的纹理」此前只有 hash 没有名称列，同一件材质在
  皮肤库有名字、在自己列表里却只是一串字符；hash 在三个界面分别截成 12/16/10 位。现在列表加
  「材质名称」列，hash 截断收敛到一个 helper（`web/src/textureLabel.js`），选完文件后名称框自动
  填文件名去扩展名（bs 的 `Upload.tsx` 就是这么做的，我们此前要求手打）。实测：列表显示
  `Named Skin Alpha` / `Cape Beta`，选 `skin-real.png` 后名称框得到 `skin-real`。

- **角色管理改成卡片网格，预览里能看到披风**：预览此前是表格里被 CSS 压到 48×72 的小图，
  而 `SkinPreview` 只调 `loadSkin`——服务端渲染器不画披风，所以换没换披风只能「打开页面发现变了」。
  现在 `SkinPreview` 多一个 `capeUrl`（空值走 `loadCape(null)`，是真摘下来而不是留着上一件），
  角色页换成响应式卡片：160×240 真尺寸预览 + 皮肤/披风选择器 + 改名/删除，窄屏一列、不再横向滚动
  （旧表 `min-width: 860px`，「删除」在常见宽度下被滚出可视区）。两处顺手修正：选择器改显示材质名
  （原来只有哈希前缀，没人能凭 `8723b27959…` 认出是哪张），预览模型跟着已挂载皮肤走
  （Alex 皮肤此前一律按 Steve 画）。实测：挂上披风后画布非透明像素 17390 → 17522，摘掉回到 17390。

- **插件 iframe 的 `init` 只发一次**：宿主在收到子页 `ready` 与 iframe `load` 时各发一条握手，
  同一个 frame 文档因此被 init 两遍；靠 `appendChild` 建列表的插件 UI 于是把每一项渲染两次
  （皮肤导入页「应用到角色」下拉里 1 个角色显示成 2 行）。现在 `ready` 是权威握手、`load`
  只在从未收到 `ready` 时兜底，`retry()` 换 frame 时重置。本地实例 + 真浏览器实测：修复前
  `/api/plugins/skin-importer/_csrf` 与 `/players` 在资源计时里各出现 2 次、下拉出现 3 项
  （占位 + 同一角色两遍），修复后各 1 次。插件侧同步把角色下拉改成先清后建（skin-importer 1.1.1），
  停在旧宿主上的安装也不会再重复。

- **数据库引擎扩到三种（MySQL/MariaDB、SQLite、PostgreSQL）**：连接层与方言层各就一位
  （`internal/dbx` 统一占位符改写、时间参数归一与连接池设置，`internal/dialect` 收全部引擎差异），
  建表 DDL 拆成 `migrations/mysql|sqlite|postgres/` 三套等形树（30 张表、列与索引逐棵比对由测试把关），
  `schema_migrations` 记下本库由哪棵树迁移，跨引擎指向直接拒绝启动而不是半途改坏表。
  存储层此前散落的 MySQL 专有写法（`NOW(3)`/`DATE_ADD`/`INSERT IGNORE`/`ON DUPLICATE KEY`/`<=>`/
  `IF()`/`GROUP_CONCAT`/`CONCAT()`/`COLLATE utf8mb4_*`/`FOR UPDATE`/`GET_LOCK`/`DELETE … LIMIT`）
  全部改到可移植写法或引擎助手，并由一道 AST 门禁守住：方言层之外出现这些拼写即 CI 失败，
  门禁自身带自检用例（含"派生表里的 LIMIT 属合法写法"），不会退化成永远绿。
  时钟统一：TTL、租约、时间戳一律绑定 Go 侧 UTC 时刻，写与比用同一只钟。
  CI 集成矩阵从 MySQL 5.7/8.0 两条扩到六条（MariaDB 11.4/10.11、PostgreSQL 16、SQLite 文件库），
  只在一台引擎上有意义的用例显式跳过并报出所需引擎。blessing 导入的源端与目标端各自选引擎，
  PostgreSQL 目标在显式 id 导入后自动对齐序列。面板运行状态页改报数据库引擎与版本。
  覆盖 42.0% → 43.7%。
- **把二进制真跑起来才发现的两处**（SQLite 本机起站 + 浏览器走完安装/登录/上传/皮肤库/外置登录握手）：
  ① Windows 托管的站所有 SPA 静态资源 404——包含性检查用未清理的相对目录 `web/dist/` 去比
  反斜杠拼接出的路径，Linux 容器不受影响，本地一跑立刻白屏；② 全新 SQLite 站的第一个请求
  `GET /api/v1/setup/status` 直接失败——安装向导的表存在性探测写着 `information_schema` 与
  `DATABASE()`，SQLite 根本没有这两样。两处都补了用例与门禁条目（`information_schema`、
  `DATABASE()`、`@@session` 一并进禁用清单）。
- **搜索语义在三家对齐**：管理端按角色名/昵称、访问日志按 who/ip/detail、皮肤库按材质名的检索，
  此前靠 MySQL 列排序规则做大小写折叠，换到 PostgreSQL 就搜不到——现在两边都 `LOWER()`。


- **通宵计划复核后的三处补齐**（逐条对着计划查现状，发现三处说做到没做到）：
  - 发信失败**即删令牌**：邮件没出去，链接就不该留在库里当一个「没人收到的活凭据」（`DeleteEmailVerifyToken`，
    集成用例覆盖删自己不影响别人）。
  - SSR 顶栏补未读角标：落地页与材质详情页此前只有「登录/用户中心」，已登录访客在服务端渲染页看不到
    「有 N 条没读」；现在与 SPA 侧栏同源（`/me` 的那个计数），未读为 0 不渲染空角标，
    **匿名渲染绝不带计数**（那是能把别人未读数泄进可缓存页面的路径，测试专门钉住这条）。
  - 计划里的「单元测试」一项按现状改口：令牌生命周期与改邮箱清标志是**真库集成用例**（MySQL 5.7/8.0 两条腿）
    覆盖的，比打桩更硬；`webapi` 处理器的 HTTP 层断言需要能替身 `*store.Mysql` 的接缝，那属于下面
    多引擎改造的产物，不拿假单元凑数。

- **站内通知中心**：玩家的材质被下架、举报被认定成立或驳回、账号被封或被解禁，此前只能靠「打开页面发现变了」
  才知道——唯一的即时通道是一次性 toast。本轮补上收件箱：`notifications` 表（uid + 封闭的 kind + 事发时
  的材质名快照 + 已读时间），90 天保留与访问日志同口径、写入时顺带清理过期行；
  `GET /api/v1/me/notifications`（分页 / 只看未读）与 `POST /api/v1/me/notifications/read`（单条或全部）。
  - kind 是**闭合集合**、文案由前端词典渲染，与错误码同一套做法：中英任一边缺键 `check-locales` 就失败。
    快照材质名是必要的——材质之后可能被彻底删除，只留 tid 的通知届时什么也说明不了。
  - 发射点在管理端处置的**成功分支**上：举报 resolve/dismiss/hide（`adminapi/reports.go`）、违规判定解除、
    以及权限**跨过 -1** 时才发的封禁/解禁（重复保存同一权限不再堆通知）。写入失败只记日志：
    丢一条通知不该让处置失败，审计日志才是权威记录。
  - 未读数是拉取式的：挂在 `/api/v1/me` 上（`unreadNotifications`），侧栏渲染角标，没有轮询也没有推送。
  - 收件箱按 uid 分区：`MarkNotificationRead` 的 WHERE 同时带 id 与 uid，猜别人的 id 只是匹配不到，
    既不报错也不泄露对方有没有这条。
  - 测试：3 条集成用例（分页与总数、只看未读、跨账号标记无效）在 MySQL 5.7/8.0 两条腿上跑；
    `go test ./...` 全绿，覆盖率 41.6%（地板 40.0），0% 包集合不变。

- **邮箱验证闭环**：`email_verified` 字段与 join 门禁早已就位，但**没有任何途径能把标志置为真**（只有管理员代设），
  所以那个开关实际只能用来把人锁死。本轮补上缺失的一半：`POST /api/v1/email/verify`（只发给**当前登录账号自己**
  的邮箱，不收地址参数，因此无法拿它给别人发邮件）→ 24 小时单次令牌 → `POST /api/v1/email/confirm` → 置标志。
  令牌形状与找回密码一致：只存 SHA-256 摘要、过期用 DB 时钟比较、未知/过期/已用一律同一句话（不做状态预言）。
  - **宽限期**让它真能开：面板 `require_verification` + `email_verify_grace_days`（N 天内注册的新号即便未验证仍可进服），
    两个都是请求期解析、改完即生效；`CreatedAt` 为零值时**不享受**宽限（fail closed）。开启门禁时注册会自动发信，
    不让新号自己去找按钮——但发送是**脱离请求**的：mailer 每条消息有 30s 截止线，挂在注册请求上会让一个
    SMTP 故障把注册拖成 30s（前端 15s 就超时，玩家会看到「网络问题」而账号其实已建成）。站点没开门禁就不发这封邮件。
  - 一并修掉一个真洞：**改邮箱会清空验证标志并作废在途链接**——否则一次验证就能给任意新地址背书。
    SQL 里 `email_verified` 的赋值刻意放在 `email = ?` **之前**：MySQL 的 SET 从左往右求值，后面的赋值看到的是
    已更新的列，放后面就等于拿新值和它自己比，永远相等、标志永远不清。
  - 前端：`/email-verify?token=…` 落地页（进来即兑换，单次令牌不让人再点一次）+ 个人资料页「发送验证邮件」。
  - 测试：令牌生命周期/过期/最新覆盖/改邮箱清标志 5 条集成用例（MySQL 5.7 + 8.0 两条腿）；
    门禁宽限期 4 条单元测试；`check-locales` 双向卡新增 4 个码的中英词典命中。
  - **演示实例真机验收抓到两处接口测试永远看不见的缺陷**：
    ① `/email-verify` 没进服务端 SPA 路由白名单（`cmd/skin-nexus/main.go` 的 `isKnownSPAPath`），邮件里的链接
    会在壳加载之前直接 404——令牌接口全绿也照不出「没有人能点开这个链接」。已补白名单，并加断言
    **凡要写进邮件的路径必须在壳路由白名单里**（撤掉白名单条目该测试即失败）。
    ② 个人资料页的「发送验证邮件」在站点没配 SMTP 时仍然显示，点它只会吃 503；改为沿用找回密码那条
    `mail_configured` 显隐规则——装不上的功能不给看。

- **对外文档口径收敛**：仓库只写「现在是什么」与「为什么不做什么」，不再写「还没做什么」。移出仓库的内容：
  README 中英两版 `## 路线图` 整节与站长段末的「尚未实现」句、`compat-matrix.md` §4 的「正版账号绑定」行、
  `competitor-analysis.md` 的我方自查清单与「将来要做 · 参考实现」索引、`docs/license-binding.md` 全文
  （纯方案零实现）。原文与判据另存于仓库外，不随本仓库分发。
  - **保留**的是有证据的现状与取舍：对照表里对手占优的格子（element-skin 覆盖率硬阈值 90% vs 我方地板 40%、
    我方前端无 lint 闸）、`ACCESSIBILITY.md` 的「尚未完成独立无障碍审计」（删掉它等于暗示已达标）、
    `compat-matrix.md` §4 的取舍与偏差行、`competitor-analysis.md` §3 的取舍与「伪需求」判定。
  - 顺带修两处「文档说的和机器做的不一致」：`plugin-spec.md` §4 把 `ui:page`/`ui:slot`/`ui:assets` 写成权限位，
    实际宿主按 manifest 的 `ui` 段做结构校验（槽位白名单、iframe 必须配 `ui.assets`）；`plugins:invoke`/`audit:write`
    与 `mail:send`/`cli:commands` 一样是「合法权限名、宿主无对应运行时面」，并入同一行说明。
    `deploy.md` 的 `SKIN_REQUIRE_VERIFICATION` 从「尚未实现，保持关」改为实际后果：开启后未验证账号一律拒绝进服，
    迁移站要先核对存量 `email_verified` 标志位。
  - 删死代码：`server/internal/auth/authsource.go` 整文件零引用（`AuthSource` / `Registry` / `Identity` 全仓无调用点）。
  - 闸实测：`node scripts/check-docs.mjs` → `ok (18 files, 45 env vars documented both ways)`，无死链；
    `go build ./...` 与 `go vet ./...` 干净；`go test ./...` 全绿，total coverage **42.0%**（删掉不可达语句后由 41.8 上移），
    0% 包集合仍是 `internal/errorgate` + `internal/jobs` 两条、与 `testdata/coverage-floor.txt` 钉死值一致。

- **前端工具链三条大版本全部落地**（#6 plugin-vue 5→6、#4 vite 6→8、#5 vue-i18n 9→11，顺序有讲究：单升 vite 会在
  `npm ci` 就 ERESOLVE）+ `vite.config.js` 改用 `import.meta.dirname`（vite 8 明确警告原生 config loader 不提供 `__dirname`）。
  - 构建契约实测保持：`dist` 仍是**单份 CSS**（`cssCodeSplit:false`，主题覆盖顺序依赖它）与**固定名 `assets/viewer.js`**
    （SSR 材质详情页硬引用它，哈希名会破契约）；skinview3d 仍单独分包。
  - 浏览器实测（vite 8 dev + 桩 API，vue-i18n 11.4.16）：中英切换正常（Sign in / Sign up / Skin library），
    `errors.*` 词典全部命中——`errors.auth.credentials` → "Wrong account or password"、`errors.common.internal`、
    带参的 `errors.texture.in_use`、`errors.player.name_invalid`、`errors.network`。
  - 副带收益：`npm ci` 现在 **0 vulnerabilities**（此前 4 个 high 全在 vue-i18n 9 的传递依赖里），
    CI 的 `npm audit` 与 `deploy.md` §6.1 挂的 high 待办一并清掉。
  - 记一次误判免得下次重犯：我先用**不存在的码** `auth.bad_credentials` 打桩，看到界面回退成后端中文 message，
    一度判成「v11 破坏词典」。真实键是 `errors.auth.credentials`，回退本来就是正确行为。教训是断言必须用码目录里真存在的键。

- **CodeQL 首轮清零**：15 条全部处置完——**4 条改代码后由扫描自动关闭**（zip 落点两处、SPA 资源探测、`?next=` 跳转），
  **11 条按误报关闭**，每条在告警上留了 `文件:line` 的判定说明。前端那条第二版改用 `new URL(next, location.origin)`
  比 origin，而不是手写前缀正则——扫描器认这个形状，而且浏览器怎么解析就怎么判，`javascript:`/`data:` 也因
  origin 为 null 一起挡掉；生产页面实测矩阵见提交说明。分诊由三路只读代理并行读码 + 本人逐条复核行号后定性。
  - 改：`plugin/archive.go` 补目录穿越的落点校验（同 `signedpkg/extract.go` 已有的那段，此前**只有那一个**提取器有）；
    `signedpkg/extract.go` 把校验提到 `IsDir` 分支之前——原来 `os.MkdirAll` 跑在校验前面，防线漏掉了目录项；
    `cmd/skin-nexus/main.go` 的 SPA 资源探测先做包含性判断再 `os.Stat`（只读探测，但越界路径会泄露目录外文件是否存在）；
    `plugin/assets.go` 拒绝控制字符与 NUL（保留 unicode 文件名，插件作者可以用非 ASCII 资源名）。
  - 关：`mailer.go` 的 email injection（收件人经 `mailRe` + `checkAddr` 双重拒绝 CR/LF，主题 Q-encoding、正文 base64，
    且已有 `TestSendRejectsHeaderInjection`）；`compress.go` 的反射 XSS（该行走的是 gzip 透传，HTML 全部经 `html/template`，
    唯一反射路径是标准库 `http.Redirect` 自带转义）；`main.go` 的开放重定向（跳转目标是站长配置项并写入时校验，
    请求参数只贡献 `?` 之后且逐个 `QueryEscape`）；三条 legacy 密码哈希（MD5/SHA256/SHA512 只用于**比对**
    blessing 存量哈希，`hmac.Equal` 消费，新密码一律 argon2id，且只有 `password_algo LIKE 'legacy%'` 的行会走到这里）；
    三条预分配（容量取分页/批量上限并已有 clamp，不是 `COUNT(*)`）。
  - 没有为了讨好扫描器改写已经足够强的守卫链：`plugin/assets.go` 的 `path.Clean` + 段检查 + 两次 `filepath.Rel`
    包含性 + `EvalSymlinks` + `Lstat` 只允许常规文件，是读得懂的防御，改成正则白名单反而会让 CodeQL 满意而让代码变差，
    所以那两条走带理由的关闭。
- **Dependabot 告警首轮 6 条全部关闭（`not_used`）**，判定依据如下（**Dependabot 告警的 API 没有评论字段**，
  只有 code-scanning 有，所以理由记在这里而不是告警上）：
  - `vue-i18n` / `@intlify/core-base` / `@intlify/shared` 的 5 条（3 High + 2 Low）：两条 advisory 都关于
    `escapeParameter` 的转义不完全。本仓库**从未开启该选项**（`web/src/i18n.js` 的 `createI18n` 只有
    legacy/locale/fallbackLocale/messages），且**全仓没有 `srcdoc`**——插件界面是 `:src` URL 加载
    （`web/src/components/plugin/PluginFrame.vue:203`）；翻译结果只走文本插值（`web/src/components/ToastHub.vue:41`
    `{{ x.message }}`），带进来的服务端参数（角色名、计数）由 Vue 转义。`v-html` 的几处分别是硬编码品牌 SVG、
    来自 `navIcons` 常量表的图标路径、以及 markdown-it + DOMPurify 之后的公告/详情正文。
    升到 11 仍按计划做（PR #5），但动机是 `npm audit` 的 4 个 high，不是这几条 advisory。
  - `go-chi/chi` 的 X-Forwarded-For 伪造（Moderate）：`server/go.mod:21` 标 `// indirect`，全仓零 import；
    HTTP 栈是 `net/http` 的 `ServeMux`，客户端 IP 走自研 `ygg.ClientIP`，chi 的中间件从未挂载。
- **dependabot 第二批 actions 升级**：合并 `codeql-action` 3→4 与 `docker/setup-buildx-action` 3→4；
  `actions/checkout` 4→7 因与前者改同一文件冲突（PR 变 CONFLICTING），直接在 `ci.yml` 落 7 处并关闭该 PR。
  三条前端大版本 PR（vite 6→8 / plugin-vue 5→6 / vue-i18n 9→11）继续开着，等专轮一起做。

- **dependabot 首批 9 条 PR 处置**：合并 go 补丁组、`golang:1.26→1.27`、三条 GitHub Actions 升级；
  关闭 `node:22→26`（26 不是 LTS，依赖分析不认 LTS 线）并在 `.github/dependabot.yml` 给 node 加 major 忽略；
  vite 6→8 与 @vitejs/plugin-vue 5→6 **必须同批**（单独升 vite 会 ERESOLVE 装不上），与 vue-i18n 9→11 一起留作前端工具链专轮。
  Dockerfile 里「builder tag 必须等于 go directive」的注释按实际语义改准（须 ≥，前进正是拿到工具链补丁的方式）。

- **运行时基础镜像过期修复（自查发现）**：`Dockerfile` 的运行层一直钉在 `alpine:3.20`，而 Alpine 3.20 的 main 仓库支持期 **2026-04-01 已结束**（3.21 也于 2026-11-01 到期）——也就是说 4 月以来构建的每一个镜像都跑在拿不到安全更新的 base 上。运行层升到 `alpine:3.24`（支持到 2028-06）。构建层刻意保持浮动（`golang:1.26-alpine` / `node:22-alpine`，不进最终镜像），但为此必须在 CI 打开 `pull: true`，否则 buildx 缓存会复用旧 base，「重建即补丁」不成立——已加。实际用的 Go 版本不用猜：面板「运行状态」报 `goVersion`。
  - 触发点是一次外部讨论：有人拿 Go/Docker 生态近期的高危 CVE 质疑「你的栈就没洞？」。逐条核过本项目的暴露面：构建时 RCE 走 cgo/SWIG，本仓库 `CGO_ENABLED=0` 且无 SWIG、CI 只构建 `go.sum` 锁定的依赖，攻击面结构上不存在；Docker 那几条容器逃逸的前提是「对不可信容器 `docker cp`」或「装 AuthZ 插件做多租户」，本部署是单机跑自己签名的镜像，前提不成立；标准库的 ASN.1/TLS DoS 两条确实相关，修复动作就是重建。**结论不是「我们没洞」，而是「修一个洞要等谁」——而这次自查证明我们自己也有一条没跟上。**
  - 落地为制度：`docs/deploy.md` 新增 §6.1「base 镜像与依赖的更新节奏」（各层支持窗口表、每季度 15 分钟自检命令、critical 阻塞 / high 只记 summary 的既有分工），并登记待办：本仓库尚无 `.github/dependabot.yml`，开了之后 go.mod / package-lock / Docker base 三类更新会自动开 PR。
- **依赖更新自动化上线**：新增 `.github/dependabot.yml`，每周一（Asia/Shanghai）检查 go.mod、npm、Dockerfile 的 `FROM` 行与 GitHub Actions 四类更新。minor/patch 每生态合并成一条 PR、major 单独开（vue-i18n 9→11 这类会自己冒出来，不会淹没在小更新里），PR 走同一套 CI。§6.1 的待办转为已启用，并记下一个覆盖不到的口子：dependabot 的 docker 生态只读 Dockerfile，`docker-compose.yml` 里的 `mysql:8.0` 仍需手动核。
- **README 大幅精简**（中 217 → 115 行，英同步）：删掉重复的两段部署说明、逐条罗列的功能清单压成四段、竞品对照表从 17 行收到 8 行（完整逐格证据本来就在 `docs/competitor-analysis.md`）、文档索引从表格改为分组链接行；语气去掉宣传腔。理由很直接：路人看 README 是为了三分钟判断「这东西对我有没有用」，不是为了读完它。

## 2026-11-01

- **v0.5.0 发布**：tag `v0.5.0`，镜像 `ghcr.io/yimingzhanshen/skin-nexus:sha-<发版提交>`（CI 只在 main 的 push 上出镜像，
  tag 本身不触发构建；`:latest` 只是便利标签，生产建议钉 `sha-`）。本轮范围=公共纹理哈希去重、受控出站、持久任务队列与
  `skin-importer`、主题市场、材质生命周期闭环、错误契约与质量闸（P0–P9）。**口径变更两条不可逆**：每个非 2xx 都带
  `code` 且 `error == code`；`message` 改英文、界面文案由 `errors.<code>` 词典承担。主仓 `version` 常量仍为 `0.5.0`，
  下一轮功能开发开始时再一起升 `0.6.0`（四处：`server/cmd/skin-nexus/main.go`、`Dockerfile`、`.github/workflows/ci.yml`、
  README 状态行与徽章）。

- **真机验收（生产 `bd9dbf2`）当场抓出四项，均已修**：
  - **管理员无筛选的材质库查询直接 500**：`store.SkinlibOpts.where()` 在「查看者是管理员 + 没有 filter/keyword/uploader」时返回空串，调用方拼成 `... textures t WHERE ORDER BY` 被 MySQL 拒绝。SPA 四个 tab 恒带类型筛选，所以浏览器里永远看不出来；任何按文档省略 `filter` 的 API 客户端（或将来加「全部」tab）都会踩爆。生产实测矩阵确认边界：`?filter=skin`/`?keyword=a`/`?uploader=8` 全 200，无 filter 与 `?page=2`（跨类型翻页）全 500。修法是补一个 `1=1` 兜底，并加免库单测把「WHERE 不得为空」钉住（同类的 `jobs.List`/`ListReports` 本来就写死 `1=1`，只有这处漏了）。
  - **`error` 与 `code` 在玩家面不一致**：`webapi.writeErrCode` 发的是 `http.StatusText(status)`，而 `docs/launcher-api.md` 与本仓库注释都写作 `{"error":"stable.code"}`，`adminapi` 与 `account` 信封也早就是 code——同一个约定两种实现。形状测试当时只断言「`error` 非空」，所以字面量闸和形状闸双双放行。现两面统一为 `error == code`，并把该断言补进测试（`error != code` 即失败）。
  - **33 处 500 把原因吞了**：`writeErrCode(w, 500, "list failed", ...)` 既不记日志也不回客户端，线上只能靠复现猜。新增 `writeLogged`/`writeInternal`（日志带方法+路径+底层 err，响应固定 `operation failed`），33 个调用点全部收敛——这正是 P3 定的「改码必须补日志」纪律，本轮补上它自己的欠账。
  - **`message` 仍有中文漏网**：角色名长度/规则两条提示是 `store` 现场拼的字符串，字面量闸扫不到（闸只看调用点的字面量）。改英文；界面文案不受影响，因为 `errors.player.name_invalid` 词典命中在前（实测：中文界面「角色名不合法」，英文界面 "Player name is not valid"，服务端 message 已是英文）。
- **生产实测通过的验收项**（补齐上轮挂着的三条）：`GET /api/admin/v1/settings` 三个密键恒空值 + `secret`/`set` 布尔（`smtp_pass` set:false 走 env、`union_member_key`/签名私钥 set:true 且不回显），`PUT smtp_pass=""` 返回 `ok:true` 且状态不变——空值即保持的语义成立；禁用 WebGL 后 `GET` 面板降级为 `image-rendering: pixelated` 的 `<img>`（players 页无 canvas 残留）；错误词典在中英文界面均命中真实后端响应。
- 尚未在生产验：面板改 `site_url` 后三处绝对 URL 一致——该项会在演示实例上做（生产改基址会让已签发的 immutable URL 与新 URL 并存，属于有代价的操作）。
- 验证：`go build`/`gofmt`/`go vet`（含 integration tag）/`go test ./...` 全绿；新增免库单测两条。

- **竞品取证第三轮（快照口径 2026-11-01）**：`docs/competitor-analysis.md` 重写为按各仓 HEAD 提交日锚定的快照，逐格 `文件:行` 证据且**我方自己那一列同样给证据**；10-26 正文整体降级为附录 A，不再冒充当前事实。新增 Drasl（Go，Yggdrasil + services/discovery 全族）与 LittleSkin 正版校验生态的取证，并把差距按「真缺口 / 伪需求」排序：对象存储与纹理失效仍是最大真缺口；`/publickeys` + certificates 确有真实消费方，但取证修正了上一轮的判断——authlib-injector 自己本地生公钥与 profile key，只有**不挂 agent 的原生客户端**才是服务端端点的消费者；「签名密钥轮换」判为伪需求（没有已泄露的密钥要处理）。另收四家工程纪律（覆盖率闸、错误三元组、公开配置白名单、迁移前备份、未实现端点答 501）与反面教材，含**我方自查清单**：`internal/auth/authsource.go` 是无人引用的占位、插件 manifest 声明的 7 项权限未被强制、码目录 3 个码尚无发出点、7 条管理路由无 SPA 消费者、墓碑表只增不清。快照会过期：引用前重新取证，这条写在文档表头。
- **正版账号绑定：出方案不出实现**（新增 `docs/license-binding.md`）：路线 A（Microsoft/XBL/XSTS + entitlement 验签，绑长期身份）与路线 B（Drasl 式无状态换肤挑战，不绑账号）按依赖、留存、撤销、失败模式、适配成本逐维度对比，结论是先做**方案 0**——零新出站依赖，用现有只读兜底 + 面板灰度把「绑定被冒用 / 进服被踢」压到可接受，路线 A 单独分段拍板。七条设计问题必须在实施前落定（票据生命周期与撤销、与封禁面的交集、`require_verification` 的语义冲突、材质 URL 与 `skinDomains`、无正版玩家的可玩性、面板灰度回退、审计保留期）；外部前置动作清单（Entra 应用注册与回调可达性、域名/ICP、公钥分发、客户端密钥只走 env）；红线写死 LittleSkin 的「凭缓存码按邮箱匹配免密继承旧绑定」为禁止形态，并明确 `enforce-secure-profile` 与混合认证不兼容、我方无 services/discovery 族故「不挂 javaagent 接入」当前不可达。排期触发条件三选一写在文档开头。
- **对照表与索引同步**：README 中英两表按同快照更新（新增纹理生命周期、错误契约与结构闸、质量门禁、邮箱验证/通知、无 agent 客户端接入面五行），并说明 Drasl / LittleSkin 为何不进表（定位不同：无 SEO、无皮肤库运营面）；`compat-matrix.md` §4 补一行「`uploadableTextures` 按位裁剪」的取舍——本站恒答 `skin,cape` 与「上传不按类型设限」的事实一致，要做按位裁剪得先把整型档位权限模型改成能力位；两份 README 与 `AGENTS.md` 的文档索引加入 `docs/license-binding.md`。
- 验证：`node scripts/check-docs.mjs` 通过（相对链接零断链、表列数一致、快照日期必须先于其管辖的首张表、`deploy.md` 变量表与 `KnownEnv`/`ComposeEnv` 双向对齐）。许可证边界：只吸收概念与公开协议事实，**不复制、不翻译改写** Drasl（GPL-3.0）与 element-skin 等源码、译文或测试 fixture，引用一律注明来源与 `文件:行`。

- **发版前门禁与五项实测修正**：
  - **CI 门禁**：覆盖率进入 server job——`go test ./...` 与阈值一次跑完，`total 40.0%` 记在 `server/testdata/coverage-floor.txt`（实测 41.8%，留 1.8 点余量），并列出**允许为 0% 的包**（`internal/errorgate` 是测试引擎、`internal/jobs` 仍缺单测），集合一变即失败，避免"删掉最后一个测试文件"这种隐形倒退；`npm audit --audit-level=critical` 作为阻塞线（当前 0 critical，4 个 high 需要 vue-i18n 9→11 大版本升级，先写进 step summary 不混进本轮）；新增 CodeQL（go + js，告警进 security 面板、不进分支保护，基线未审前不当闸）与 zizmor（工作流自检，non-blocking，版本钉死）；新增 `scripts/check-docs.mjs` 并配独立 docs job：相对链接零断链、GFM 表列数一致（`\|` 是数据不是分列）、竞品对照表必须在首张表之前声明 `快照口径：YYYY-MM-DD`、`deploy.md` 的变量表与 `config.KnownEnv`/`ComposeEnv` **双向**对齐。
  - **面板密钥只写不读**：`GET /api/admin/v1/settings` 原先原样返回 `smtp_pass`/`union_member_key`/`OAuth2 签名私钥` 明文（只有前端做了掩码），现读侧一律空值 + `secret`/`set` 两个布尔，写侧空值=保持原值、清除是显式 `clear`；审计脱敏改由 `secret` 标志驱动，PUT 不再回显 value。前端删掉硬编码的 secret 键清单，改吃接口字段。
  - **纹理 URL 基址只有一处**：`store.Open` 在面板 `site_url` 覆盖 env **之前**就把基址烘进了连接池，于是 `store.TextureURL` 一路给旧值、`webapi`/`publicapi` 给新值——而 Yggdrasil 签的正是最终那串。新增 `internal/siteurl`（原子基址 + `Texture/Avatar/Preview/AvatarUser/Page`），启动时装 env 值、option 解析完再覆盖一次，监听器打开前完成；`store.Open` 少一个参数。两道闸兜底：源码里除该包外不得把基址与 `/textures`、`/preview`、`/avatar` 拼接（当场抓到 `/avatar/user`），三层输出同 hash 必须逐字节相等。`deploy.md` 两处把该缺陷写成契约的话（"面板改 site_url 不影响纹理 URL"）已按实际语义更正，SMTP 口令"只走 env"一行也与面板可写的事实矛盾，一并改。
  - **未知 SKIN_\* 变量启动即警告**：`internal/config/env.go` 成为唯一清单（`KnownEnv` / `ComposeEnv` 逐条注明消费者 / `TestOnlyEnv`），拼错一个字母的变量过去只会让站点安静地用默认值跑下去。三条闸钉住：源码 AST 扫到的 env 读取必须登记、登记项必须真有人读、compose 模板插值的 `${SKIN_*}` 必须有人消费。
  - **SMTP 五键只读一次**：此前重置邮件器与面板占位在 `main.go` 里各读一遍，两份可以漂移；现归 `config.SMTP`，面板用 `PanelMap()`（少一个密码字段）。
  - **无 WebGL 时预览组件自己降级**：`SkinPreview` 原先只发事件、留一块空白画布，接了事件的 Dashboard 会换布局、没接的 players 页就只剩看不出原因的空框；探测结果改由组件持有并直接渲染扁平皮肤图。
  - 验证：`go build`/`go vet`（含 `-tags integration`）/`go test ./...`/`gofmt` 全绿，覆盖率实测 41.8%；`npm run build`、`check-locales`、`check-docs` 通过；新增单测覆盖密钥掩码读写两侧、SMTP 两份 map 的键集、env 清单三向、siteurl 时序与逐字节一致。浏览器实测已做：英文界面登录失败显示词典文本、页面无中文泄漏、`texture.in_use` 插值渲染成句子。**待实测**：面板改 `site_url` 后三处绝对 URL 一致（需可达后端）、禁用 WebGL 后 players 页出 `<img>`、密钥保存后 DB 值不变——并入 0.5.0 发版前验收清单。

- **错误契约与本地化（本轮口径变更，两条不可逆）**：
  - **每个非 2xx 响应都带 `code`**。此前 `internal/webapi` 167 处、`internal/adminapi` 184 处错误只有人话没有码，前端无法取词，英文界面只能显示中文。现在两面归零，并由 `internal/errorgate` 的 AST 闸钉住：错误调用必须带 code、code 必须是 `internal/i18n` 或 `internal/account` 的常量、message 字面量禁止出现中文。闸最初是「预算文件只许降」的形式（167/87/0/44 与 184/49/0），两面都归零后预算文件删除、转为硬失败。
  - **`message` 改为英文，`error` 改为等于 `code`**。中文文案自此由 `web/src/locales/{zh-CN,en}.json` 的 `errors.<code>` 承担（103 个码 × 2 语言，`scripts/check-locales.mjs` 同时钉住中英叶键相等、码全覆盖、且不允许留下后端已不存在的词条；该脚本已进 web job）。这是对 `curl` 与日志读者的口径变更：状态文本不再出现在 body 里，`launcher-api` 文档早就写作 `{"error":"stable.code"}`，本次把玩家面与管理面对齐到同一约定。前端 `describe()` 在三个传输出口（`api.js` 的 fetch 与 XHR 上传、`adminApi.js`）收口，词典命中优先、未命中回退 `message`，因此后端可以先加码、前端后补词，不会出现空白界面；94 处 `err.message` 调用点零改动。
  - **凭据类错误文本只进日志**。34 处 `err.Error()` 直回客户端按归属分流：存储/驱动/出站失败改 `writeInternal`/`writeLogged`（日志带方法+路径），而安装计划原因、插件 guard 拒绝语、主题包校验失败这类**本仓库写给调用方的话**经 `writeDomain`/`errDomain` 显式声明并保留。`adminapi` 中间件里手写的 JSON 响应收进 helper——`w.Write` 出去的 body 闸看不见。
  - **`account` 的 code→status/信封映射回到归属地**（`Error.Status()`/`Error.Body()`）。此前玩家面与自己的 `writeAccountErr` 各写一张表，管理面又手写第三张：同一个「材质被占用」在玩家面答 `texture.not_found`、在管理面答 `player.not_found`，且新增码会静默落 500。现在两面共用一套映射，表与测试（覆盖 16 个码 + 未知码必 500）随 `internal/account/errorhttp_test.go` 一起归位。
  - 附带修正 Yggdrasil 四处中文错误文本为英文协议短语（`error` 值与 `{error,errorMessage,cause}` 形状不动，那是被测试锁死的协议契约）与 `thememkt` 六条包校验原因。
  - 验证：`go build`/`go vet`（含 `-tags integration`）/`go test ./...` 全绿；`internal/webapi` 从无测试到 19 条免库信封形状用例；`npm run build` + `check-locales` 通过；浏览器实测（Vite dev + 桩 API）：英文界面登录失败显示 "Wrong account or password"（服务端 message 仍是中文，词典命中在前），页面无中文泄漏，`texture.in_use` 插值渲染为 "Still in use: 2 players (Steve, Alex), 1 favourites, 0 avatars"。

- **材质闭环三件套**：
  - **手臂模型改为按像素判定**。此前 `model` 完全采信上传者声明（`webapi` 直接取表单字段），声明与像素不符时游戏内手臂宽度、`/avatar`、`/preview`、皮肤库 steve/alex 筛选四处一起错。现由 `internal/texture/model.go` 采用 Mojang `HttpTexture#isSlimSkin` 规则判定：64×64 逻辑坐标下右臂第一层两块 slim 空白区 `(50,16,2,4)`、`(54,20,2,12)` 出现任一透明像素即 slim，仅方形且边长为 64 倍数的图参与（按 `scale` 平移，高清皮同样适用），64×32 恒判经典。刻意不采纳 skinview3d 的左臂区与纯黑/纯白启发式——单色经典皮肤那些区域本来就是真实贴图，采纳会把它们误判。上传仍接受 `model` 但只作建议值（非法值仍 400，与像素不一致时留一行日志，这是发现某启动器恒传错值的唯一途径）；64×32 判不出来属于已知盲区，故保留 owner/管理员的 `PATCH {model}` 纠正通路并写审计。前端不再提供下拉，改为按像素显示识别结果，材质列表内可纠正。
  - **旧 22×17 族披风入库前补边**。该族此前原样入库，游戏内显示错位。现在解码后按「宽取 `max(64, ⌈w/64⌉·64)`、高取 `max(32, ⌈h/32⌉·32)`」补透明边到 64×32 的整数倍（两边各自取，同倍会把 22×34 无谓放大到 128×64），图像贴左上。**只作用新上传**——改写存量文件会移动内容地址、打断所有引用，与 legacy byte-exact 策略冲突。补边按类型门控：披风补完可能正好 64×64，透明补边区会被模型判定读成 slim。
  - **材质生命周期补全**。① 举报处置状态化：`textures.moderation`（迁移 0018）记录违规判定，「下架」把转私有 + 清公共去重键 + 结案报告合成**单事务**（此前是两次独立写入，中间失败会留下报告已结案而材质仍公开）；被判违规的材质**不能再次转公开**（403），判定可由站长解除，解除只恢复资格不自动公开。② 彻底删除：`DELETE /api/admin/v1/textures/{tid}` 与 `DELETE /api/v1/me/textures/{tid}`，缺省拒绝并回传阻塞引用清单（角色名/衣柜数/头像数，409），`?force=1` 显式解绑（只置 NULL，玩家回到默认外观，不删角色）。删除写入 `texture_tombstones`（迁移 0019）保留上传者/类型/hash/来源/可见性/处置态/操作者/理由，同内容地址最后一行删除时才 unlink 文件。`texture.delete.check` guard 与 `texture.deleted` 事件**从「已声明」转为已接线**（此前全仓零调用点）。举报列表与详情的 `JOIN textures` 改为 LEFT JOIN + 墓碑 COALESCE——否则删除材质会顺带销毁举报历史。管理面板材质删除入口挂在举报视图（材质无独立管理 tab）。
  - **上传与删除的并发**：文件写在行存在之前、删除在计数之后，跨文件系统与 DB，行锁与 gap 锁都不足以收敛（上传的 `Save`→`INSERT` 之间无事务）。改为按内容地址取 MySQL advisory lock（`GET_LOCK('tex:'+hash 前 32 位, 5)`，锁连接固定、退出必释放），上传与删除共用；取锁超时返回 409 而非挂住。DB 删除已提交而 unlink 失败只记日志——「行没了文件留下」是可自愈孤儿，反向不可。
  - **口径修正**：`internal/texture/pipeline.go` 那句 `ref-count handled at DB layer` 注释此前与事实相反（全仓无 `DELETE FROM textures`），删除路径落地后才成立。
  - 验证：`go build`/`go vet`（含 `-tags integration`）/`go test ./...` 全绿；`internal/texture` 新增像素判定与补边几何表驱动测试（调色板、灰度、HD 128/256、64×32、纯黑回归锁、44×34→64×64、220×170→256×192）；`internal/store` 新增 integration 用例覆盖处置原子性与封禁门、同 hash 多行回填幂等、删除引用清点与墓碑、advisory lock 互斥（**本机无可达 MySQL，这三组集成测试待 CI 的 5.7/8.0 矩阵实跑**）；`npm run build` 通过。真实浏览器与游戏内验证（slim 皮手臂宽度、补边披风对齐）仍在待发版验收清单上。

- **市场续签补全（分发仓库）**：主题市场此前**只有推 `themes/**` 才签名**，没有到期自动续签——放置 90 天后注册表过期，市场浏览/安装直接失败（宿主对磁盘 last-good 缓存同样做到期校验）。现补 `resign-themes.yml`（每周一 03:41 UTC 检查、剩余 <60 天自动重签推送），并把插件侧 `resign.yml` 的阈值从 30 天提到 60 天：除了留出失败余量，更因为 GitHub 对 60 天无任何推送的公开仓库会**自动停用 schedule**，~5 周一次的续签提交让仓库始终"活跃"，定时任务不会悄悄停摆。签名确定性已在临时副本实测：三个主题包 zip SHA-256 逐一不变，只有 `registry.json` 的时间戳与签名位移，`verify-registry` 通过。`docs/plugin-publishing.md` §7 与 `docs/deploy.md` 同步口径（过期只伤市场，已装插件/主题与本地上传不受影响）。

- **全量文档清查与环境变量总表**：`docs/deploy.md` §2.3 由「一段话塞 15 个变量」重建为**分组全表**——50 个变量（46 进程 + 4 compose 专用）逐行给默认值，并区分「源码默认 / 镜像内 ENV 覆盖」两层与 `unset ≠ 空串` 语义；SMTP 五键、Union 八键、限流两键拆成独立行；补上此前只散落在别处的 `SKIN_PORT`、`SKIN_IMAGE_TAG`、`SKIN_DB_*`；面板与 env 的**分键优先级**改为可查表。
  - 事实纠正：`AGENTS.md`「config.go 是全部 SKIN_* 的唯一入口」为假（五个变量散在 `main.go`/`adminapi`），已改并注明真源；迁移水位 `0001…0014` → `0001…0017`（deploy.md、migration.md）；`CITATION.cff` 声称 0.5.0 已于 2026-10-28 发布 → 改回 `0.4.0`；`SECURITY.md` 支持版本补 `main`（0.5.0 开发中）行，issue 模板版本占位从 `v0.5.0` 改 `v0.4.0`；`plugin-spec.md` 三处「未实现」其实已实现（`jobs`/`media` manifest 面、`requires`/`conflicts` 安装计划强制、卸载 `purge`），插件装载语义统一为「面板重载即生效」；`theme-spec.md` 删掉无消费点的 `--text-dim` 幽灵令牌、把 `.ghost`/`.danger-outline` 修正为实际的 `button.ghost`/`button.danger-outline`、补齐缺失的 `## 7` 父标题；`CONTRIBUTING.md`「未签名包会被拒绝」与超管风险确认的实际行为矛盾，已改写。
  - 降噪：`compat-matrix.md` §4 由完工流水账改为「缺口与取舍」登记表（邮箱验证流、纹理删除面、正版绑定为真缺口；「换肤必须先入柜」与 429 偏差备案记为取舍）；`competitor-analysis.md` 去掉已完成项的过程叙述、失效的 `refs/` 本地路径与内部技能引用，标注快照口径与「本文不含 blessing 证据」；`CHANGELOG.md` 三个重复的 `## 2026-10-28` 标题合并为一条；`GOVERNANCE.md` 指向 README 不存在的「作者与署名」章节、PR 模板 `AGENTS.md` 相对链接断裂（`.github/AGENTS.md`）均已修；`README.en.md` 补齐落后一整个迭代的英文事实（主题市场与本地上传、材质元数据/举报/私密上传、页脚、`llms.txt`、文档索引与路线图）。
  - 顺带补一处可观测性：启动日志新增 `theme market: market=… trustKeys=N themesDir=…`，与插件市场那行对称，便于确认两个 keyring 各自生效。验证：`go build`/`go vet`/`go test ./...`（27 包）全绿，文档检查脚本确认 0 断链、50 个变量在 deploy.md 全部命中、CHANGELOG 无重复日期标题。

- **市场零配置**：官方注册表地址与官方验签公钥（插件 `market-2026-10`、主题 `theme-market-2026-11`）一并**编译内置**到二进制，`docker run` 裸跑或 `go run` 不设任何 env 即可浏览/安装两个市场（此前主题市场提示「未配置」）。env 语义保持「unset = 官方默认、显式空串 = 关闭」，compose 里的两行只是把下游覆盖点亮出来；`docs/deploy.md`、`docs/plugin-publishing.md`、`docs/theme-spec.md` 同步口径。
- **主题市场上线**：管理面板「外观」新增主题市场（官方注册表浏览/安装/更新/卸载）与本地主题包上传（zip ≤16MiB，两步检查→确认）。与插件市场同仓不同路径（`themes/v1/`）、**独立 Ed25519 密钥**（`theme-market-2026-11`，`SKIN_THEME_TRUST_KEYS`/`SKIN_THEME_MARKET_URL` 配置）；市场包四道校验（注册表签名→size/sha256→包内验签→theme.json 结构），安装/卸载**热生效无需重启**，使用中主题禁止卸载，写操作要求超管网页会话+同源 CSRF。`skin-plugin` 新增 `pack-theme` 与 `registry --kind theme`（确定性 zip、同版本不可变）；官方首批上架 azure-glass/ember-forge/verdigris-atelier，线上注册表已发布验证。详见 `docs/theme-spec.md` §7.1。
- **文案实体与孤行修复**：`admin.pluginsHint` 在文本插值里写了 `&lt;名称&gt;` 实体（文本模式不解码，页面显示实体原文），改为裸 `<名称>`（中英）；落地页一句话简介的 `max-width: 46em` 把「服务器」劈成 8 字孤行，放宽到 62em 使整句单行排下。
- **复选框拉伸修复**：全局 `input{width:100%}` 把「设置为私密材质」等复选/单选控件拉成整行蓝块（用户标注）；现为原生控件重置 auto 宽度 + 朱砂 `accent-color`，触屏 44px 命中区改由包裹的 label 承担，并清除各组件里零散的 `width:auto` 内联补丁。
- **默认主题全面升级（v3.3 六阶段）**：
  - 令牌层：补齐 `--panel-2/--accent-ink`（此前为死引用）、新增间距刻度 `--sp-1…8` 与层级刻度 `--z-topbar/menu/toast/modal`；徽章改 `.badge.danger/ok/jade/gold/accent/dim` 变体类，清除 6 个管理视图的内联改色；SSR 与 SPA 字号/动效令牌对齐。
  - 组件层：新增 `Pager/EmptyState/ToastHub` 共享组件与全站统一 `.tabs/.tab`（描边胶囊）、`.spinner`、`.table-scroll`、`.ghost/.small/.danger-outline` 按钮与骨架屏；管理侧栏并入 `.nav-item`；`skin-toast` 事件自此有全局消费者（`aria-live`、自动消失）。
  - 响应式：≤640px 顶栏操作收纳为汉堡抽屉（`aria-expanded`、Esc/换页自动关闭）；`pointer:coarse` 44px 触摸目标；网格 `min()` 防溢出、`100dvh`、`overflow-wrap:anywhere` 替代 `break-all`。
  - 可访问性：登录/注册/找回/重置、安装向导、后台 SEO/外观、`SchemaForm` 全部补 `label for/id`；SPA 与 SSR 详情页加 skip-link；面包屑 `aria-current`；补齐缺 `alt` 的预览图；`#8884` 等不达标记号入令牌。
  - 打磨：动效一律走 `--t-*/--ease`；皮肤卡 hover 抬升 + 预览微放大；`font-size:12px` 等杂值收敛到字阶；邀请码页/举报原因/插件加载文案入 i18n。
  - 单一真源：`server/cmd/skin-nexus/theme-base.css` 承载 `:root` 令牌块，SPA `@import`、SSR 两页 `//go:embed` 注入（模板测试断言令牌随行），SSR/SPA 值漂移自此结构性消灭。
- **私有预览授权修复**：`/preview/{tid}` 不再对私有纹理一律返回 404；所有者与管理员（`permission > 0`）可查看自己的/待管理的私有预览，匿名和其他用户仍 404。私有响应改为 `Cache-Control: private, no-store`、`Vary: Cookie`、`X-Robots-Tag: noindex`，避免共享缓存或搜索引擎留存。
- **官方插件 UI 统一**：`skin-importer` 1.1.0、`nav-links` 1.0.3、`player-notes` 1.0.3、`texture-provenance` 1.0.2 使用同一套宿主主题令牌（新增 `link/danger/ok`）、字号/间距/`panel` 背景与内容高度自适应；页面型 iframe 自带标题，宿主不再重复渲染。
- **管理侧栏一致性修正**：管理面板标题去掉内联上边距并置于首位，默认主题下与用户侧栏「用户中心」同级，横向 dock 下也保持居中；「返回个人页」移入顶栏，管理子项补上与用户中心一致的线性图标。
- **视觉一致性收口**：SSR 首页/详情页改用与 SPA 相同的 `--bg/--panel/--border` 合同键与表面圆角/阴影，统一页脚和面包屑的颜色与间距，修正浏览器 `theme-color`；SPA 顶栏改为与内容区同宽对齐，品牌名改用后台配置的站点名（不再固定显示 SkinNexus）。
- **运行状态扩展**：构建阶段通过 Docker/CI 注入 `version/commit/buildTime/buildBranch`，管理面板「运行状态」分组展示构建信息、部署环境（新增 `SKIN_ENV`）、OS/架构/主机/容器/启动时长、站点路径、MySQL/迁移与模块；README 与部署/构建文档同步。
- **默认主题与插件宿主布局**：SPA 内容区改为 1200（后台 1440），SSR 落地页 1040、详情页 940；修复 flex 列中 `margin:0 auto` 导致内容按内容宽度收缩（插件页曾被压到约 300px）的根因；后台隐藏重复的用户侧栏并新增「返回个人页」，插件页去掉宿主重复标题与双层内边距。`PluginFrame` 的 `init.user` 改为可结构化克隆的普通对象，修复 `DataCloneError` 导致插件桥接/自适应高度失效；iframe 现按内容高度上报 resize。
- **自定义页脚与贴底布局**：站长后台「外观」新增页脚文字（支持 `{year}` / `{site_name}` / `{site_url}` 占位符）、最多 8 条友链/备案链接；SSR 落地页/详情页与 SPA 共用同一份配置，`Powered by SkinNexus` 版权行固定保留（AGPL 署名，不提供隐藏开关），页脚在内容不足一屏时贴住视口底部。皮肤库列表补上与详情页一致的「首页 › 皮肤库」面包屑。

## 2026-10-31

- **插件 iframe 主题令牌**：`init` 桥接新增 `theme`（bg/panel/border/text/muted/accent/dark），插件页面可直接映射宿主主题；`skin-importer` 用户/管理页改为使用该令牌，修复深色主题下文字不可读。
- **插件资产缓存失效**：iframe 页面/插槽 URL 追加 `?v=<插件版本>`，`/plugin-assets` 改为 `max-age=0, must-revalidate`；升级插件后无需手动清浏览器缓存。
- **任务步进与限速修复**：`SaveStep` 每步结束置 `queued` 并释放租约（取消/续跑不再等 30s 过期），step 支持 `delayMS`；`skin-importer` 批量改为每条皮肤一个 step，避免整页连续请求触发 `net.rate_limited`，错误信息带上游错误码。
- **CI 构建稳定性**：镜像构建改用 GHCR registry 缓存（GHA 缓存仅作回退），CI 内 `GOPROXY` 覆盖为 `proxy.golang.org`，各 job 增加超时；修复 `go mod` 层被缓存驱逐后重新下载、4.3 kB blob 卡住 370s 的问题。
- **任务恢复时钟修复**：`Resume`/`Requeue` 的 `run_after` 改用 Go 时间参数，不再依赖 MySQL `NOW(3)`；此前数据库会话为 +08 时，恢复的任务会被推迟 8 小时，表现为一直 `queued`。
- **前端启动与弱网体验**：SPA 启动改为 setup/site-config/插件 UI/`/me` 并行请求并先显示加载页，已登录刷新不再闪登录卡；`/me` 仅在 401 时视为未登录（超时/5xx 显示重试）；首页/详情页内联首屏加载态；页面按路由拆分 chunk（入口 439→207 KB，gzip 74 KB），Markdown/管理面板按需加载；API 统一 15s 超时；皮肤库卡片改用半尺寸预览并复用 App 的会话，图片 `lazy/async`；服务端新增 gzip 与 hash 资源一年 immutable 缓存。
- **后端逻辑修复**：任务 worker 按插件串行并持有 90s 租约（防同插件两个 job 并发串用 job 身份/重复执行），reclaim 上限 10；取消中的任务不再被 `Fail` 复活；停用/启用正确暂停与恢复排队任务；私有纹理的 `/preview/{tid}` 对匿名/非所有者返回 404（所有者/管理员后续修复放行）；私有导入后再公开上传会真正发布；MySQL 会话统一 UTC（修复 +08 下 session/TTL/任务时间的 8 小时偏移）；KV 配额不计已过期键。
- **主题样式加载顺序修复**：关闭 Vite CSS code splitting，所有页面 CSS 回到 head 单 bundle、主题包始终最后加载；修复异步 AdminPanel CSS 后注入导致「晴空琉璃」管理面板被压回纵向大胶囊的问题。
- **插件 i18n**：宿主侧栏/页面标题支持 manifest `ui.i18n` 的 `nav.<page-id>`；`skin-importer`、`nav-links`、`player-notes`、`texture-provenance` 的 iframe UI 接入 `init.locale`，中英文文案随宿主语言切换。
- **材质元数据与举报**：新增 `textures.detail_md/source/source_url` 与 `texture_reports`（0017）；上传支持名称、来源（原创/二创/转载）、Markdown 描述、原始发布地址与私密选项，**二创/转载必须提供 http(s) 来源链接**；详情页渲染消毒后的描述/来源并提供举报 dialog；管理端新增举报处理页（忽略/已处理/下架），下架复用私有化逻辑并清公共 dedupe；插件账号桥的 `uploadBase64/importBase64` 接受 `{detail,source,sourceUrl,public}`，`skin-importer` 1.0.4/1.0.5 自动标注转载来源并把 UI 接入中英文。
- **插件 KV 配额 opt-in**：`store.kv.maxKeys` 允许可信插件把命名空间扩到 5 万键（默认仍 1000，硬顶 50000）；`skin-importer` 1.0.3 用它解决 20 页以上批量导入触发 `plugin KV quota exceeded` 的问题。
- **预览渲染 v4/v5**：v4 复刻 legacy 的「背景色透明」规则——左上 8×8 像素为同一不透明颜色时，把该颜色在基础 UV 区域之外视为透明，修复无 alpha 的高清皮肤（如 littleskin #594824 的白色 overlay）渲染成白头；v5 修正躯干背面 UV（u36→u32，之前采样到左臂图集，导致所有皮肤背面渲染异常）。`/preview` 的 SSR/API 地址统一带 `?v=5`；详情页 3D 查看器改用 `/texture-render/{hash}`（同一规则转成带 alpha 的副本），原始下载纹理与哈希不变。
- **初始化账号超级管理员**：安装向导创建的账号改为 `permission=2`（超管），新站可直接安装插件；既有站点首次启动若不存在超管，自动把最老的 `permission=1` 管理员提升为超管（幂等，启动日志记录 uid）。
- **公共纹理哈希去重**：`textures.public_dedupe` + 唯一索引，普通上传遇到公共库同 `(type,hash)` 返回 `409 texture.duplicate_public`（带原条目链接）；同用户重复上传幂等返回；私有导入 `skin.textures.importBase64` 不受公共查重限制。新增 0015 迁移与历史回填。
- **受控出站 `net:http`**：插件 manifest `net.http` + `skin.net.get`；仅公网 IP、禁重定向、host/path 白名单、大小/超时/限速；Mojang 与 Blessing 单条导入可用。
- **持久任务 `schedule:jobs`**：新增 0016 `host_jobs` 表、租约 worker、独立 job VM、`skin.jobs.*` 与后台管理 API；支持进度、取消、恢复、重启续跑。
- **`skin-importer` 插件（第 12 个官方插件）**：Mojang/Blessing 单条导入（私有库 + 可选应用）；管理员按点赞/页范围批量收录，管理员 uploader、公共库来源标注、哈希去重、进度/取消/恢复、关页续跑。
- **模块化 UI 入口**：关闭邀请码时注册页不再显示「邀请码（选填）」，后台隐藏「邀请码」导航；Union 关闭时用户侧与后台的 Union 入口隐藏，设置中除启用开关外的 Union 分组一并收起；`/api/v1/site-config` 增加 `union_enabled`，前端按真实能力渲染。
- **CDN 真实 IP 解析**：后台「安全与限流」新增 CDN 开关与可排序头列表（默认 `CF-Connecting-IP → True-Client-IP → Ali-CDN-Real-IP → X-Real-IP → X-Forwarded-For`）；仅对 loopback/RFC1918 反代生效，XFF 仍取最后一跳；限流、注册 IP 计数、访问日志与插件 IP 统一走该解析，保存即生效。

## 2026-10-30

- **SEO 标题长度**：默认标题不足 15 字符时自动追加「｜Minecraft 皮肤站 · 皮肤、披风与角色管理」，一键优化同时生成更完整的 description；后台 SEO 页显示当前字符数并提示 15–30 字符区间（Bing 会将过短标题判为信息不足）。
- **GEO / AI 收录**：所有服务端渲染页新增 WebSite + Organization JSON-LD（名称、描述、logo、站内搜索）、`og:site_name` 与 Twitter Card；新增 `/llms.txt` 公开页面地图与 API 入口，供 AI 答案引擎抓取。
- **README 界面预览**：新增落地页、皮肤库、插件市场、Launcher API 四张生产实例截图（站名与敏感配置已替换），中英 README 同步。
- **零编辑部署**：新增 `docker-compose.quickstart.yml`（必填值全部走 `.env` 插值）；中英 README 首屏给出可复制的 `git clone → .env → docker compose up -d` 命令与 blessing 迁移入口，部署手册同步。

## 2026-10-29

- **插件管理页重排**：拥挤表格改为自适应卡片行——名称/描述、版本/来源/信任/作者、权限折叠（>5 显示 `+N`）、状态/健康/路由/错误、操作右置；900px 以下堆叠，全部走既有 token；无 API 变化。
- **CI 修复与提速**：插件仓库签名工作流改用 `bash scripts/sign-market.sh`（避免检出丢执行位导致 exit 126）；server/web 补 Go modules 与 npm 依赖缓存（`cache-dependency-path`），并加 `concurrency` 取消同 ref 的旧运行；插件仓库同步缓存，签名产物由 `sign.yml` 正常提交。
- **社区准则补齐**：新增行为准则、安全策略、无障碍声明、issue 表单（bug/feature + 联系入口）与 PR 模板；README 文档索引同步。语言切换时同步更新 `<html lang>`（无障碍修正）。

## 2026-10-28

- **v0.4.0 发布 / 通用扩展面**：slot 全接线（新增 `auth.register.after`、`texture.upload.after`、`skinlib.list.toolbar`，删除 3 个未接线 ID）；`ui.forms` 注册/上传插件字段（宿主 `SchemaForm` 渲染、服务端校验、guard/event 传递）；`media:avatar` 头像 302（每插件 host 白名单）；`profile.update.check` / `player.rename.check` guard；`skin.host.info()` 能力内省；本地上传信任分级（verified/unknown-key/unsigned/invalid）+ 摘要绑定的风险确认 + 插件列表来源标识；新增 4 个官方插件（texture-provenance / signup-survey / avatar-cdn / name-policy）；主站默认官方市场；新增 0014 `plugin_meta.trust/signer` 迁移。
- **Launcher API v1**：宿主新增 `bearer` 路由档（Ygg/OIDC token → uid，无 cookie/CSRF）、`internal/account` 共享账号服务（角色/纹理/库，guard/事件/审计/Union 一致）、`skin.account/players/textures/library` 插件桥、`bodyEncoding:"base64"` + 4 MiB 二进制上传与 10s 路由预算；官方 `launcher-api` 插件提供 16 个 `/v1` 端点、OpenAPI 与 `LAUNCHER-API.md` 规范；webapi 角色/纹理/上传改为走共享服务（响应形状不变）。
- **插件市场仓库化**：官方插件源码迁入 `skin-nexus-plugins`（11 个插件，含 launcher-api），PR 投稿 + 无密钥 `skin-plugin lint` + CI 环境密钥签名/续签/Pages 发布；`pack` 统一 LF 换行以保证跨平台可复现；主仓库只保留 `hello-world` / `hello-ui` 两个最小夹具。
- **v0.3.0 发布 / 签名市场首发**：`skin-plugin` CLI（keygen/pack/registry/verify/verify-registry）、`marketfmt` 单一 registry 编码器、签名 bundle/registry、GitHub Pages 分发、首批 3 个示例（config-greeter / nav-links / player-notes）；修复 `SKIN_PLUGIN_STATE_DIR` 未读取、上传上限未生效；生产配置公钥/registry URL 后可在后台安装/升级/回滚。
- **插件工具链文档**：`docs/plugin-publishing.md`（私钥离线保管、打包/签名、Pages 布局、过期与轮换、故障排查）。
- **插件市场 P2 Core**：单源签名 registry（Ed25519）+ SHA-256 + 签名 bundle、staging 解压/zip-slip/压缩比防护、原子替换、升级回滚、卸载保留/purge、依赖/冲突与能力 consent、后台市场页与本地上传；新增 0013 安装事务表；修复 `DeletePluginData` 列名与 `.state` 扫描问题。
- **API 收敛**：项目尚无存量生态，插件只支持 `hookApi: 2`；删除 v1 兼容分支、未使用的 `profiles:write`/`admin:menu` 与死字段，示例/文档同步迁移。
- **插件平台 v2（P0+P1）**：manifest v2（config/store/routes/ui/events/guards）、JSON Schema 配置 + AES-GCM 密钥、`0012` 插件平台迁移（meta/secrets/kv/docs/migrations）、public/session/admin/superadmin 三档路由（身份注入/CSRF/限流/body 上限/访问日志）、命名空间 KV/集合与宿主托管迁移、通用事件目录与 fail-closed guard（含超管破窗）、sandbox iframe UI 页面/固定 slot/`/plugin-assets`/`/ext` 深链、插件健康/路由/日志指标；当时保留 v1 `hookApi: 1` 兼容（已于同日 P2 Core 收敛时移除）；新增 4 个 v2 离线示例与规范重写。
- **加载硬化**：入口脚本加载超时、调用栈上限、慢插件不阻塞 List/Handler、审计 actor 统一（移除重复审计）。

## 2026-10-27

- **插件宿主 v1（goja）**：`internal/plugin` 从冻结契约落地为完整运行时——manifest 校验（含可选 title/author/homepage 元数据）、单插件 VM + 超时/panic fail-open、6 hook 挂载（profile/texture 过滤严格先于签名）、`/api/plugins/<name>/` 只读动态路由、options 持久化启停状态、超管会话专属启停/重载 + 审计、管理面板「插件」页；`examples/plugins/` 两个离线示例与 `docs/plugin-spec.md`（含 blessing 对照与 v1.1/v1.2/v2 路线）。
- **v0.2.0 发布**：相对 v0.1.0 的增量——A1 自助找回（SMTP 全链路）、A2 Mojang 官方兜底、C1 登录/通信日志观测面、SSR SEO 内容页、v3 视觉体系与上传 3D 预览、主题规范 v4、HTTP 超时与优雅停机；新增迁移 0010/0011（`SKIN_AUTO_MIGRATE` 启动自动应用，升级前先备份）。
- **前端逻辑修复（批 A–C）**：skinlib URL 语义统一（view↔URL 双向同步，`/user/skinlib` 仅存启动器深链兼容）、landing 登录态 CTA、未知路径真 404 + SPA 壳 noindex、sitemap 归位、错误可见性整改（UnionView / SettingsView / DashboardPage）、访客登录引导与全站 401 统一、写操作成功反馈、pendingView 生命周期修复。
- **admin 视图 zh-ui lint 存量清零**：31 条旧债（4px 网格 / token 化 / 去 fallback / emoji 图标）；仅存诊断导出模板 6 条既定例外。
- **晴空琉璃主题迭代**：§22 气派升级（双层阴影 / hover 微交互 / 渐变发丝线 / topbar 玻璃）、同型 bug 批量清账（未声明属性泄漏 / 特异性打平）、批注三问题修复；主题版本 3.0.11（上传脚本自动递增）。
- **晴空琉璃 v3.2「质感整合」**：整份样式单文件重构（删三个 `.card` 等重复层、token 全量双态），修复 `--az-grad-num` 未定义导致统计数字透明不可见；落地页精选固定 4×2、hero 全出血下缘接缝消除；概览统计卡改内嵌薄片（不再卡中卡）；管理面板侧栏改顶部 dock（token 兜底行右置）；详情页预览区加中心聚光；窄屏衣柜/材质表改卡内横滑（日期与操作按钮不再被压成一字一行）、dock 右缘渐隐提示横滑；`theme.js` theme-color 随新色板更新。
- **主题上传脚本加固**（事故驱动）：PS5.1 按系统代码页解码无 BOM 的 UTF-8 脚本，中文注释吞行后版本号解析出错、`ConvertFrom-Json` 往返把 `theme.json` 写成 BOM-only 并上传；脚本改为强制 PS7、正则改版本号、写入无 BOM、分小包传输、容器内 sha256 断言。已按会话记录恢复并重传，三文件哈希逐字节一致（3.2.1）。
- **开源仓库整理**：删除 M0 插件运行时 PoC（goja 选型已固化、宿主运行时后置）与历史蓝图草稿 `docs/blueprint.md`、重复协作规范 `docs/standards.md`（有效约束并入 AGENTS/CONTRIBUTING）；CI 收敛为 4 个 job；中英 README 重写为「是什么 → 优势 → 核心功能 → 三步部署」；清除公开文档中指向私有工作区的路径。
- **HTTP 服务加固**：公开监听从零超时 `http.ListenAndServe` 改为带 ReadHeader/Read/Write/Idle 超时与 `MaxHeaderBytes` 的 `http.Server`，并处理 SIGTERM/SIGINT 优雅停机（8s 排水，落在 Docker 默认 10s stop 窗口内）；新增超时配置回归测试。
- **隐私修复**：迁移集成测试 fixture 中的学号样例改假值（全仓隐私扫描归零）。
- **全量文档审计（三路只读子代理 + 主会话复核）**：修 25+ 处——`/setup` 白名单回归补修（代码）、migration.md 指纹比对命令修正（PEM 解码）、纹理 BAD-IHDR 拷贝口径、dry-run/selected 对账口径、管理员/setup_done 机制描述、PKCS#8 提示、迁移范围 0001…0011；deploy/build 补 SMTP 五键与 MOJANG_FALLBACK 变量、CI 双矩阵描述、config.go 全量性口径；compat-matrix 找回密码矛盾归正并拆条、theme-spec 落地页 token 矛盾、blueprint 悬空待办归档、GOVERNANCE 协议误引、双 README 巨长段压缩与索引补齐；CHANGELOG 补性能实测记录。

## 2026-10-26

- **晴空琉璃「差异最大化」样板主题 v3**：`theme-spec.md` 主题包能力边界规范终版（四条红线 / 共存契约 / 全盒模型显式重写）、dock 单行化、缓存版本号自动递增（事故根治）、明暗形态统一。
- **v3 全站视觉革新「月下仙山」**：token 架构、顶栏与分栏场景认证、图标导航、统一动效体系、三主题适配。
- 产品功能：上传纹理卡拖放 + 选肤即 3D 预览、Union 黑名单翻页与「有效期至」列、公告 markdown-it 渲染、皮肤详情页与 404 页统一入主题。
- **A1 找回密码**（SMTP 全链路：防枚举 / 单次令牌 / 面板热配置）、**A2 Mojang 官方兜底**（出站防护 + 热开关）、**C1 运维观测面**（登录与通信日志，90 天保留）。
- 运营面：竞品对照矩阵上线（三家源码实证）与 blessing 勘误、注册 / 找回表单按站点配置收敛、站点设置分组化。

## 2026-10-25

- **正式切流完成**（67 用户 / 61 纹理零丢行、UUID 零漂移、签名钥连续性实测）。
- **性能实测**（2 核 / 3.5GB 生产 VPS）：落地页 ~600 RPS（CPU 封顶）、纹理分发 5182 RPS、Yggdrasil 元数据 2365 RPS、全程零错误。
- 站点 Logo（SVG 标识 + favicon/PWA 全家桶）、皮肤详情页 SEO 内容页、访客首页 + 逐页 SEO meta。
- 高清皮肤原生分辨率渲染 v3、预览 URL 版本化、皮肤库预览改服务端 PNG（规避 WebGL 上限）。
- UI 自定义体系修复 + 贡献指南、三路文档静态审计修复、英文 README、**v0.1.0 首发**。
- 文档规整 + Docker 部署文档主线化、管理面板「自助诊断」1:1 对标。

## 2026-10-24

- **全量迁移闭环**：7 表逐一对账、纹理 61 文件字节级一致。
- 管理面板对齐 blessing **B1–B5 全批**；Union 配置 options 化 + 热切换；管理面板账号会话化。
- 用户中心（仪表盘 / 衣柜 / 角色 / 资料 / 删号）+ 皮肤库全栈（可见性三档 / 筛选 / 排序 / 点赞=入柜）。
- 服务端头像与预览渲染器（`internal/avatargen`）、公告 Markdown 双层消毒、公开迁移手册 `docs/migration.md` 上线。

## 2026-10-23

- 二期收官：免密登录（yggc / OIDC 四种 grant）+ MUA Union 可开关模块 + 前端管理与集成。
- 兼容矩阵 36 端点全量实测对齐、`migrate import`（ID 1:1、`$2y$` 直迁直验）。
- 三期骨架：i18n 双语言包 / SEO 注入 / 主题包（`docs/theme-spec.md`）/ Docker 双 DB 模式 / `/setup` 安装向导。
- 部署管线 CI→GHCR（部署机只拉不编）；邀请码模块；数据兼容铁律入 AGENTS 铁律 7。

## 2026-10-22

- **立项与一期 M0–M5**：三仓库（blessing-skin-server / yggdrasil-connect / authlib-injector）源码级分析、技术栈冻结 **Go + Vue 3**、插件 v1 = goja。
- Yggdrasil 全端点契约测试、密码 12 算式移植、纹理管线、migrate 引擎与 CLI、Vue 前端与管理面板、公开 API、CI 与文档。
- 建仓 `YimingZhanshen/skin-nexus`（AGPL-3.0，署名 YMZS）。
