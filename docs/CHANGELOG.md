# 变更日志

> 迭代历史（按日期倒序，每天一条，只记要点）。开发细节见对应 commit；README 状态行只留一句话摘要。

## 2026-11-12

落地页两处修订，都来自站长在演示站（晴空琉璃主题）上的当场标注。**生产站零操作**，
验收在本地 SQLite rig；不发版（tag 仍 v0.5.0）。

- **精选材质标题不再压到邻卡**。根因不在主题包：宿主给标题定的策略是单行截断
  （`white-space: nowrap` + `text-overflow: ellipsis`），而 ellipsis 只在元素自身宽度受限时
  才生效——主题把卡片改成居中 flex 列后，nowrap 标题的 min-content 就是整行宽度，于是
  它按文字撑开。真浏览器量到卡片 222px、标题 379px，左右各盖住邻卡 78px（窄轨道下 110px）。
  修法是一条宿主声明 `max-width: 100%`，读数回到标题 194px / 卡片 222px、重叠卡数 0；
  截断后全名交给 `figcaption title`。**顺带否证一条**：`figure { min-width: 0 }` 实测与不改
  完全一样（轨道是固定 `minmax(150px, …)` 最小值，没人把它撑开），所以没有进这次提交。
  修复全在宿主，主题包无需重新签名上架。
- **首页撤下「关于本站的事实」**。那五问五答（启动器协议、数据库引擎、老站迁移、验签安装、
  匿名可读）是软件说明书，玩家来挑皮肤、服主来找注册按钮时都不会读，却横在简介与皮肤库入口
  之间。内容没有丢：`docs/compat-matrix.md`、`docs/deploy.md`、`docs/migration.md`、
  `docs/theme-spec.md` 才是它们的真源。连带删掉双语目录里 22 个键与该块的专用测试，
  新增一条范围守卫：说明书不再回到首页，半删状态也不会把裸 catalog 键漏进可被抓取的页面。
- 没跑的检查：整页截图观感（内置浏览器当时无可见 surface）；演示站要拉到本轮镜像才看得到
  这两处变化；CSP 放行项用本地第二个端口当「第三方源」验的，真实 googletagmanager.com /
  clarity.ms 的联网投递本机未验（拦与放的行为与源无关）。

- 面板「自定义 JS」粘贴的第三方统计脚本现在能声明放行。站长把 Google Tag Manager 与
  Microsoft Clarity 合进自定义 JS 后没反应：本站 CSP 的 `script-src` 只有 `'self' 'unsafe-inline'`，
  第三方 `<script>` 在浏览器里就被拦下（本地 rig 复现出与他控制台一字相同的违规）。新增两个
  **默认留空**的部署变量 `SKIN_CSP_SCRIPT_EXTRA_SOURCES` / `SKIN_CSP_CONNECT_EXTRA_SOURCES`，
  分别追加 `script-src` 与 `connect-src`；不设时响应头逐字不变。**只放脚本源不够**：Clarity
  用 fetch 上报，`connect-src` 不放就是「脚本加载了但一条数据都没收到」。值在启动时严格校验，
  只接受 `https://host`、`http://host`、`host`、`https://*.host`（可带端口）——分号正是 CSP 的
  分隔符，一个不是「源」的值等于替我们往响应头里写别的指令，所以这类值直接启动失败而不是
  静默忽略；校验也刻意排在 `listening` 那行之前，先宣布在听再退出的日志是凌晨三点最难读的一种。
  真机三档读数：不设变量时探针「NOT RUN」加一条 CSP 违规；设了之后探针 `ran` 且控制台干净；
  填非法值时进程没起来，日志最后一行就是那条配置错误。
- 过程教训：`deploy.md` 的变量表与代码里的 env 清单是双向门禁，新变量必须同时进
  `config.KnownEnv`，否则启动时会对一个其实生效的变量报「未知」——这条闸当场拦住了我。

- 部署文档改成「最少 env」口径（站长要求）。`docs/deploy.md` §2.3 的 11 张变量表统一加了一列
  **必填性**，分四档：必填 / 分模式必填 / 面板真源 / 可选，54 行逐行标注。分类不是编辑判断：
  「面板真源」来自 `admin.SettingsDefaults`（option-first、env 只兜底），「必填」来自 quickstart
  compose 的 `:?` 守卫。§2.3 开头新增一段结论——新站最少四个值就能起，其余全有默认；
  **已经跑起来的站要改设置去面板改，别改 `.env`**，因为面板保存的那份才是真源。
  一处刻意的不一致被写明而不是抹平：`SKIN_ADMIN_TOKEN` 标「必填」是 quickstart 的守卫要求的，
  程序本身允许留空（= 机器 token 通道关闭），主 `docker-compose.yml` 里它只是 `CHANGE_ME` 占位。
- 过程教训：`check-docs` 会拦列数不齐的表，而这 11 张表的表头有三种形态（两列 / 三列 /
  「默认（源码 / 镜像内）」），批量插列必须按各表自身宽度处理，否则会把说明里带转义竖线的行
  数错列。

- 修掉上一片我自己漏的一个洞：CSP 放行变量在 compose 里没透传。站长照文档在 `.env` 里加了
  两行、也拉了新镜像，响应头却一字没变——取证后确认不是缓存（首页事实块已消失，说明新代码
  在线上），真因是 compose 的 `.env` 只做变量插值，app 进程看得见什么取决于 `environment:`
  段列了什么，而我加那两个变量时只改了代码与变量表，没同步两份 compose 示例。现在 quickstart
  与主 compose 都带上这两键，`deploy.md` §2.3 补了一段「`.env` 写了键容器不一定看得见」，
  并把这句写进两个变量自己的说明里。教训：**给部署侧加一个 env，代码、compose 示例、变量表
  三处必须同批**，少一处就是让站长白跑一轮。

- 验证门终于付得起它自己那笔钱：开启 `require_verification` 时注册必须填邮箱。
  站长的原话是「难道不是强制填邮箱、注册后跳验证页吗」——之前不是：这道门只装在
  **进服**那一步（`ygg` 的 `verificationBlocks`），而注册接口对邮箱的规则是「非空才校验格式」，
  于是玩家可以不填邮箱注册、逛完整个站，最后在服务器门口被拒，且注册页一个字没提醒；
  更糟的是没有地址就永远发不出验证链接，门对他来说是死的。现在服务端在写任何数据之前
  用新码 `auth.email_required` 拒绝，注册表单同步把邮箱标成必填、给 fail-fast 提示，
  并在门开着时**即使 SMTP 还没配也显示这个字段**（原来它整个包在 `mail_configured` 里，
  门开着反而没有地方填）。文案随状态变：门关「邮箱（可留空）」，门开「邮箱（必填）」。
  `deploy.md` 里那句「开启后新注册账号会自动收到验证邮件」是说满的，已改成写明
  「只拦进服、注册强制邮箱、发信配置是这道门的前提」。
  注册成功后也不再把人默默丢回仪表盘：门开着时落在「邮箱验证」这一屏，说清链接会送到哪个地址、
  邮件可能进垃圾箱、没收到可以在这里重发。SMTP 未配置时同一屏换成明话「验证邮件发不出去，
  请联系站长」并收起重发按钮（发了也是失败）。措辞刻意不写「已发送」——发信是 detached 的，
  服务端返回 201 那一刻并不能知道邮件有没有出去，重发失败时页面就地显示服务端那句
  「验证邮件发送失败，请稍后重试」。这一屏只在这道门开着时出现：门关时邮箱字段照旧不显示、
  注册照旧直接进仪表盘；OAuth `?next=` 与设备码流程仍在抢这一屏时优先回自己的落地页。
  三态都取了真机读数（本地 rig：门开+SMTP 形状齐 → 已创建并指向地址；门开+SMTP 空 → 明说发不出去
  且无重发按钮；门关 → 无字段、无引导屏）。
  测试钉四件事：门开+空邮箱 400 且**不建账号**；门关+空邮箱仍 201（默认行为一字不变）；
  格式错仍回原码；门开+有邮箱则地址真落库且 `email_verified=0`。变异取证：把开关写死成
  always-on，第二条立刻红——说明「门开着」与「门关着」两侧都被钉住，不是单侧断言。
  注册后的发信本身是 detached 的，测试里 `t.Cleanup` 关库比 goroutine 更快，因此**不**断言
  令牌计数（那是竞态不是测试），发信链路由 `mail_detach_test.go` 覆盖。

## 2026-11-11

邮件改进轮：起因是演示站两次真实故障（主题装不上 = 旧镜像漏认证包装，已在此前修复；
测试发信失败 = 收件人是向导塞的占位地址）与站长三个问题（信能否跟主题、发信昵称、
还有啥可优化）。五片落地，**生产站零操作**，验收在本地 SQLite rig + 假 SMTP + 真浏览器；
不发版（tag 仍 v0.5.0）。

- **验证链接改点击消费**。邮件网关与微信/QQ 内置浏览器会预取链接，「进页面即兑换」
  把一次性令牌烧在玩家点击之前。落地只读 URL 展示确认按钮，点击才 POST；Go 源码守卫
  钉住两个路由入口不自动消费、按钮已接线（web 无测试框架，沿用读 .vue 的既有纪律）。
- **占位邮箱止血**。安装向导留空邮箱曾静默写 `admin@localhost`：一个永远收不到信的
  地址，却让面板以为站长有邮箱（企业邮在 RCPT 回 501）。留空改 NULL，非空必过注册同一条
  形状规则；面板测试信与验证请求在铸令牌/拨号之前回 `mail.recipient_placeholder`。
  存量占位行不做迁移——改存量邮箱会清验证态并烧令牌，代价由站长自决。
- **三封信双语 + HTML 副本**。文案进 webi18n catalog 的 `mail` 命名空间（locale 闸显式
  扩面，非放宽）；multipart/alternative 纯文本在前 HTML 在后，各 part 自带 base64；
  补 `Message-ID` 与 `Auto-Submitted`；From 显示名缺省站点名（RFC2047），手填优先。
  主题只烤 accent 进色带与按钮：`TokenMap` 只取无条件 `:root`、别名两跳、丢弃 alpha 与
  现代颜色语法；正文恒定浅底深字——邮件客户端剥样式且暗色反转不可控，跟暗色真源等于
  白底白字。已发出的信是快照，换主题不改旧信。测试信带不含凭据的配置摘要与证明边界。
- **部署自检加发件域 SPF/DMARC**（封闭集六项→七项）。每次查询 3s 上限；缺记录报 check
  并给补法，查询失败也报 check 但明说「不代表记录不存在」（读不到压过没有），永不报
  broken。DKIM 不代查：selector 猜不到等于编结论。`diagFixKeys` 与双语词典改为双向机器核。
- **顺带修一条静默缺陷**：`audit_log.created_at` 一直吃列 DEFAULT（数据库钟），而窗口
  读取比对 store 钟，主机与库时钟不一致时 24h 计数读错窗口。InsertAudit 改绑 store 钟。
  玩家侧发信失败此前只进日志，现在落审计（掩码邮箱）并进运行状态「近 24h 发信失败」；
  计数失败降级 0 绝不 500。忘记密码响应形状逐字不变（存在性 oracle 红线）。
- **瞬断重试一次**：灰名单/拨号瞬断/连接被重置在无人等待答复时重试一次，共享同一条绝对
  deadline；5xx、凭据与 TLS 错误不重试（重试凭据错误可能锁号）；**DATA 被接受后绝不重试**
  （否则投两遍），编排层与分类层双重守卫，变异证明去掉任一层即红。
- **自检的 DNS 改问公共 resolver**（站长实测抓出）：SPF 加好后公网 DNS 早已生效，但面板
  仍报「没有 SPF」——容器走的阿里云 VPC 内网 DNS（100.100.2.x）缓存陈旧，TTL 被钳在 10s
  内容却不刷新。自检问的是「收件方看到什么」，不该信云内网 resolver：mail_dns 一项改为
  直问 223.5.5.5（1.1.1.1 兜底），每次尝试独立 1.5s 上限；系统 resolver 不再参与这一项。
  新增本地 UDP DNS 假服务器做真线路测试（含首台静默时的兜底），变异证明改回系统 dial 即红。
- 过程教训：PG 集成腿红在测试自己写的裸 SQL 用 `?` 占位符（应走 dbx 重绑定）；面板真机
  读数先抓到一次「裸键」，真因是 web/dist 未随 locale 重建——浏览器验收必须用当轮构建。

## 2026-11-10

联盟自助诊断轮：站长反馈「弹窗位置不太对，诊断项和展示方式可以在老插件基础上升级」。
两条都落地，位置那条查到了根因而不是改了个数值。**生产站与演示站零操作**，验收在本地
SQLite rig + 假 hub；不发版（tag 仍 v0.5.0）。

- **弹窗错位的根因是包含块被抢走，不是没写 `inset`**。`web/src/style.css` 给
  `.user-main > *` 上 `animation: view-rise … both`，命中的正是管理面板的根节点；
  动画跑完后 Chrome 报的 computed transform 是 `matrix(1,0,0,1,0,0)` 而不是关键字
  `none`，非 `none` 就构成 `position: fixed` 后代的包含块。真机量到 `.admin-shell`
  此刻 transform 非 none，遮罩因此按**面板内容高**（1800px）而不是视口（1145px）
  铺满，弹窗落在折叠线下约 400px，且滚动时整块跟着文档走。改法不是继续找哪个属性
  闯的祸，而是搬到抢不走的地方：改用本仓既有的原生 `<dialog>` + `showModal()`
  约定（top-layer），修完 dialog 的 `centerOffset` 实测 `dx=0, dy=0`，同时白拿焦点
  归位、Esc 与可访问名——这些原来一概没有。`max-height` 一并从 `vh` 换成 `dvh`。
- **报告内容的语义本来就是错的**（这条比位置值钱）。hub 诊断载荷里的
  `status_code / headers / body / tls_handshake` 描述的是 **hub 回探本站**那一跳，
  而第一跳失败时它们描述的是 hub 对我们这一跳的答复；旧代码不分情况平铺读，于是
  生产上出现「双向连通正常」下面挂着 `状态码 200` 和一段 nginx 响应头。老插件
  `config.twig:135-150` 是同一个 bug 的源头，所以这是超越而非我们的回归。现在按腿
  分家，每个键只在一个分支里读；第一跳失败时第二跳写「未测」，不编结论。
- **本站那一跳由本站自己观测**（老插件没有的能力）：我们和 hub 实际谈成的 TLS 版本
  与套件、对端证书到期日与剩余天数（<14 天注意、过期失败）、以及 hub `Date` 头与
  本地钟的偏差——判据直接复用入站验签的 `VerifyPastSkew`/`VerifyFutureSkew` 两个
  常量，所以面板不可能再说「时钟同步」而隔壁正在拒收 hub 回调。hub 粘回来的 GnuTLS
  轨迹摘要成「版本 + 最后保留的套件 + 断言行数」，全文折进「原始内容」。
- **失败原因给稳定 token 而不是散文**：`http_key`（成员钥匙被拒，去面板改）与
  `dns`/`refused`（去查网络）修法相反，老插件把两者都折成一句 exception。三态徽章
  （通过/注意/失败）取代老插件的成功/失败两态——证书快到期被显示成绿色是错的。
- **顺带查出并修好一条静默缺陷**：`Diagnose` 外层包 10s、内层 `doJSONInner` 又包
  默认 5s，嵌套 deadline 取小的那个，所以诊断一直 5s 就断，注释与老插件的 10s 都没
  兑现。超时改成 `roundTrip` 的参数、deadline 只设一次；`roundTrip` 由此成为 hub
  报文形状的唯一建造点。测试用 200ms 普通预算配 600ms 才答的假 hub，普通调用必须
  超时、诊断必须成功，两者互为对照。
- **闸自己回敬了两次**：新增 `scripts/check-modal-chrome.mjs` 的第一版对变异毫无
  反应——它的说明文字里同时含 `position: fixed` 与 `showModal()`，所以检查打在散文上
  而不是代码上；剥注释必须做在入口。`check-locales` 新增规则 1b 时也先红后绿：加上时
  精确命中 `admin.themeUploadDone` 那两条裸 `@`（vue-i18n 把它当链接消息起始，
  `baseCompile` 抛 Invalid linked format，症状是整块面板空白），且不误伤早已用
  `{'@'}` 转义的那条。机制是实测的：`parse()` 放行、`baseCompile()` 才抛。
- **模型抽成纯函数用 `node --test` 断言**（12 条，零新依赖）：`web/` 没有组件测试
  框架，但决定字段归属的是纯函数，不需要框架也能测。变异：把 `status_code` 放回腿一
  → 报「它描述的是 hub 回探我们」；把未测的腿二说成健康 → 报 `ok` 不等于 `skip`。
- **真机读到的两处当场修掉**：`close` 事件在真实 Chrome 里根本没派发到监听器
  （`dialog.open` 立刻 false，等 1.2s 也没有事件），于是 `diagOpen` 停在 true、
  已关闭的节点长期留在 DOM；改成状态自己置回 + Esc 走 `cancel`。修完 × 与遮罩点击
  后节点都不在 DOM 里。
- **not-run（不写成「没问题」）**：生产 hub 的真实 diagnose 载荷与真实证书链
  （生产零操作）；375px 窄屏渲染（隐藏浏览器无法改视口，媒体查询只经代码核查）；
  Esc 键第二次按键未落到页面上，该路径按「部分验证 + 表面不稳」记账；
  `-race`（本机无 cgo）；导出文件的落盘内容（Blob 下载在隐藏页不可观测）。

## 2026-11-09

收口 + 优化轮：三条挂了很久的验收账全部拿到真机读数，扫描出的真缺陷逐片修掉，
门禁自己的盲区补上，文档与实测对齐。S1–S23 逐片落地、逐片 commit + push，全部本地
SQLite rig 验收，**生产站与演示站零操作**；不发版（tag 仍 v0.5.0）。

- **三条挂账结清**。① **D9（面板任务取消/恢复）**：examples 里原先只有两个 hello 夹具，
  都不声明 jobs，本地根本造不出一个 running 任务——新增 `examples/plugins/jobs-fixture`
  （不联网，在 job VM 里烧时间步）。同时发现面板的「恢复」按钮谓词比后端窄：
  `jobs/store.go` 的 Resume 收 `cancelled`/`cancelling`，按钮却只认 `paused`/`failed`，
  等于**已取消的任务只能看着、不能重启**。现在真机走完 running→取消→恢复→done 一整圈。
  ② **英文管理面板那一屏**：设置面板约 40 条说明是写死的中文串，前端直接
  `{{ s.description }}` 渲染，英文界面整屏中文；改成后端只发键、前端 `t()` 渲染。
  ③ **明细 `upstream_code`**：本地起一个只回 500 的假 hub，把 `union_api_root` 指过去再开
  诊断窗——读到 `hub.500`（4 ms）；关掉假 hub 重放，读到 `transport`（0 ms）。
  这两条在面板展开行里逐字读到，列名与 `{n} ms` 都是英文。
- **插件在长步里第一次能自己知道被取消了**。宿主打断不了已经发起的 VM 调用，取消只在步
  边界生效，而步上限 30 秒；payload 里那个 `cancelRequested` 是步开始前的快照、恒为
  `false`，全仓除了这一行只有文档提过它。**没改那个字段**（它在 hook 合约里），而是加了一条
  只读 `skin.jobs.cancelRequested(jobId)`，读 store 的 cancelling/cancelled 两态。
  真机读数：8 秒预算的步里，取消落下后 **2701 ms** 插件就自行收尾（日志原文见
  `docs/plugin-spec.md` §10.5 与 S19 提交信息）。
- **两处「谎报完成」**：OIDC 吊销客户端刷新令牌时错误被 `_ =` 吃掉，照样回
  `{"ok":true}`——而同文件的 All 分支早就修过同类问题；`join session` 那张表既没人建索引
  也没人删行，`hasJoined` 每次全表扫（三树 0030 补 uuid 索引 + 挂进既有清扫器）。
- **入站文本校验缺口**：站点命名规则只管上传不管改名，于是**改名可以绕过规则**；衣柜别名
  既不封顶也不去隐形字符。改名复用上传的同一条校验，别名加长度帽并复用 `ugctext.Visible`
  ——没有新造校验器。
- **半套守卫逐条补全**：SMTP 卡住时找回密码/邮箱验证两个请求能挂 40 秒（注册路径早就
  detached 了，另两处没有）；三处时间戳交给列 DEFAULT、读取端却按应用钟算窗口（统一到
  注入钟）；SPA 只写不读 `skin_lang` cookie，把在 SSR 页选的英文抹回中文（读序改成
  cookie 优先，因为两边都写 cookie，它才是最后一次选择的记录）。
- **门禁自己的三个盲区**：`check-locales` 的 SSR 扫描面漏了 `internal/seo`、未知前缀静默
  跳过、拼接键完全不查；UI 键只有上限没有地板（清掉 22 个没人引用的键之后加了地板）；
  `errorgate` 没吃到 `internal/oidc`，一接上就当场抓出设备码同意页的四条裸文案。
  本轮末尾又补第九条：**访问日志的 kind 词表**——插件路由的 kind 是在调用点写死的字符串，
  绕过了词表，于是英文面板把键路径 `admin.kind.plugin` 原样打在表格里（21 行里 8 行）。
  现在 kind 必须登记成常量、必须有中英标签、必须出现在筛选下拉里，缺一红。
- **双语从「句子」扩展到「机器名」**：审计动作 53+ 个 snake_case 名进词典（`audit.action.*`），
  市场包名按当前 locale 取而不是固定先 `zh-CN`；SEO 站点级文案改为调用方注入，英文页不再
  回吐中文句子。
- **可访问性补齐**：107 个 `th` 全部带 `scope`、数据表补隐藏 `caption`（骨架占位表在
  `aria-hidden` 容器里不参与朗读）；两处漏迁的对比度令牌回到 `--danger-ink`（危险按钮
  3.90:1 → 4.57:1，强调徽章 2.94:1 → 6.09:1，都按 WCAG 相对亮度手算复核过）；市场页五处补 `aria-live`。
  OIDC 建 client 连点两次会**永久丢掉明文 secret**（第二次提交把它抹空），连同
  togglePublic / 发信 / 邮箱验证四处一起补 in-flight 守卫。
- **死代码六处**：`DetailHeaderWhitelist`、`oidc.SetSessionCookieName`、
  `oidc.MarshalDeviceState`、`POST /api/admin/v1/market/plan` + handler +
  `Manager.MarketPlan`、`GET /api/v1/stats` + handler。每条都重新 grep 全仓（含 .vue 与
  生成物目录）确认零引用；`/me` 那个弱哈希数字实际由 `/api/admin/v1/overview` 提供，
  真机验过两边行为（旧端点 404、overview 仍回数）。
- **本轮 CI 红过 8 个提交，两条原因都是我自己造的**（记账，不粉饰）：
  ① S1 把 `internal/account` 的集成腿加进三条真库腿，却没有串行化——account 腿和 store 腿
  共用同一个测试库，两边都在迁移前抢着建/清 `schema_migrations`，SQLite 报
  `UNIQUE constraint failed: schema_migrations.file`、Postgres 报类型目录唯一键冲突；
  修在 `3188605`（真库腿 `-p 1`，理由写进 workflow 注释）。② S2 带进 gofmt 债
  （`internal/i18n/codes.go`、`internal/oidc/userapi_test.go`），`server` job 一路红在
  `test -z "$(gofmt -l .)"`，到 S10 才被顺带清掉。两条从 S9 之后由 CI 全程绿覆盖。
  流程教训：手工闸清单里必须显式列出 `gofmt -l` 与 `go vet -tags integration`，
  并且**每推一条就回查那一条 run**，不要攒到收尾。
- **性能只量不改（S23）**：落地页每请求三个 COUNT 加一次 likes 榜，榜那条 SQL 的计划里挂着
  `USE TEMP B-TREE FOR ORDER BY` 和一条对全部公开材质的相关子查询——**成本随整站公开库大小
  线性增长且无缓存**（1 条 1.09 ms → 3001 条 4.08 ms → 6001 条 7.37 ms）；
  `GET /api/v1/me` 逐条 `Path()`+`statFile`，1 条 1.14 ms → 3001 条 128.7 ms →
  6001 条 255.5 ms。要修 `/me` 必须先给 textures 加 size 列（迁移 + 回填），本轮没做。
  数字与推算都写进仓外待办，等拍板。
- **文档与实测对齐六处**：README 中英与竞品表的覆盖率地板还写着 40.0（实测 41.8），实际
  地板 45.0、本轮同一条命令量到 52.5；`docs/build.md` 说「前端为纯静态 SPA」，与真实存在的
  三个 SSR 面矛盾；CONTRIBUTING 加语言的步骤只写了 SPA 一侧，照它做完只会得到「一半界面是
  新语言」，且指向的 `resolveLocale` 早就不存在（实际白名单是同文件的 `asLocale`）；
  ACCESSIBILITY 把「界面文案全部键值化」从口头承诺改成可查证表述，并写清对比度只量过默认主题；
  竞品表的集成矩阵从「MySQL 两条腿」纠正为六条腿。

### 同日第二轮 · 视觉去生成感（默认主题 + 官方主题包）

按界面打磨清单把两个主题重过一遍：默认主题（月下仙山）与官方包「晴空琉璃」。
拍板口径是**保琉璃之名，收玻璃之实**——琉璃由三处天空玻璃承担，不再满屏拟态。
全部本地 SQLite rig 与静态产物取证，**生产站与演示站零操作**。

- **先补闸，再动样式**。`scripts/check-theme-contract.mjs` 两条窄闸：① `docs/theme-spec.md` §3
  那七个契约键的默认值与 `theme-base.css` 逐键比对；② 主题包里 `var(--x)` 引用的宿主键若在真源
  已不存在则报错（改名等于让已上架的包静默变死变量，同版本制品不可重签）。闸落地即红——
  §3 的七处默认值**全部**是从上一代色板抄的（`--bg:#0f1115` 对真源 `#101825` 等），逐键纠偏后转绿。
  两条变异各自复现：把 §3 的 `--accent` 改回旧值 → rc=1；给包里塞一行 `var(--nope-token)` → rc=1。
  接入 CI `docs` job；`build.md` 的闸表同步。
- **令牌层**。`--glow-cinnabar/--glow-danger` 从 .6 透明度的朱砂/红石彩晕改成中性墨色，
  `--shadow` 从 12px 外阴影改成 `inset` 顶边高光（暗底 #101825 上外阴影只会糊成脏），
  `--shadow-lift` 只留给真正浮在内容之上的层；圆角 `--r-s/m/l` 4/6/10 → 6/10/14（41 处消费点随之统一）。
  **新增 `--mono`**：六个管理面板一直在写 `font-family: var(--mono, …)` 各带兜底，而这个键在真源
  从来不存在——没有任何报错，只是每屏 id/哈希/时间戳的等宽字面长得不一样。补键属加法，不打断任何包。
  不变量进了 Go 测试：层次令牌不得含品牌色 rgb 三元组、`--mono` 必须存在且以通用族收尾。
- **SPA 形态层**。越格值归 4 阶（卡片下边 18→16、页脚 18→16 与 gap 6/18→8/16、顶栏标 26→24、
  Tab 内边距 14→16、未读徽章 18/9/5→20/10/6）；`.tab` 的 999px 退成 `--r-m`；按钮 hover 去掉
  `translateY(-1px)` 与彩晕（`button:active` 只剩一条空复位，整条删）；场景雾的 70s/110s
  `fogdrift` 循环删除，两层静态雾留着——纵深还在，永动背景走掉。SPA 里剩下的 `translateY` 只有
  `view-rise` 与 `toast-in` 两处一次性入场。
- **SSR 四处 + 一个真缺陷**。详情页 404 自带一份 `:root`（`--ink:#16202e` 等三条加一份手抄字体栈），
  正是 `theme-base.css` 头一句禁止的第二真源，且早已漂移——改为注入同一令牌块并消费
  `var(--bg)/var(--text)/var(--font-sans)`，红测 `TestTexNotFoundCarriesThemeBaseOnce` 修前 FAIL
  （令牌块计数 0 + 四处手抄命中）、修后 PASS。落地页雾色改 `var(--fog)`、远山三层与雾底渐变改由
  class 消费 `--mtn-far/mid/near` 与 `--panel`（`var()` 在 `fill=` 呈现属性里不被解析，故用 class）、
  `fogdrift` 同批删；越格值归格（cta 11→12、more 9→8、stats 22→24 与 30→`--fs-2xl`、divider 边距、
  grid figure 14→16、z-index 90/50→`--z-modal`/`--z-topbar` 等）。**品牌 logo 一字节未动**（站长
  拍板归品牌，测试亦断言 `brandmark`/`p2m-stage`/`p2m-wipe`）。
  皮肤库爬虫面取证确认它外链编译好的 `style-*.css`（bundle 内 `--r-l:14px`/`--fs-xl:24px` 实测在），
  于是灰边 127,127,127 → `var(--line)`、圆角归刻度，并把 `.ssr-meta/.ssr-w` 的 `opacity .72/.66`
  换成 `color: var(--muted)`——靠透明度压小字会把对比度一起压下去。
- **诊断报告导出**。`UnionView.vue` 的独立 HTML 用 Bootstrap 的 `#198754`/`#dc3545`（白纸 4.53:1，
  对比度不违规，问题是别人的色板出现在我们的产物里），换成本包色相压深的 `#2f7d4f`（5.04:1）/
  `#a12b20`（7.29:1）。没有直接取当前主题的 `var(--ok)`：导出文件是白纸文档，暗色主题的 `#6fbf7d`
  在白底只有 2.23:1。
- **晴空琉璃 3.2.1 → 3.3.0**（分发仓库 PR #1 合并，`Lint changed themes` 先 pass）。删掉
  `--az-grad*`/`--az-on-grad`/`--az-acc2`/`--az-tint-b|c` 七个私有键，主操作改吃契约键
  `--cinnabar` + `--on-cinnabar`（白底 5.38:1、深底 7.60:1）；**7 个 `background-clip:text` 块整块删除**
  而不是只换令牌值——只换值会留下「实心背景 + 透明填色」＝文字彻底隐形，本机没有可视手段，
  于是靠静态不变量守：服务端实际下发的产物里 `background-clip` 与 `text-fill-color` 计数 0，
  剩余 4 处 `color: transparent` 逐处复核全是 `border-color`。玻璃收到 dock/衣柜栏/管理栏三站
  （`backdrop-filter` 6 处 = 3 站 × 前缀对，`--az-glass` 消费点 3）；`drop-shadow` 3 条清零；
  20 处 999px 里 17 处归半径刻度；43 处写死字号上字阶、`font-weight` 800/700 全部降到 600、
  小标签 `.14em` 宽距归 normal；9 处 hover 上浮与 90s 背景漂移删除，一次性入场保留；
  `theme.js` 里当图标用的 `☀`/`☾` 字符换成 20px 线稿 SVG（语义仍由 `aria-label` 承担）。
  页脚双色渐变带改实面 `--az-footer`。`pack-theme` 本地校验同 CI 口径：version 3.3.0、size 13616。
- **文档同步**。`theme-spec.md`：§3 纠偏 + 新增闸说明、§9 的 AI 模板补四条护栏（尺寸走刻度、
  层次不画颜色、**渐变字要撤就整块撤**、`var()` 在 SVG 呈现属性里不生效）、§10 写清 SSR 侧禁止
  手抄令牌与 `theme-color` 字面值的例外、§11 派生键补 `--mono`/`--danger-ink` 并说明层次令牌现为
  中性、§12 里「官方样板是超大渐变标题 + 玻璃卡」的过时描述改掉并写明那是能力清单而非推荐做法。

### 同日第三轮 · 主题规范三处与实测对齐

对一个自托管像素字体的私有主题包做体检时，量出三条规范本身的问题，改在规范侧（本轮公开仓
只有文档改动，宿主代码零改动）：

- **§4 列了一个宿主没有的类**。组件安全区里的 .admin-nav-item 在全仓代码中零命中——
  管理后台侧栏渲染的是全局 .nav-item（容器才是 .admin-nav，AdminPanel.vue 里有注释明说）。
  照规范写的主题规则整组是死块。已移除该类并写明实际结构。
- **§4 补 SSR 皮肤库列表的特异性陷阱**。skinlib_ssr.go 的 ssrListStyle 随 SSR 区块注在
  body 里，排在 head 中的主题 link 之后，所以同特异性下宿主胜出：主题要覆盖 .ssr-grid a
  的形态必须写 html .ssr-grid a。只消费令牌不受影响（宿主那部分本来就写 var()）。
  落地页与详情页的内联样式在 head 里，没有这个问题。
- **§12 补自托管像素字体的字号纪律**。像素/点阵字体的字号必须是其栅格整数倍，否则笔画落在
  半设备像素上整片发虚；栅格可用 fontTools 从 unitsPerEm 与 glyf 轮廓坐标 GCD 反推
  （例：1024 em、拉丁 GCD 128 = 8×8，中文 GCD 64 = 16×16，中英混排取 16px 的倍数）。
  这类展示位允许写死字面量字号但要注明依据；连续 clamp(min,Nvw,max) 做不到，应改断点分档。
  同时写明：cmap 覆盖中文的像素字体不会自动回落黑体，别照字体名猜。

### 同日第四轮 · 联盟 hub 里本站站名消失、授权按钮被禁用

- **根因不在数据，在路径拼写**。hub 侧成员站清单里 53 个站**没有一个带 `name` 字段**——站名是 hub
  页面用访客的浏览器跨源读 `bs_root + /api/yggdrasil` 的 `meta.serverName` 回填的，再读
  `/api/union/member` 的 `enabledFeatures` 决定按钮能否用，两条 fetch 共用一个 catch，任一条挂掉
  成员站就渲染成「✕ 代号」并禁用。我们这两个端点只注册了**带斜杠**的形式，而 Go 的 ServeMux 对
  `{$}` 模式的不带斜杠请求由 mux 自己回 307——那条重定向上没有任何处理器头，浏览器整条按 CORS
  拦掉（控制台原文 `No 'Access-Control-Allow-Origin' header is present on the requested resource`）。
  数据侧当时实测正常：`meta.serverName` 非空、`enabledFeatures` 含 `unionOAuth2`。
- **修法不对称，且这个不对称是实测出来的**：yggdrasil 要在 root 与包内两层各补一条规范地址注册
  （root 的 `/api/yggdrasil/` 子树根本不匹配不带斜杠的路径），union 只需包内补一条（`/api/union/`
  子树会把请求透传到内层）。参照实现取自同联盟随机 10 个成员站：9 个在无斜杠地址上是
  `200 + 恰好一个 ACAO`，第 10 个返回 200 但 ACAO 出现两次、以另一种方式失败——所以断言里同时
  钉住「只出现一次」。
- **测试分三层，逐层带变异**。包内两层各加规范地址断言（含「body 与带斜杠形式逐字节相同」「POST
  不得被放宽成 200」）；组装层新增 `mountCompatSurfaces`，把 root 挂载从 `main()` 里抽出来才第一次
  可测——本缺陷正活在两层之间：2026-10-24 那次把 CORS 头加在处理器上，包级测试从此全绿而生产一直是
  坏的，所以只在处理器层补测试等于用同一套盲区再赌一次。
- **真机 A/B**：本地 rig（含修复）与未更新的生产，用同一台一次性跨源仪器跑 hub 那段取名逻辑——
  rig 两条腿 `200 / type:cors / redirected:false`（307 是彻底没了，不是给重定向补头绕过去），
  生产两条腿 `TypeError: Failed to fetch`。另验 `union_enabled=0` 时新路由仍被 gate 挡成 404，
  没把能力文档在关模块时漏出去。
- **文档跟着实测改**：`compat-matrix.md` §1 那行原本把兼容面写成 `GET /api/yggdrasil/`（带斜杠），
  现改为两种拼写并写明原因；§3 新增 hub 浏览器端取名一行；`deploy.md` 验收 curl 换成不带斜杠的
  规范地址——那串正是同一篇文档告诉玩家在启动器里填的形式。全部本地 rig 取证，生产站零操作，不发版。

### 同日第五轮 · 名字该是人看的字：联盟列表、衣柜弹窗与两处静默坏数据

- **联盟服务器列表的「名称」列装的是域名，而且回填代码根本不可能跑**。本页照老插件的样子做浏览器端
  取名（逐个跨源读成员站 `/api/yggdrasil` 的 `meta.serverName`），可我们自己的 CSP 是
  `connect-src 'self'`——实测带这条头的页面里跨源 `fetch` 直接 `TypeError: Failed to fetch`，控制台
  `Refused to connect because it violates the document's Content Security Policy`。于是每次访问都发
  53 个被浏览器立刻拦掉的请求，52 行掉到「域名」兜底，只有本站那一行有名字（它同源）。hub 侧
  53 个成员站**没有一个存 `name`**，所以这一列本来就恒空。改法：列名改成它真正装的东西（代码 /
  站点地址，地址保留路径段，因为有的成员站挂在子路径下），删掉那段结构上不可能成功的代码，并加一行
  说明。要真给出名字必须换成服务端解析，那是一条新的对外抓取面、必须带 SSRF 护栏，另开一轮。
  顺带修一个真缺陷：hub 列表里 `YITUEM` 带两个不同地址重复出现，而 `:key` 与名字表都按 code 索引。
- **衣柜那一行「材质」和「条目」以前不是同一行**。`/me/wearable` 按 `texture_tid` 分组取
  `MAX(item_name)`（字典序最大的别名），排序却用 `MAX(c.id)`（最新条目），而 `user_closet` 按铁律 7
  保持老站形状没有唯一键、`AddClosetEntry` 又是先查后插，所以同一材质可以有两个条目。RED 实测坐实：
  造两条别名 `zzz-older` / `aaa-newest`，接口返回的 label 就是 `zzz-older`。第一版把规则定成
  「最新条目」，四条集成腿当场判错——重复条目的**真实形状**是一条带别名、一条 NULL（0022 回填与
  老站导入都这样落），按最新会选中 NULL 那条，别名反而看不见，比原来还差；而那条测试早就写明搜索
  要够到玩家可能输入的三样东西。最终规则是**优先有别名的、其次最新的**：一行仍等于一个条目，
  id / 别名 / 描述 / 搜索四者一致，别名继续可见。用 `NOT EXISTS` 而非窗口函数，因为 MySQL 5.7
  没有 `ROW_NUMBER`。载荷补 `closetId` 与 `named`。
- **选皮肤弹窗不再拿哈希当标题**：无名条目主行显示「未命名皮肤/披风」、指纹降为第二行标识（以前两行
  都是哈希），每格就地「改名」——改名 API 与衣柜页的弹窗早就存在，但要用它得先离开这个弹窗；现在
  这里就是最需要名字的时刻。每页 12 → 24 且网格自带滚动（`DialogPanel` 只约束宽度不约束高度），
  几百件的衣柜页数直接减半；`shortHash` 改回用 `textureLabel.js` 那份，不再自己抄一份。
- **别名会把坏编码静默存成黑色菱形**：Go 的 `json.Decoder` 对非法 UTF-8 是**替换**而不是报错，所以
  处理器拿到的串永远是合法 UTF-8——上传那条能拦（multipart 不过 JSON），改名这条拦不到。实测一条
  GBK 别名返回 `{"ok":true}`，存进去的是 4 个 U+FFFD。现在两条路都拒（`common.invalid_value`），
  并断言被拒的那次没有写进去。
- **安装向导的报错以前是硬编码中文**：四条 `error.value = '…'` 在英文界面下整页中文。闸只查模板里的
  中文、不查 script 里的赋值，所以它一直漏网；四条全部进词典。全站同类残留扫过，现为 0。

### 同日第六轮 · 站名还是不显示：第二个 ACAO 来自反代，不是我们

第五轮把裸 `/api/yggdrasil` 的 307 修成 200 并带上 CORS，站长部署新镜像后回查：307 确实没了，
hub 侧站名**仍然空**，控制台换了一条新失败——`Access-Control-Allow-Origin` 出现**两个** `*`
（`multiple values '*, *', but only one is allowed`），请求 `net::ERR_FAILED 200 (OK)`。
这正是上一轮在私有登记里预警过的分支：症状一模一样，根因完全不同。

- **只读取证定位来源**。逐路径 `curl -D -` 数头：`/api/yggdrasil` 与 `/api/union/member` 各两条
  ACAO，`/api/union/member/oauth2/` 也是两条，而 `/`、`/healthz`、`/api/v1/setup/status` 各一条。
  多出来的那一组（`Origin: *` + `Methods: GET,POST,OPTIONS,PUT,DELETE` + `Headers: DNT,...` +
  `Expose`）在**每个**响应上都出现，且排在应用自己的头之前——是反代 blanket 加的。
- **真凶不在站点配置里，这是第二轮取证才坐实的**。站长把 `#PROXY-START/` 段和整个 `server` 段
  都贴出来，两处都没有那四行。两条线索指向别处：那组头**只在请求带 `Origin` 时才出现**（裸
  `add_header` 是无条件的），而且它连 nginx 自己拦下来的响应也加得上——打 `/.git/config` 拿到一张
  `<title>网站防火墙</title>` 的 403、`Cache-Control: no-store`，那四行 CORS 头照样在。结论：
  **宝塔 Nginx 防火墙（网站防火墙）的「跨域设置」在响应阶段统一注入**，那串值就是它面板里的默认值；
  `nginx -T` 搜不到，删站点配置也没用。关掉之后闭环。
- **应用侧无法自愈**：nginx 在应用响应**之后**才追加头，Go 代码看不到也删不掉；把 CORS 交给
  反代又会让没有 blanket 头的部署（Docker 直连、宝塔裸端口）失去这层。所以本轮**不改代码**，
  只把定位方法与验收写进公开文档，避免下一个部署的人再踩：`deploy.md` 新增「CORS 头」小节
  （三条按顺序排查的实测线索 + 逐路径计数命令 + 确实需要窄加时按 `location` 窄写的正确姿势），
  `compat-matrix.md` 联盟取名那行补记后半症状、真凶与修法。
- **连带症状**：`/api/union/member/oauth2/` 应用侧按要求返回具体 Origin（非 `*`）并带
  `Access-Control-Allow-Credentials: true`，与 blanket 的 `*` 同时存在时既重复又违规，
  从 hub 发起的本站登录也一起坏——同一个根因的第二处表现，关掉后一并恢复。
- **顺手量出第二条静默失效**：`server` 层的 `add_header Strict-Transport-Security` 在**所有**
  反代响应上都不下发（实测计数 0）。原因是 nginx 的继承规则——`location` 只要自己写了任何一条
  `add_header`（宝塔反代段自带 `X-Cache`），就不再继承上一层那一组。已写进 `deploy.md`
  「反代自己写的 add_header 会吞掉上一层那批头」小节。
- **端到端读数（真浏览器，hub 页面）**：四条路径计数 `1 / 1 / 1 / 0`；`/api/yggdrasil` 的
  `meta.serverName` 读到「温州商学院皮肤站」；hub `/union` 的成员行与服务器行都显示站名，
  按钮 `disabled=false`；hub 控制台 17 条报错**没有一条**指向本站，其中 ECNU 与 nekoplay
  正是我们上周那个坏法（`multiple values '*, *'`），它们至今还坏着。
- **not-run**：nginx 防火墙的开关在站长面板侧，本轮无法代跑；上面的读数是站长关掉之后测的。

### 同日第七轮 · 弹窗仍是哈希：是白底白字；HSTS 顺手收进服务端

- **衣柜弹窗「全是哈希」的真凶不是数据，是渲染**。站长三张图对着看才定位得准：衣柜页
  「材质名称」列有名字，弹窗里同一批材质只剩指纹 ⇒ 后端与载荷都对，是名字那行被画没了。
  全局 `button` 规则把「实心底 + 浅色字」配成一对（`--cinnabar` / `--on-cinnabar`）；
  上一轮把格子改成 `div` 包两个 `button` 时，`.picker-pick` 覆盖了 `background: var(--panel)`
  却没覆盖 `color`，文字仍是近白压在白底上。指纹行反而看得见，因为它带 `.muted`、显式设了
  color。真机读数：修前 `pc-label` 的 computed color 是 `255,255,255`（背景同为 `255,255,255`），
  修后 `33,28,36`，六格名字逐字读到。**名字一直在 DOM 与无障碍树里，就是不在屏幕上。**
- **同族缺陷扫过，现为 0 条**：全站搜「写了浅色 background、没写 color、且挂在 `<button>` 上」
  的规则。这条扫描本身拿修前的文件验过不空转——它确实抓得到 `picker-pick`。
  （把它做成 `scripts/` 里的常驻闸是新增构建步骤，单独开轮。）
- **HSTS 从「等站长改面板」改成服务端自己发**。上一轮量出 `server` 层的
  `Strict-Transport-Security` 在所有 200 响应上计数为 0（nginx 只在当前层级一条
  `add_header` 都没写时才继承上一层，而宝塔反代段自己写了 `X-Cache`）。在反代里补有两个问题：
  段里还有 `if` 块，`if` 内写了 `add_header` 时连本 location 的一组也不继承，得逐处补；
  而且这条头生不生效从此取决于代理的指令摆法。所以它进了 `securityHeaders`，
  **判据用现成的那条**（这条连接是 TLS，或 `X-Forwarded-Proto: https`）——纯 http 的站
  收到一年期 HSTS 等于把自己锁在门外，所以它不能无条件发。
  先红后绿：三条 https 形状红、两条 http 形状当场绿（不空转）；变异（去掉 scheme 闸）
  让两条 http 形状立刻红。真机读数：无 XFP 无、`X-Forwarded-Proto: http` 无、
  `: https` 有且只有一条。文档同步：反代里那句可以删掉了。

### 同日第八轮 · 把联盟这条链整条读一遍，差异四条逐条补

上一轮收口时只查了 hub 与反代，这次把参考实现（MUAlliance 的 yggdrasil-connect 成员站侧）
从路由、事件监听、入站验签到 twig 取数整条读完，再对着我们自己的实现列差异账。
顺带纠一个会误导后续判断的事实：`blessing-skin-plugins` 单仓里那个同名插件与本主题**无关**，
全仓 `union` 引用计数为 0，它只是 Yggdrasil 认证服务器插件；联盟实现只在 MUAlliance 那份。

- **改名与自删角色从不通告 hub**（本轮最值钱的一条）。`SyncProfileRename` 与 `SyncProfileDelete`
  早就写在 `outbound.go` 里、各自还有录制 hub 的单元测，**但全仓没有调用者**——角色在本站改名或
  被玩家自己删掉后，hub 仍按旧名发布它。参考实现是从核心事件发的（bootstrap.php:97-119），
  而且两侧都没有任何定时对账（全仓无 Schedule），所谓「联盟自己会同步」这个前提是错的，
  我们代码里那条注释就是这么写的。修法：三条腿统一挂在 `account.Service`（一处覆盖自服务、
  管理面板、插件桥），面板那条绕过 service 直连 store 的改名另补同形钩子。
  新增组装层守卫：字段声明、调用点、接线三处必须给出同一组名字——单看任何一层都不会红，
  这正是它能藏两周的原因。四条变异（去通告、拆接线、把身份换成玩家名、去掉一条腿）逐条验证会红。
- **安全级别徽章**（站长点名要的那条）。参考实现是两跳：成员钥匙换本站代号，再用代号读
  `security/level`，响应体是裸数字，页面按 3 绿其余黄。我们此前只有半条（第一跳是死代码），
  现在补齐并给 `GET /api/v1/union/security-level`。一处刻意不同：**读不到绝不显示 SL0**——
  SL0 是「未接入联合黑名单」这个真实结论，拿它兜底等于在最坏一档上冤枉站长；上游失败明确 502 +
  页面写「暂时读不到」。另外老站每次看页跑两跳，这里改 10min 正缓存 / 1min 负缓存，
  且 hub 读用独立 context，某个玩家关页面不会让所有人继承一次失败。
- **`enabledFeatures` 少报两项**。两个字段一直躺在 `config.go` 里，但装配处只设了 OAuth2 那项，
  生产永远不置位，于是开了邮箱验证、开了邀请码的本站对 hub 少报两项能力。语义有出处：
  邀请码那项在 MUAlliance 的 invitation-codes 里就是插件启用写 true、停用写 false，
  等于「本站注册要不要邀请码」。改成按请求读选项，与老站 `option()` 同构。
- **成员站名改由本站后台读**。hub 不存站名（实测 53 条 `name` 全空），老插件是让访客浏览器
  逐个去读成员站的 `/api/yggdrasil`；我们本页 CSP 是 `connect-src 'self'`，这条路结构性不通。
  服务端读五十多个第三方主机是新出站面，所以不复用 hub 那个裸 client，改走插件/主题市场那套
  受控客户端（只连公网、拒 IP 字面量、禁重定向、只放 80/443、限响应体、每主限速），
  并且不照抄 `bs_root`——只取 scheme 与 host，路径固定成我们自己的 `/api/yggdrasil`，
  清单里塞路径/查询/凭据都读不到别的东西。后台补、24h 正缓存 30min 负缓存、同一时刻只跑一轮，
  打开页面那一瞬不等待。面板 `union_resolve_names` 与 `SKIN_UNION_RESOLVE_NAMES` 可关，
  关掉后连已读到的名字也不再显示。两个测试走真实受控客户端：一个证明策略确实放行那条路径
  （写错路径会静默全失败），一个证明留了测试后门之外的环回对端读不到。
- **CI 拦下我们自己的重构**（值得记的教训）。把三条通告折成一个 helper 时，动作名变成参数，
  而双语审计闸是按「emitter 调用的第一层字符串实参」收集动作的——`web` 作业当场红，
  同时报「新动作没翻译」与「旧文案是死字」。改回三条显式写并把原因写进注释：
  helper 省下的十二行不值得让一条门禁失明。
- **本轮读到但没动**（进仓外登记，不塞进这一轮）：入站验签的 nonce 重放缓存是进程内的，
  多副本部署会各自放行同一签名（参考实现用跨进程 cache），要么落库要么把「联盟入站要单实例」
  写进部署文档；hub 那侧出站仍是裸 client；`union_enable_oauth2` 面板标成热生效但热应用分支里
  没有它；两处 hub 读都还没有 single-flight。
- **not-run**：徽章与站名列的浏览器真机读数（本轮以 HTTP 层与进程内集成测试为凭）；
  生产站零操作，真实 hub 的 `security/level` 取值需站长部署新镜像后在联盟页自查。

## 2026-11-07

- **真机把诊断模式走通时抓到三件测试看不见的事，都已修（这是本轮最值钱的读数）**：
  ① **明细正文里存着明文口令**。`body_excerpt` 存的是请求体原文，而认证/登录的请求体里就有
  `password`——它跟的是 7–90 天的保留期，不是令牌那 24 小时；读取面又对所有管理员开放。
  上一段"读取端点不含凭据值"这句**当时就是说满了**：它对 `credential_ref` 成立，对隔壁那列正文不成立。
  现在正文在**落库前**把 `password` / `*Token` / `secret` 这类键的值抹成 `[redacted]`（键名与其余字段照留，
  那才是排查要看的），JSON 解析不开又撞到这些键名的整段丢下不猜；写两次 sanitize 幂等，
  变异（把脱敏短路）当场红出六条子例。② **状态码恒 0、耗时只到记日志那一刻**：记录器在处理器决定结果时就写了，
  那时响应行还没出去。现在 Observe 在响应结束后把真正的状态码与总时长交回，明细行按 id 补这两列——
  只补这两列，凭据列与到期线一概不碰（一条把 0 当"没观测到"就写回去的变异当场被红出来）。
  ③ **面板展开明细必然 500**：`dialect.Placeholders` 本身已经带括号，我又给它套了一层，
  于是 SQLite 报 `row value misused`；此前 5d 的读测试全部替换了取数桩，**这条 SQL 从没在真库上跑过**。
  补一条真库集成腿（3 个 id / 1 个 id / 含不存在的 id / 空列表四种形状），三引擎矩阵都真跑它。
- **文档口径跟着改**：deploy.md §2.7 的"要知道的"从三条变成四条，第四条写明正文脱敏发生在落库前，
  并补一句面板读取（`access-log/details`）对所有管理员开放、只有开关认超管网页会话。
- **SSR 页面第一次给出可点的中·EN 对照**。hreflang 早就声明了两种语言的可寻址形式，页面上却一条
  指向它们的链接都没有——读者要换语言得先知道 `?hl=` 存在，爬虫也只能从 sitemap 走过去。落地页、
  材质详情页、皮肤库列表页各出一条 `rel="alternate"` 双语链接，href 与 head 里的 alternates 同源
  （`hl` 是替换不是追加，其余筛选参数留着）。另一半是持久化：`?hl=` 优先级最高却从没被写进
  `skin_lang` cookie，从 SSR 页切英文再进 SPA 会回到协商语言；现在由服务端下发，属性逐字镜像
  `web/src/i18n.js`（不 HttpOnly、SameSite=Lax、Secure 随 https、一年）。持久化放在渲染路径里
  而不是挂在 mux 上——第一版挂路由时，测试当场抓到"不走那条路由的渲染路径静默丢掉持久化"。
  404 明确不给切换：那条链接指向另一语言下一条不存在的 URL，且 noindex 页本就不出 alternates。
- **日志诊断模式落地（5a–5e + 面板展开）**。超管可开一个 **≤24h 自动过期**的诊断窗口（选项
  `access_detail_until`，只有超管的**网页会话**能改，机器令牌一律 403，因为这条开关的审计必须归到
  真实账号）；开着时同一请求除了摘要行，另在 `access_log_details`（三树迁移 0029）落一行明细：
  状态码、耗时、上游错误码、`User-Agent`/`X-Request-Id` 两个头、正文节选，以及**窗口内**的令牌原文。
  两条留存时钟刻意分开：明细行跟面板可设的 `access_detail_retention_days`（默认 90 天，活读），
  带凭据的行跟**固定 24h**——90 天是日志保留期，不是把可用凭据存一个季度的许可，清扫只抹凭据列、
  行本身留着。头是白名单不是黑名单；正文截断 4 KiB 且按 rune 边界回退（中文不会被截出 U+FFFD）。
  面板日志页可逐行展开明细；读取端点**不含凭据值**，只回 `credential_present`——"能看日志"不该
  等于"持有全站可用会话"。摘要有凭据不进日志的既有规则一字未动（`accesslog_credentials_test.go`）。
- **修复一个登录洞：空摘要 + 未计算的 legacy 算式 = 任何密码都能登录**。`computeLegacy` 对它没有
  计算分支的 method（`bcrypt` / `argon2i` / `php_password_hash`，以及任何将来的新名字）返回空串，
  而空 `password` 列不以 `$2`/`$argon2` 开头，于是落到 `hmac.Equal(空, 空)` 成立。迁移带来的
  `password` 列为空的行走的正是这条路。现在一律先拒空摘要；**行为变化**：这类行不再能用任何密码
  登录，需走找回密码或管理员代设（口径见 `compat-matrix.md` §4）。G10 的特征测试原先把它钉成
  OBSERVED 并写明"若有意关掉就改这一句"，本轮按那句话翻转了断言——改的是被授权的翻转，不是放宽闸。
- **`skinDomains` 为带端口的条目同时发布去端口孪生**。authlib-injector 一类客户端比的是
  `URI.getHost()`（不含端口），所以 `site.invalid:8443` 这种条目永远匹配不上：账号能登、皮肤静默不加载。
  只增不改——已发布条目一字不动、位置不变，无端口的部署输出一模一样。
- **复用名重建不再以内部错误冒出来**。改名让开的名字再被重建时，铁律 2 保证 UUID 逐字节相同，
  于是 `uuid_map` 撞 `UNIQUE(uuid)`，原始驱动错误直接逃成 `internal`。现在与管理员改名同一处理形状，
  收敛到 `ErrDuplicate` → `player.name_taken`。改的是报错形状，不是"能否重建"。**同片翻掉了一条
  已提交的 OBSERVED 断言**（`uuid_reuse_characterization_test.go`）：那条断言原本把这个 500 形状钉成
  现状，并写明"若有意关掉就改这一句"——本轮就是那次有意关掉，不是有人放宽了闸。
- **落地页加 answer-first 事实块**（五条，纯正文层）：启动器协议面、四套数据库引擎、blessing 无损
  导入、验签市场、匿名可读的服务端渲染页。不加 FAQPage 也不新增任何 JSON-LD 类型——AI 搜索不需要
  特殊 schema，写标记不等于被引用。资源/限流阈值那条**没写**：面板可配、没有单一真数，写上去就是说满。
- **协议与旧站兼容面取证（只钉现状，不改语义）**：`uploadableTextures` 恒 `skin,cape` 且不参与签名；
  textures JSON 在 Go 下是确定序（`CAPE` 先于 `SKIN`）；旧站 9 条算式我方 12 条全覆盖，
  `SALTED2*` 三家的期望值改用独立复算的已知向量（原先有一条测试是拿被测代码自己算期望值的同义反复）；
  删角→改名→重建链的 UUID 行为被特征测试固定住。

## 2026-11-05

- **插件页不再"先默认主题、跳一下再变主题"**。根因不是动画或延迟，是通道选错了：每个插件文档
  自带 `:root{--plugin-bg:#101825;…}`，**第一帧必然画它那套**，而主题令牌只能走 JS 桥
  （frame `ready` → 宿主读 computed style → `init` → 插件 `applyTheme`），这条链一定在第一帧之后，
  于是肉眼能看到一次配色跳变。修法是让主题离开这场比赛：插件页的 HTML 是宿主发出去的，
  所以把**当前主题的根令牌**和一段 `--plugin-*: var(--bg,#101825)…` 的映射**插到 `</head>` 之前**——
  顺序是关键，必须排在插件自己那段 `:root` 之后，同特异性后写的才赢。`init.theme` 照旧发
  （`dark` 判定、语言、用户态只有这条路能到）。没启用主题时文件**一字不改**地发出，
  CSS/JS 资产一律不重写。断言：注入落在插件自己的 `:root` 之后且在 `<head>` 内、令牌段排在映射段之前、
  无主题与 nil 注入器都逐字节原样、非 HTML 不动、`Sandbox` 与 `must-revalidate` 跟着改写活下来
  （缓存钉住旧主题就是下一个 bug）。另有一条防漂移闸：宿主 `PluginFrame.vue` 里那九个
  `cssVar(…, 默认值)` 与注入表的九个回退**必须逐项相等**——这道闸当场抓到过一次真实不一致
  （`rgba(169,138,95,.24)` 被 `[^)]*` 截断成半个值）。
- **上一条的第一版把整张主题表 `<link>` 进插件文档，是个回归，被站长一眼看出来了**。主题的样式表
  是给宿主 DOM 写的，里面有一批裸元素规则——wzbc-violet 就有 `body{background-image: 三枚柔紫高光}`、
  `h1,h2{font-family: 像素体; font-weight:400}`、`input,select,textarea{font-size}`、`table{font-size}`、
  `th,td{border-bottom}`、`html{overflow-x:clip}`。它们一起作用到插件自己的 `<body>`/`<h2>` 上：
  实测 `body` 的 `background-image` 从 `none` 变成 `radial-gradient(920px 480px…)`、`h2` 从插件自己的
  无衬线 600 掉成像素体 400、`input` 14px→13px、`html` overflow-x visible→clip。改成只注入令牌：
  `theme.TokenStyle()` 从主题声明的 CSS 里抽出 `:root`/`html` 块上的自定义属性与 `color-scheme`
  （`@media` 包裹保留，明暗双主题靠它切换），其余选择器一概不带。两道闸各管一半——选择器闸挡住
  `body{--bg:…}`（写在非根选择器上的令牌不进插件页，回落到插件默认值 + JS 桥），属性闸挡住
  `html{overflow-x:clip}` 这类根元素上的宿主决定；两处各做过变异取证。
  抽取器自己也被注释咬过一次：注释不是选择器也不是属性名的一部分，但按字节切分时
  `--bg: #f7f5f0; /* 纸感暖白 */` 会让注释尾巴粘进下一条的属性名，wzbc-violet 那一整块配色因此
  被静默丢掉、`--plugin-bg` 悄悄用回回退值——只有真机计算值暴露了它，单元测试当时全绿。
  真机复验（浏览器直接开注入后的插件 HTML，桥不触发、`documentElement` 无内联 `style`）：
  九个 `--plugin-*` 全是主题值、`color-scheme` 随主题，同时 `background-image:none`、`h2` 回到插件自己的
  字体与字重、`input` 14px、`overflow-x:visible`；`init.theme` 会写的九个值与它们逐项相等，
  所以桥到达时不再产生第二次变化。**未做的**：逐帧录屏（截图通道不可用）。
- **皮肤库列表页第一屏服务端渲染**（上一条 hreflang 的落地续集）：列表是 SPA 路由，交出的 HTML 里
  `#app` 只有一个转圈。Bing 的规范说关键内容别藏在客户端渲染后面，GEO 那边更硬——
  GPTBot/ClaudeBot/PerplexityBot **不执行 JS**，对它们这一页就是空的。于是玩家真正会搜的那一页
  既没可爬文字也不发内链。现在第一屏（24 条）在服务器渲进 `#app`：真实 `<a href>` 进详情、
  `<img alt>` 带材质名与种类、总数与分页都是文字；两个 marker 夹住可替换区间，别的路由启动占位不动；
  SPA 挂载照旧接管，搜索/筛选仍在客户端。连带消掉三处自相矛盾：`/skinlib` 从 noindex 变 index、
  回到 sitemap（按语言各一条）、`SearchAction` 指回 `/skinlib?q=`（这页现在真的在服务端回答 `?q=`）。
  分页链接带齐 `page/sort/type/q/hl`。真机看到的效果是**空库空态**（那个库里没有公开材质），
  网格与 alt 那半由 5 条单元测试证明；`check-locales` 顺手教了一件事：SSR 文案命名空间不能和
  插件槽名/URL 前缀撞（`skinlib.*` 会把 `skinlib.detail.sidebar` 当成没翻译的文案），改用 `lib.*`。


- **两种语言从此是两种 URL**（SEO/GEO 第一片）。此前一个 URL 靠 cookie 与
  `Accept-Language` 决定语言：`hreflang` 全仓零命中，canonical 不带语言，站点图谱里
  `inLanguage` 无论页面是什么语言都写死 `zh-CN`——爬虫只会编入它碰巧抓到的那一个变体，
  另一种语言在索引里等于不存在（Google 的 get-started 与 Bing 的规范都要求「显式声明本地化版本」）。
  现在 `?hl=zh-CN|en` 是一等 URL：`ResolveLang` 把它排在 cookie 与请求头之前（渲染服务
  会清空 cookie，把语言只存在 cookie 里等于对爬虫不存在），页面自 canonical 到带 `?hl`
  的自己、把另一语言声明成 alternate、裸路径留作 `x-default`（它本来就 canonical 到某个
  语言页，再列一条 `<loc>` 只会造出第三个副本）。`og:locale` / `og:locale:alternate` /
  `inLanguage` 跟着页面走。
  sitemap 两处一起纠正：每个页面按语言各列一条 `<loc>` 并带 `xhtml:link` 交替声明；
  `/skinlib` 列表页**暂时从 sitemap 移除**——它是客户端渲染的壳，壳一律 `noindex`，
  一边递 URL 一边叫人别索引是自相矛盾的信号（真要拿回这一页，办法是服务端渲染列表，
  而不是把 `noindex` 摘掉）。同理由是指向 `/skinlib?q=` 的 `SearchAction`：
  Bing 对「标注与页面不符」的处理是忽略标注，Google 也已下线站点搜索框富结果，
  这一节点一并去掉，列表页服务端化之后可以名正言顺地回来。
  真机验证（本地实例）：`/` 与 `/?hl=en` 分别给出
  `rel="canonical" href="…/?hl=zh-CN"` / `…?hl=en`，两条 `hreflang` 交替 + `x-default`，
  `og:locale` 与 `inLanguage` 随页面语言变化。


- **把安全加固那一轮自己弄坏的东西逐项找回来**（七路并行审计，每条结论都回读代码复核后才写在这里）。
  同一轮里最像的一条：`477267b` 之后插件桥的静默回归不是孤例，加固的问题形态基本都是
  「**守卫只落了一半**」或「**失败不留声音**」。这轮修掉九处，四处只补文档口径，两处记账不动手：
  · 主题市场的 install/uninstall/upload 三条路由注册时漏了 `b.auth`（`adminapi/appearance.go:90-92`），
    而 `actorUID` 只有 `auth` 会注入 → `pluginSuper` 永远看到 actor 0 → **面板里这三个动作对谁都 403**，
    README 却写着「主题市场、本地主题包上传」。补齐认证链后真机复验：装一个不存在的包回
    `409 theme.not_found`（说明请求到达了服务），修前是 `403 admin.web_session_required`。
  · 全局 1 MiB 请求体上限只豁免插件上传，主题上传自己声明的 16 MiB 根本没机会生效
    （`adminapi.go` 的豁免现在是一张 `packageUploadRoutes` 表，并加了一条读自身源码的闸：
    解析 multipart 的路由必须在表里，表里的路由必须真的解析 multipart——两个方向都会红）。
  · `X-Admin-Token` 排在会话之前：浏览器只要曾经存过令牌就每个请求都带，于是人被判成机器、
    actor 归零，所有「必须网页会话」的操作对登录着的超管也是 403。现在令牌匹配时若同请求带着
    会话 cookie，先认人；纯机器调用与「带一个不是管理员的 cookie」仍然落回原通道（两条都测了）。
    面板另外补了「本机仍存着 API 令牌 / 清除令牌」一行——之前这张卡在 `permission>0` 后就隐藏，
    管理员既看不见也清不掉自己浏览器里那个凭据。
  · 一个站点只有一枚 `skin_csrf`（`Path=/`），但每个插件 iframe 各存各的：`/_csrf` 每调一次就
    重新签发，后开的页面把先开的顶掉，先那个的写操作全部 403 `plugin.csrf_failed`，页面开着
    12 小时过期也是同一条路径。现在同一浏览器重复申请拿回同一个值（只认 64 位小写十六进制，
    外部塞进来的值一律换掉），两条断言都在 `plugin/csrf_stability_test.go`。
  · `/avatar/hash/{hash}` 是 `65e2beb` 那条私有材质门禁的**兄弟路由**，但它没被改：不查可见性、
    `private` 硬编 false、响应 `public, max-age=86400`——库里 `avatar.go:299-301` 那句
    「按 content address 寻址所以不用查」的前提，被同仓库 `avatar.go:408-411` 的注释直接否定。
    现在与 `/texture-render` 同规则：允许的人看到脸，其他人拿到默认头像且一律 `private, no-store`；
    公开哈希的缓存行为不变（一条测试专门钉这点，免得把性能修没了）。
  · 插件 KV / 文档前缀扫描挂了 `ESCAPE '!'` 却把 `prefix` 原值绑定，`dialect.LikePrefix` 写好了
    **零调用点**：前缀含 `_`（`cfg_` 这种）就多命中、含 `!` 就 0 行，全静默。假实现用
    `strings.HasPrefix`、AST 闸只看 Go 侧 `%` 拼接，所以两道门禁都看不见——补的是真库断言
    （`store/plugin_kv_prefix_test.go`，SQLite 腿，`cfg_`/`urgent!`/`50%` 三种前缀各一条）。
  · `dcc72b3`「日志不留明文邮箱」遮住了 SMTP 那两行，漏了更持久的那处：登录把
    `req.Identifier`（注释自己写着「email or nickname」）原样写进 `access_logs.who`，保留 90 天、
    面板可搜。现在经 `redact.Identifier` ——昵称原样留（管理员要靠它对上号），含 `@` 的才遮；
    顺手修了 `redact.Email` 用字节切片取首字符导致非 ASCII 用户名截出半个字符的问题。
    另加一条读自身源码的闸，防止新的日志写入点又用原文。
  · 「全站安全响应头」此前不是全站：`pub.WrapRoot(securityHeaders(...))` 让 CustomSkinAPI 的
    `GET /{player}.json` 在包装**外层**就答完，nosniff/CSP/XFO/Referrer-Policy 一条都没有。
    包装顺序换正，闸读 `main.go` 自己那行（不是测合成 handler，而是测接线，因为翻错顺序只有真接线看得见）。
  · 插件 `navigate` 桥的过滤是 `startsWith('/') && !startsWith('//')`：`/\evil.com` 能过、
    浏览器按 `\`→`/` 归一后整页跳到别人的站；反过来一个**同站完整 URL** 会被静默丢掉。
    现在解析成 URL 再比 origin（同源放行、跨源拒绝、非 URL 拒绝），闸看源码形状。
  · 顺带发现并修掉一条真实回显：主题包校验失败时响应里带着服务端临时目录路径
    （`mkdir C:\Users\…\skintheme-4290567848: Access is denied`），而且主机磁盘权限问题被说成
    「你的包有问题」，把人推去重打包。决定留在响应里、细节进日志（`?inspect=1` 才是给人看的结构化通道）。
- **E1 的覆盖面补到 OIDC 那条腿**（`c8dcbb9` 今天才进 main，演示机还在 11-02 的 `sha-92bff16`，
  生产未 pull——`token_digest_v1` 至今没在任何实例落过，所以这是唯一一次不用追加第二次不可逆重写的窗口）。
  `oidc_refresh_tokens.token` 是 64 hex 随机串、30 天有效、按主键原文精确匹配，拿到就能换新的
  access+refresh 对（`oidc/storage.go:308`），比 E1 当初收的三列更长命；README:48 那句
  「读到库不等于读到还能用的登录态」对它不成立。现在它也进 `digestRewrites`，签发/查回/轮换/吊销
  一律「客户端拿原文、库里存摘要」；实现陷阱记在代码里：`GetRefreshToken` 必须把 `rec.Token`
  换回玩家呈报的原值，否则 `RotateRefreshToken` 的 `WHERE token = ?` 会对摘要再取一次摘要、
  刷新轮换直接坏掉（集成腿加了「在途刷新穿过重写仍可用」这条断言，四引擎都跑）。
  同处把「什么仍然保持原文、为什么」写实：`client_token`（单独不能认证 + 协议要求原样回吐 +
  `CHAR(32)` 装不下）、OIDC 的 `code`/`device_code`（一次一用、几分钟失效）、`user_code`
  （故意低熵给人抄，摘要形式反而把「读到库」变成「离线爆破」），以及导入器原样搬进来、
  本程序从不读取的 `users.verification_token`。
- **上手门槛那两条**：`docker-compose.quickstart.yml` 从来没引用 `SKIN_SETUP_TOKEN`，而 deploy.md:145
  承诺「自己定一个则跨重启不变」——`.env` 只做插值，不进 `environment:` 就进不了容器，
  所以零编辑路径上这个开关**一直是空的**；README 的安装段落也整个跳过了令牌这一格。
  两个 compose 示例现在都转发它，中英文 README 的段落补上这一格与取值来源，`check-docs` 加第 5 条：
  README 的 `cat > .env` 块、README.en 的同一块、quickstart 自己头注释里的同一块必须三方一致，
  且列出的每个变量都得真的被 quickstart 引用（把这条删掉立刻红，验过）。
  另外把 `SKIN_TRUSTED_PROXIES` 的反直觉之处写进 deploy.md:144：**列了清单就不再默认信 loopback**，
  同机 nginx 要把 `127.0.0.1/32,::1/128` 一起列上；只按「我的反代是 Cloudflare」去填 CF 公网段，
  `clientIP()` 会退回对端地址、每个访客塌成同一个 IP，注册每 IP 上限第三个人被挡、
  四类 `ip:` 限流桶全站共用一只、审计与 `users.ip` 一律记成对端地址。行为本身有测试钉住，坑在文档。
- **主题规范与 CSP 对不上的一句**：`theme-spec.md` 让人用 `custom_head_html`「挂统计脚本、字体」，
  而 CSP 是 `script-src 'self' 'unsafe-inline'`、`font-src 'self' data:`——内联与**自托管**可用，
  第三方 origins 的脚本/样式/字体会被浏览器拒绝。deploy.md:455 早已说明 `unsafe-inline` 是有意的产品取舍，
  所以改的是规范措辞（§8 与 §9 的安装清单都补上了这条边界），不是放松 CSP。


- **修掉我自己 11-04 引入的插件桥失效**（站长报的是「插件 UI 不适配自定义主题」，症状只是最表面那一层）：
  安全轮那条 `477267b`「postMessage 不再发往任意源」把宿主→iframe 的 targetOrigin 从 `'*'` 改成站点自己的源。
  但 frame 是 `sandbox="allow-scripts allow-forms"`（**故意**不给 `allow-same-origin`，否则插件 JS 能读站点 cookie），
  它的 origin 永远是 opaque `"null"`；浏览器比对的是**接收方**的 origin，`"null"` 不匹配 `"https://站点"`
  → 消息被**静默丢弃**。于是 `/ext/*` 插件页再也收不到 `init`：主题令牌、语言、用户态全丢，
  插件自己的 API 调用一次也发不出去（`loadPlayers` 在 init 分支里），点「开始导入」只会卡住。
  宿主侧日志与网络面板一切正常——**这是它难发现的真正原因**。
  取证链：生产 iframe 的 CSS 默认值就是截图里的深底红按钮（`--plugin-bg:#101825`/`--plugin-accent:#b0453a`）；
  生产父页此刻的令牌确实是主题的 `#f7f5f0`/`#601986`，主题 CSS 76ms 就加载完、iframe 181ms 才开始——**不是时序问题**；
  生产 bundle 里 `init` 确实带着 `theme:`；最小实验（沙箱 iframe 各发一条，一条目标写站点源、一条写 `'*'`）
  只有 `'*'` 那条被收到。本地带主题+插件复现：修前 `/api/plugins/…/players` 零请求、iframe 钉死 520px；
  修后该请求出现，非沙箱 harness 量到 iframe 内 `--plugin-accent=#601986`、按钮实际渲染 `rgb(96,25,134)`、
  body 白底墨字、`lang="en"`。
  修法不是把 A10 撤回去：`'*'` 是唯一能送达的目标，所以规则改成「**经 `'*'` 出去的 payload 不得含凭据**」——
  `init` 不再带 `csrf`（宿主代发请求时自己加 `X-CSRF-Token`，frame 从来不需要持有它），契约与 SPEC 同步改口径。
  新增一条读自身源码的门禁 `plugin_bridge_test.go`：targetOrigin 必须是 `'*'`、init 里不得出现 `csrf`、
  sandbox 不得被加上 `allow-same-origin`（那是权限放大，不是修主题）。三条变异分别取证会红。
  **遗留（记账不粉饰）**：opaque frame 意味着握手用的 `nonce` 也会随 `'*'` 交给「自己导航到别处」的那份文档；
  彻底做法是把 nonce 放进 iframe 的 URL 查询、由子页回显后才认，这需要插件侧一起改，属独立一片。

- **会话与启动器令牌改为只存摘要（不可回滚的一次升级）**：`web_sessions.token`、
  `ygg_tokens.access_token`、`ygg_join_sessions.access_token` 原先存的是客户端呈报的那串原文，
  于是任何一次库读取——备份、SQL 报错回显、被拖走的 dump——都是一张「还能登录的账号表」。现在存
  `hex(sha256(原值))`，比对与查库都先摘要再比；摘要与原文同为 64 位十六进制，长度与前缀都分不开，
  所以不做双读、不加判别列、迁移水位也不为它扩张：首次启动在**一个事务**里改写存量行并落
  `options` 标记，标记与改写同时可见或同时不存在，日志给出行数，之后每次启动都跳过。
  `ygg_tokens.client_token` 故意留原文（它单独不能认证，Yggdrasil 又要求服务端原样回吐，且列宽
  `CHAR(32)` 装不下摘要）。**回滚语义变了**：旧代码退回后比不中任何一条，所有人要重新登录一次，
  且无法用代码修复——`docs/deploy.md` §2.5 把这条写在了升级注意事项里。
  验收是一台真站的两份二进制：旧版建号、登录、签一把在途令牌（库里此刻是原文），换新版开同一套库
  （`auth: rewrote 2 stored token(s) as digests`，原文随即取不出），再用**同一把**在途凭证走完
  `validate 204 → refresh 200 → join 204 → hasJoined 200`，升级前签发的网页 cookie 仍然 200；
  第二次启动不重复改写。改写 SQL 由 `digestRewrites` 列表自己拼，所以另加一条走 CI 四引擎腿的
  集成用例（MySQL/MariaDB/PostgreSQL/SQLite 各跑一遍，断改写前后行数相等）。**这条腿第一次跑就抓到东西**：
  夹具 `testClientToken32` 实际 33 个字符，而 `ygg_tokens.client_token` 是 `CHAR(32)`——SQLite 不检查列宽所以
  它一直「对」，MySQL/MariaDB/PostgreSQL 三条腿同时报 `Data too long for column 'client_token'`；收进 32 位后
  四条腿全绿（run `41942441167`）。变异三次：摘要截半 →
  红在三列的期望摘要；漏掉 `web_sessions` → 红在 `rewrote 2 rows, want 3` 与 `in-flight session = 0 not found`；
  `fsck` 改成恒报 `digest_migrated=true` → 红在「空库」与「升级前」两行。
- **日志策略改回来了（站长拍板）**：昨晚把访问日志里的 `client_token` 落成了 sha256 前缀，
  今天按「管理员要能拿日志排障」的要求恢复原文，并把理由写进用例注释——clientToken 单独不能
  认证任何事（用到它的端点都还要求一把有效 accessToken），而它对排障的价值恰恰是分辨两台安装。
  底线同时钉成断言，两条各配一次变异：**只留前缀 → 红**（`want the full clientToken`）；
  **把 accessToken 也写进日志 → 红**（`accessToken reached the traffic log`）。
  即「可读」与「不可用」是同一枚币的两面，少一面都能被下一次改动悄悄磨掉。
- **一处更正（我写的，不是站长说的）**：上一条里「底线同时钉成断言」的**那半句底线**——「能认证的东西不进日志」——
  是我自己的推理，我把它当成我们已达成的共识写进了文档、提交信息和用例注释。站长当场否认（「我没定啊！」）。
  现在这条由我拍板继续留着，但**换了适用范围**：常驻的摘要行永不含可用凭据；他真正需要的「能顺着令牌排障」
  归诊断模式——原文只在诊断窗口开着的那段时间进明细表，并且按固定的短时钟清（不是日志的 90 天），
  开关本身有审计。用例注释也改了口，不再把这条写成谁也没同意的「底线」。

- **分页的严重性上调了**：上一轮记的是「匿名请求能把皮肤库打出 500」，实得更差——`page` 很大时
  `(page-1)*perPage` 在 int64 上回绕成负 OFFSET，**MySQL 与 PostgreSQL 拒绝负 OFFSET（500），而 SQLite
  把「负数行都不跳」读成跳过 0 行，于是直接把第 1 页当答案返回**。玩家侧看到的是「翻到第 N 页还是那
  些皮肤」，不是报错。`SkinlibList` 与 `ListReports` 现在统一走 `dbx.Offset`（它在乘之前先夹），
  并新增一条 AST 门禁禁止再手写 `(page-1)*perPage` 这种算式。
  验收当时也纠正了一处：`paging_test.go` 里那条用例解的是响应中**并不存在**的字段 `textures`
  （真实字段是 `items`），所以它一直是真空断言——改字段之后再加一条「页超末尾必须返回 0 行、
  total 仍诚实」。门禁非真空由变异证明：把 clamp 分支关掉，集成用例报 `returned 1 rows`。
- **给两个零测试的包补测试，顺手抓到一个校验洞**：`internal/jobs`（队列的持久化与租约，320 行没有一条单测）
  与 `internal/errorgate`（错误信封的 AST 门禁本体，462 行——它一直守着别人，自己没被测过）现在分别是
  92.4% 与 82.8%，覆盖率白名单两条随之删除，地板从 40.0 提到 45.0（实测 48.7%）。
  写「畸形请求体必须在碰到存储之前被拒」这组用例时，`POST /api/admin/v1/oidc/clients` 露了馅：它的守卫是
  `body.ID == ""`，**不带 trim**，而同包的黑名单添加用的是 `strings.TrimSpace` —— 于是
  `{"client_id":"   "}` 能过校验，把一行谁也叫不出名字的 OAuth 客户端写进库。现在按 trim 后判空，
  并且落库、审计、响应回吐都用 trim 过的值。

- **sitemap 的上限终于只有一个真源，而且方向改对了**：`SitemapIDLimit` 当初导出时写的理由是「路由与只读体检必须说同一个数，
  只有一方知道的数字没人能审」——但 `/sitemap.xml` 那条仍传字面量 `5000`，`fsck` 传常量。今天把路由改成常量，
  并加一条读自身源码的用例钉住（`PublicTextureIDs(..., store.SitemapIDLimit)` 必须成，传数字必须红）：
  断言只认调用里的表达式，注释里提一句常量名不算过关。变异取证：改回 `5000` → 红并打印出那一行。
  更实质的一半是**方向**：原来 `ORDER BY id LIMIT 5000` 取的是**最小 id**，库一旦长过 5000，爬虫被告知的就永远是
  最旧的那批，新增的材质一条都不上报，且没有任何地方说这件事。现在 `ORDER BY upload_at DESC, id DESC`——
  上限的代价从「新内容不上报」变成「旧内容不再重复上报」（每条都带 `lastmod`，回访本来看的是它），
  而且这正是皮肤库默认视图的顺序，`idx_textures_public_upload`（0024）反向扫即可命中，
  连那条迁移注释里自己承认的「sitemap 查询用不上顺序、还得排序」一起修掉。上限保持 5000（抬到协议允许的
  5 万条会让每次请求现拼约 4.5 MB 的 XML，那是另一笔账）。`fsck` 的 `omitted=` 文案同步改口。
  用例先写歪过一次：把断言加在只有**一行公开材质**的旧夹具上，变异成升序照样绿——四条断言全是空的；
  换到有 3 行公开材质的夹具才真的红出「starts at tid 1, want the newest 3」。**「测试通过」不等于「测试在测」**。

- **双语门禁扩到 `.vue`，三张页面 67 行搬进词典**：`check-locales` 加了第 6 条——SPA 模板区不许留未译的中文。
  SPA 有 fallback 语言，这恰恰是需要门禁的理由：vue-i18n 缺键时回落 zh-CN，英文界面里塞进一句中文，
  写它的人看不见、每个访问者都看得见。区间用**深度配对**找（`<template>` 里具名插槽会再嵌一层
  `</template>`，用「从 `<template>` 到第一个 `</template>`」这种区间扫法会提前截断，剩下的半页就成了盲区，
  我此前就是这么把 39 行数成 27 的）；整段 HTML 注释用**原地空格化**抹掉而不是删除，报告里的行号才是站长
  要打开的那一行；命中**按行**报一条（逐字符会刷出几百条噪音）。
  搬完的三张页面：`SettingsView.vue` 39 行、`SetupPage.vue` 27 行、`App.vue` 1 行（此前记的 27/26/1 也是我数错的），
  新增 60 个键 × 2 语言，UI 词典 922 → 982。语言下拉里的 `中文` 是**自称名**，两个词典里都写 `中文`，
  把它当数据放进目录，而不是给门禁开个豁免。
  页脚那行的 `{year} / {site_name} / {site_url}` 是技术字面量不是文案：留在模板里按字符串渲染，
  只翻译句子本身——写进 vue-i18n 的消息里，花括号会被当成插值参数。
  非真空：往一张零命中的页面注入一行中文 → 门禁红在这一行；撤掉 → 绿。`npm run build` 通过。
  **真机**：本地新库起服务，英文下把安装向导四步走完（填表、提交、建号、看第 4 步文案），
  再进管理面板的设置页中英各读一遍——标题、按钮、提示都出自目录，无裸键路径、无未解析的 `{}`。
- **真机顺手撞见的缺口（记账，这片不动）**：设置页每张卡片里**每行的描述**是服务端给的（选项目录自带文案），
  英文界面下那一片仍是整屏中文（实测页面里 588 个汉字）。门禁只看模板字面量，看不见 API 返回的数据，
  所以它「绿」是真的、这一页英文用户读不懂也是真的。要做的是后端选项描述目录的双语化，独立一片。

## 2026-11-04

- **安全段（A1–A10）**：三路并行审计 + 逐条读源码复核后确认**没有可被外部输入驱动的 SQL 注入**
  （全仓 `?` 占位符 + `dbx` 统一改写，动态片段只有常量白名单），但补了十个真实缺陷。两个高危：
  ① 私有材质的渲染面——`/avatar/{tid}` 因 tid 可枚举把别人的私有皮肤渲染给任何人，
  `/texture-render/{hash}` 漏得更狠（私有图回 `public, max-age=31536000, immutable`，站到 CDN 后面
  等于把私密皮肤推成全站缓存），两处现在都按「作者本人或管理员」放行并回 `private, no-store`；
  ② 新站初始化窗口内先到者得超管——现在未初始化时启动生成一次性安装令牌打进日志，
  `POST /api/v1/setup` 必须带令牌，占位与建号在同一事务里用 `options(k PRIMARY KEY)` 的
  `INSERT IGNORE` 抢，并发六发只有一发成功、库里只有一行 `permission=2`。
  其余八个：分页 `page` 溢出成负 OFFSET（`dbx.Offset` 统一夹住）、LIKE 通配符不转义（新增
  `dialect.LikeArg/LikeContains/CIContainsSQL` + 一条 AST 门禁禁止再手写 `%q%`）、
  `source_url` 只在 PATCH 带该字段时校验（`source=original` 的行能把 `javascript:` 存进库）、
  全站无安全响应头基线、受控出站的子串路径匹配与 6to4/Teredo、反代 `X-Forwarded-For`
  信任面可用 `SKIN_TRUSTED_PROXIES` 收窄（默认值维持现行为）、错误体与日志回显内部信息、
  插件桥 `postMessage(..., '*')` 把 `_csrf` 发往任意 origin。
- **安全头差点把自家功能打死，被真机抓住两次**：`/plugin-assets/` 上的 `default-src 'none'`
  会让插件页加载不了自己的 JS/CSS（改成只收紧数据/外部源、禁顶层导航与表单）；
  而 `X-Frame-Options: DENY` + `frame-ancestors 'none'` 挂在根上会连**同源** iframe 一起拒，
  站长装完插件点开就是一页空白。现在 `/plugin-assets/` 前缀改 `SAMEORIGIN` + `'self'`，
  其余照旧 DENY。教训写进提交：安全头这种全站中间件，验收必须包含被它约束的那个功能。
- **反人类设计修正（B1–B6）**：皮肤库顶部给「上传我的材质」入口并带 `#upload` 滚动聚焦；
  「我的材质」能搜能筛、空页不再假装材质全丢、新上传的行描边一次；材质详情页对已登录的人不再
  摆一条「注册后可用」，改给「穿到角色」并把选人弹窗接回单页应用；♥ 收藏有了状态文字，
  取消收藏先问一句（它写的是「收进衣柜」）；角色管理与设头像的纯文字下拉换成带缩略图、
  可搜索、分页的弹窗（`/me/wearable` 补 `q/page/pageSize/total`）；**面板「站内材质」从只读列表
  变成能按名称/指纹/上传者搜、能筛类型与处置状态、行上直接下架/撤销判定/彻底删除的工作面**，
  并新开 `POST /textures/{tid}/strike`（站长自己看见违禁内容不该只能等玩家举报，走同一个
  `StrikeTexture` 事务）；任务队列接上取消/恢复；操作审计可查人/事并回传动作清单；
  两处裸 `<input type=file>` 统一成站内按钮样式并在禁用时说明原因；通知/授权/联盟三个视图
  补上自己的 URL，并加一条跨语言门禁保证 SPA 路由表与服务端壳白名单不再各说各话。
- **措辞与错误码**：`hash` 列头改「材质指纹」、「遗留孤儿行」改成说得通的一句话；
  设备码的三条硬编码中文报错进词典并补「回启动器重新获取」；删除一条已不存在的材质不再回
  `player.not_found`（它曾让站长去查角色表）。`A5` 提交自身引入过一个 nil 指针 panic
  （URL 校验写在 `if req.SourceUrl != nil` 之外，任何不带该字段的改名 PATCH 直接崩），
  由同片新写的测试当场抓到并在下一片修掉。
- **本轮刻意不做**：管理端「把别人的材质转公开」与「墓碑视图/解禁按钮」（写操作面扩张且无既有
  路由可复用）；联盟 hub 公钥静默覆盖 pin（拍板只记账不改，保互操作）；严格 CSP（nonce 化会
  打破站长自定义 CSS/JS 与主题/插件 JS）；`.table-scroll` 的横向滚动可发现性与 SSR 落地页语言
  切换（见下轮）；不新增迁移文件，水位仍在 0022。

## 2026-11-03

- **管理面板补三页：材质、操作审计、任务队列**。这三条路由（`GET /api/admin/v1/textures` / `/audit` /
  `/jobs`）从落地起就没有前端消费者，站长只能用 `X-Admin-Token` 打接口。材质页要服务端补 `public` /
  `moderation` / 材质名三列才够用（只有 hash 的列表对站长不可读），公开/私密/已判违规一个徽章分清；
  审计页把 `RecentAudit(limit)` 换成 `AdminAudit(page,size)` 并回传总数——钉死 100 条会让越旧的记录
  静默消失，还不告诉人后面有东西；任务队列页接已有列表接口（插件、状态两个筛选器），队列未挂载时的
  404 分成一句人话。三页只读，照「访问日志」页的既有范式（分页器、固定列宽预算、时间按 DB 墙上时间
  原样显示）。中英双语在本地实例逐页核过。
- **双语门禁加第五条规则：视图引用的键必须真的存在**。`check-locales.mjs` 原来只比两套词典的键集是否
  相等，而「两边都缺同一个键」是完美的平价——vue-i18n 会把 `admin.colName` 直接渲染出来。现在扫描
  `web/src` 的 `.vue`/`.js` 里 `t('a.b')` 字面量与 `` t(`a.b.${…}`) `` 的前缀，缺条目就报出文件与行号。
  它当场抓到三处裸键：新页自己的 `admin.colName` / `admin.colType`，以及**插件管理页早就存在的**
  `admin.state`（那张表的表头一直是原始路径）。
- **真机验收抓出两处界面语义错位**：① 上传表单的「设置为私密材质（仅自己可见）」复选框极性反了——它绑的是
  `uploadPublic` 且默认 `true`，默认勾着却意味着公开，取消勾选才会传成私密，成功提示跟着一起错位。改成
  `uploadPrivate` 默认 `false`、提交时取反、提示按勾选状态选句，并在本地实例真点两遍：不动复选框传出的材质
  `public=true`，勾上才是 `public=false`。② 新页的重载按钮沿用 `admin.load`（「加载」），贴在列表旁边读起来
  像一个加载状态而不是一个动作，另给一条 `admin.reload`（刷新 / Reload），面板原有的令牌按钮不动。
- **文档补四类排错**：SMTP 挂死时面板现在的 12s 结论与新文案、违规内容重传被 403 的真实语义与解禁口径、
  裸二进制工作目录不对导致 `no *.sql found`（`SKIN_MIGRATIONS_DIR` 要给绝对路径的树根）、
  空库上安装向导回 503 的处置。§2.7 那行「站点未配置邮件服务」的引用同步成新文案。
- **面板测试发信不再让站长对着一个网络错误猜**：`mailer.Send` 最坏 10s 拨号 + 30s 会话 = 40s，
  而面板的 `fetchJSON` 20s 就放弃——挂死的 SMTP 让浏览器先报网络错、服务端随后仍可能真发出去。
  现在 `Config.Budget` 是调用方给的总时长（`sessionBudget` 拆成 1/3 拨号窗口 + 整个预算），只有
  测试发信这条路收到 12s，找回密码与通知等其余调用方保持原来的宽窗口；黑掉的 SMTP 现在 12s 内
  给出 `mail.send_failed`。同时给面板单独一条 `mail.admin_not_configured`：原句「站点未配置邮件
  服务，请联系管理员」是说给玩家听的，而读它的人自己就是管理员——同一状态、两个受众、两句话，
  播放器端的文案一个字没动。
- **空库上的安装向导不再把「没建表」说成「昵称已被占用」**：`NeedsSetup` 对「建好表还没人注册」与
  「根本没建表」（裸二进制忘了 `SKIN_AUTO_MIGRATE`）都返回 true，后者以前在第一条 INSERT 才死，
  操作者收到 409 `auth.identifier_taken`。现在先用已有的跨引擎 `TableExists("users")` 探一次，
  缺表就 503 `setup.db_missing` 并给出该执行的那条命令。另外删掉两条从未被发射的码
  （`common.not_found`、`theme.bad_id`，连同中英词典条目）：每个域的 404 都带自己的码，通用码与
  HTTP 面上的主题 id 错误都不存在，留着就是「声明了但走不到」的债。
- **违规内容地址在上传处硬拦，SQLite 不再回收行 id**：两条都是上一轮「材质生命周期」留下的。
  ① 强制删除把 `moderation` 一起抄进墓碑，但上传收口点只读 `textures` 表（同人幂等 + 公共查重），
  而判定违规又会把 `public_dedupe` 置 NULL 让唯一约束失效——所以「违规不能再转公开」只在行还活着时
  成立，同一份字节原样再传就重新入库。现在拦截落在 `WithHashLock` 闭包内、写文件之前，一处覆盖
  web 表单、插件桥、私有导入、launcher-api 四条上传路径；只拦删除时带 `violation` 判定的墓碑，
  普通自删与撤销判定后的删除一律放行。新错误码 `texture.violation_reupload`（403）。
  ② 驱动 ① 的测试连删两次即失败：`migrations/sqlite/0001_init.sql` 的行 id 是裸 rowid，硬删后立刻
  被下一行拿走，于是墓碑主键（就是 `texture_id`）撞 UNIQUE、删除 500，而举报与墓碑都按 `texture_id`
  关联——回收的 id 会把别人的处置历史挂到无辜的新材质上。新装库改 DDL 加 `AUTOINCREMENT`，已装库由
  启动路径做一次幂等重建（单连接事务、行数校验、索引名原样恢复、计数器抬到历史仍引用过的最高 id，
  任一表失败即回滚并停在那里）。重建不落进 SQL 树，所以跨引擎表形门禁与整树重跑门禁都不需要放宽。
  真机（本地 SQLite 实例）实测：行 id 走完 1→2→3 不回退、被判定违规并删除的字节在中文与英文界面各出
  一句人话、上传者本人与其他账号同传都吃 403、干净删除的同一份字节可正常重传。
- **新增 `skin-nexus fsck`**：材质目录与库对账，报孤儿文件、悬空引用、只被墓碑引用的违规文件、
  以及 `AUTOINCREMENT` 之前留下的行 id 错配格；缺省只报不删，`--fix` 仅 unlink 既无行也无墓碑的地址，
  `--deep` 重哈希验内容。错配格只报不修——该留哪一个只有站长说得清。
- **文档有了对外镜像**：仓库内生成的 Repo Wiki 全量同步到 [skin-nexus/wiki](https://github.com/YimingZhanshen/skin-nexus/wiki)，mermaid 图与目录锚点在 wiki 侧照常渲染。同步时把生成产物里的 `file://server/...` 引用改写成 `blob/<生成基准 commit>/...`——相对路径在 wiki 上是死链，跟 `main` 的绝对链接又会在文件挪动后失效，锁基准 commit 才能让每条 `文件:行` 永久解析到它对应的那一版。基准取自 `repowiki-metadata.json`，每条被引路径都要在该 commit 存在才放行（`git cat-file -e` 逐条核，核不到就降级成纯文本路径），本轮 200 条引用零缺失；生成器偶尔会把目录前缀写重（`server/server/internal/...`），这类同样按真实文件树纠正。wiki 是与代码仓库分离的另一个仓库：默认分支 `master`，页名里的空格按 GitHub 规范写成连字符，而且必须先在网页上建出一页才拿得到可推的地址。生成物与同步脚本都留在 `.qoder/` 下，不入仓。
- **修掉三处会把文档再次带偏的源码注释**：`migrations/mysql/0010_password_reset.sql`、`migrations/mysql/0020_email_verify_tokens.sql`、`migrations/postgres/0010_password_reset.sql` 的文件头都写着「TTL 用 DB 时钟 `NOW(3)` / `NOW()` 写入与校验」，而实现是把 `m.now().UTC()` 当参数绑进去（`internal/store/password_reset.go`、`internal/store/email_verify.go`），`NOW(3)`/`DATE_ADD` 还正是可移植性门控（`internal/dialect/portability_gate_test.go`）明令禁用的写法——注释是错的，代码是对的，而生成的文档会照抄注释。同类的还有 `internal/webapi/password.go` 里 `POST /api/v1/password/reset {token, newPassword}` 的 doc 注（真实 tag 是 `password`，`newPassword` 只属于改密端）。三处均只改注释，DDL 与行为未动。

## 2026-11-02

- **文档里的数字改成不靠人记（U18）**：`AGENTS.md` 的迁移范围还停在「0001…0017」，而三套引擎树已经到
  0022；`deploy.md` 两处 prose 写死「46 个进程变量 + 4 个 compose 变量 / 共 50 个变量」，`SKIN_AUTO_MIGRATE`
  那行写死「当前 0001–0021」，§五 写死「0001…0021」。这些数字没有一个门禁看着——`check-docs` 双向比对的是
  **集合**（表里少一行/多一行都会红），所以会漂的只有 prose 里手抄的计数。处置是把数字换成「以目录/本节表
  实际内容为准」并说明为什么这里不写计数，而不是再加一条计数门禁。顺带修一处真矛盾：`migration.md` 的换域名
  步骤第 3 条同时说「option `site_url` 优先于 env」和「纹理 URL 只随 `SKIN_BASE_URL` 变」——两句不可能同时成立，
  真源是**启动时解析一次**（option `site_url` → 老 option `base_url` → env），现在两处口径一致。
  §2.7 排错表补三行真会来问的：皮肤在游戏里不显示（三类原因，与 `/help` 同一口径）、注册/登录被限
  （反代没转发真实 IP 时全站共享网关那一个 IP）、找回密码邮件收不到（指向部署自检与测试发信）。
  新增 §2.8「用宝塔面板管这一套」：面板 Docker 菜单只是 `docker compose` 的一层壳，与 §2.1~§2.5 同一套镜像与卷；
  写清三个只有真装过才会知道的点——面板 Compose 列表不含命令行建的项目（不是故障）、镜像里 healthcheck 只挂在
  db 上所以应用容器只会显示 `Up`、自带 compose 默认 `'8080:8080'` 绑所有网卡而反代场景要改成
  `'127.0.0.1:8080:8080'`（就在该行注释里）。`.env` 无行尾换行时 `echo >> ` 会粘坏上一行也写成了提醒。
  另外确认 `.qoder/repowiki/` 里的同类陈旧说法（0017 / 共 50 个变量）不在版本控制内（`.gitignore` 已排除），
  不需要也不应该跟着改。

- **面板可以真的发一封邮件了（U17）**：`POST /api/admin/v1/mail-test`。**没有 `to` 字段**——一个能指定收件人
  的端点就是架在管理员密码后面的邮件中继，届时的问题从「配置对不对」变成「谁用它发了什么」。收件人恒等于
  调用者自己账号的地址，因此这条路径**必须**是网页会话：`X-Admin-Token` 是机器接法，没有收件箱可言，
  与改权限/装插件同一处置（`admin.web_session_required`）。
  顺序上有意的两件事：先答「本站有没有配邮件」再答「你这个账号有没有地址」——站长问的是前者；
  两者都缺时也只报前者（`mail.not_configured`，503）。发送失败给 502 与 `mail.send_failed`：这条**新加**，
  没有复用 `email.verification_failed`——那句是「验证邮件发送失败，请稍后重试」，对一个只是在测 SMTP 的
  管理员等于承诺「重试就能收到」，而中继问题重试一百次也一样；错误码来源仍在 `i18n/codes.go`，
  于是自动进 check-locales 的双语平价与 errorgate 的常量闸。
  审计与响应都不落明文地址：收件人一律 `r***@example.com` 形式（掩码规则与设置面板的密钥同一精神——
  明文在 users 行里，日志是任何人有面板权限就能翻走的归档）；原始 transport 错误只进日志与审计 detail，
  因为它带着站长自己的 host 和失败原因。
  实测（本地 rig + 一次性 SMTP 收件器）：账号 `root16@example.com`、SMTP 指向 127.0.0.1:2525 →
  收件器抓到 `From: site@rig.example.test` / `To: root16@example.com` / RFC2047 中文主题 / base64 正文，
  面板与 curl 各发一封共两封；端口改到没人监听的 2599 → `502 {"code":"mail.send_failed",...}`，
  浏览器中文给「测试邮件没能发出去，请核对 SMTP 设置并查看服务日志」、英文给对应句，成功行会被清掉不残留；
  带错 token 调 → 403。中英两套 + `role=alert`/`role=status` 各就各位。

- **后台运行状态页多了「部署自检」（U16）**：`GET /api/admin/v1/diagnostics` 不塞进 `/status`——快照
  是可缓存的事实清单，自检读的是可变配置、文件系统和渲染器，两者的缓存与写域都不同。检查项**钉死 6 条**
  （站点地址 / 迁移水位 / 邮件 / 预览渲染 / 前端资源 / 插件签名密钥），少一条就少一个支持问题，多一条就会
  长成一个愿望清单。**服务端只发 id、状态和数据**（路径、水位、host:port、密钥数），句子全在面板词典
  `admin.diag*` / `admin.diagFix.*` 里——和通知 kinds 同一套做法，中文面板不会拿到半英半中的散文。
  两处刻意不做「绿」：探针没注入 → `skip` 而不是 ok；皮肤库空 → `skip/previewNothing`，因为「没东西可渲染」
  不是通过，把假绿写进自检面等于教站长不信任它。
  `site` 一行把四个真源并排吐出（进程解析值 / option `site_url` / option `base_url` / env `SKIN_BASE_URL`），
  因为「到底哪个赢」就是站长问的那个问题；顺带发现一个必须区分的状态：**option 已存成对外地址、进程仍在用
  启动时解析到的局域网地址**（`site_url` 要重启才生效，`siteurl` 只在 boot 写一次）。这一行早期版本直接套
  `siteLan`，等于当场指责站长刚刚做过的那件事，现在给独立的 `siteNeedsRestart`。
  预览探针走真数据：取库里最新一张公开皮肤 → `ts.Open` → `avatargen.LoadSkinReader` → `RenderPreviewAt`，
  因此它抓的是「材质记录在库里、文件不在磁盘上」（数据卷搬家、导入漏文件）这类真会发生的坏法，内置样本永远
  不会失败也就永远测不到。检查目录用 `spaDistDir` 常量与静态处理器共享同一个路径，否则自检指着一个没人服务
  的目录也能报绿。SMTP 判定抽成 `mailer.ConfigFrom`（option 优先 env 的语义不变），webapi 找回密码与
  adminapi 自检从此不可能漂开。
  实测：局域网基线站给 `warn/siteLan`，其余五条 ok；只填 `smtp_host` → `fail/mailNoSender`（面板原先保存成功
  却一封不发的那个坑）；另起实例（自己的库、`SKIN_AUTO_MIGRATE=false`、迁移树比库新）→
  `warn/migrationsPending` 且值写清 `0021_notifications.sql → 0022_closet_backfill.sql`，同一实例在没有
  `web/dist` 的 cwd 启动 → `fail/assetsMissing`；公开皮肤上传前后 preview 由 `skip` 变 `ok`；面板设成
  `https://skin.example.test` 后重启，`site` 从 `warn/siteNeedsRestart` 变 ok。浏览器里中英两套逐项复核，
  18 个 fix key 无一到裸键；连点三次「重新检查」只发一个请求（`diagBusy` 挡住）。
  自检端点认 `X-Admin-Token`（与 `/status` 同档），机器接法也拿得到。

- **安装向导不再把站点地址写进面板看不见的键（U15）**：向导原先写 option `base_url`，而解析顺序是
  `site_url → base_url → SKIN_BASE_URL`——`base_url` 不在面板设置白名单里，于是安装时用了一个局域网 IP，
  它就会长期压住 env，而管理员在面板里既看不到也改不掉（要修得进数据库）。现在向导写面板的 `site_url`，
  校验与面板同规（必须是 http(s) 绝对地址）且**放在建号之前**：一次性门不容许留下「账号建了、地址被拒」的半成品。
  完成页把刚填的地址与 `SKIN_BASE_URL` 并排显示，不一致就红字警告 + 说清后果（材质链接/页面链接/sitemap 都用它）
  + 给出处置（当场改，或面板改完重启）。优先级一字未改：旧库的 `base_url` 照旧生效，`main_test.go`
  的表驱动用例本就钉着这条回退。**新实现场证据**（空库 second rig，env=`https://skin.example.test`）：
  无 scheme 的输入 → `400 common.invalid_value` 且 `needs_setup` 仍为 true（旧二进制同一请求直接 200 写库）；
  填 `http://192.168.1.50:8098` → 响应带 `site_url / env_base_url / mismatch:true`；重启后 `sitemap.xml`
  确实用了该地址（优先级不变）；面板 settings 里出现 `site_url` 行（含 value/default/restart 标记），
  而旧实现写进去的那条在面板里根本查不到。浏览器实测向导四步：地址栏缺 scheme 时前端先拦（留在步骤 1）、
  改对后进步骤 2、第 4 步显示写入结果与不一致警告。
  `deploy.md` 三处同步：`SKIN_BASE_URL` 行说明向导写哪个键、`base_url` 仍夹在中间；§2.4 补完成页警告与
  「改完重启」；§四 补 options 是「先到先得」——老站带来的 `site_url`/`base_url` 会盖住 env，迁移后要核对。

- **玩家帮助中心 `/help`（U14）**：新建 `web/src/pages/HelpPage.vue`，只写本站能答对的部分——皮肤不显示的排查顺序
  （启动器要走外置登录 → 进服名字必须是自己「角色管理」里的角色名 → 服务器要接受本站身份 → 材质地址要从你这边可达 →
  开了邮箱验证门时启动器登录会被拒），接启动器的 HMCL/PCL2 步骤复用个人页既有口径，上传尺寸与实际文件大小上限
  从 `/site-config` 的 `max_upload_kb` 取真值，衣柜语义一句话说清（能穿的就是衣柜里的，移出不再可穿但不删材质），
  名字规则与改密码/找回密码的后果各自一条。**未登录可读**：最卡的人正是还没进门的人。
  接线七处：`App.vue` 的 asyncPage、navIcons、navGroups、viewURLs、legacyRoutes、applyInitialRoute（公开路径与
  `/skinlib` 同等待遇，不再被登录墙挡回去）、模板两个分支；`main.go` 的 `isKnownSPAPath` 加 `help`（不加就是
  硬 404）；`handleSiteConfig` 补 `verify_gate_enabled`（新增 `ygg.Service.VerificationRequired` 转发既有 resolver，
  门开着才显示那一条）。入口两处：登录卡原「忘记密码」那一行改成「帮助中心 / 忘记密码」并列（后者仍按邮件服务
  是否配置出现），落地页 CTA 下加一个进帮助中心的入口。
  一处偏离计划的判断：帮助链接没有塞进默认页脚——页脚是站长可增删的内容，往里注入一条删不掉的站内链接是
  越俎代庖，而玩家真正卡住的地方是登录卡与落地页，就在那两处给入口。
  邮箱验证那句文案改到能站得住：只说「启动器登录被拒（网页登录本身不受影响）」，不说成全站封死。
  实测（浏览器，登录/未登录 + 中英）：侧栏出现「帮助中心/Help」、六个小节全渲染、验证门那条随
  `verify_gate_enabled` 出现（rig 开 `SKIN_REQUIRE_VERIFICATION=true` 后 in=true）、上传那句显示
  「One file may not exceed 1.0 MB」（跟站点旋钮）、`去角色管理` 跳到 `/user/players` 且地址栏同步；
  退出登录后登录卡有「Help」链接，点进去未登录可读；切中文整页中文、`skin_lang` cookie 同步。
  `curl /help` 由 404 变 200；落地页渲染 `href="/help">帮助中心`。词典 804 key、SSR 目录 90 key 双语平价。

- **双语门禁扩到服务端渲染（U13）**：`scripts/check-locales.mjs` 在原有三条（中英键集平价、每个后端码有词、
  不许留已退役码的孤儿词）之外加四条 SSR 检查——`internal/webi18n` 两份目录键集相等且必须是平表、
  SSR 键禁止落在 `errors.` 命名空间（那是错误码的，反向孤儿闸会误杀）、目录里不许有没人引用的死文案、
  被引用的键必须在两边都存在（少一个字母就当着爬虫渲染出键名）。另加一条闸口：扫
  `server/cmd/skin-nexus` 与 `internal/oidc` 里像页面模板的回引字面量，剥掉 `<style>`、`/* */`、
  `<!-- -->` 后不许残留汉字——落地页 CSS 里那批设计说明中文因此不误报，模板里漏一句中文会在
  `文件:行号` 报出来。引用扫描只限这三棵目录（含 webi18n 自己）：扫全仓库会把 `report.resolved`、
  `common.bad_id` 这类**后端错误码**当成页面键索要词条。顺带把 SSR 的 `common.cancel/save` 改名 `ui.*`——
  `common.` 已被错误码占用，一个前缀两种含义正是上面那类误报的源头。
  逐条验证过注入效果：模板里塞「忘了入表」→ 红并给行号；删掉 en 的 `detail.download` → 平价与引用两条同时红；
  `ui.cancel` 打成 `ui.cancl` → 红；删掉 `ui.save` 的引用换成硬写「保存」→ 死文案 + 残留中文两条一起红；
  全部还原后重新全绿（当前 SSR 目录 89 key 中英平价）。`docs/build.md` 前端一节补上这道闸的实际口径。

- **落地页与设备授权页接上同一套语言机制（U12）**：`landing.go` 的 tag、四个 CTA、三张统计卡、
  「皮肤库精选」与进库按钮、以及当 SEO description 用的那句话简介全部入表；
  `oidc/provider.go` 的无 JS 设备授权页（启动器在跑不动 SPA 时打开的就是这一页）同样入表——
  它的标签是以 JS 字符串送进 `const L = {...}` 的，`html/template` 在 JS 上下文里会自动加引号转义，
  测试 `TestDeviceFormLabelsAreValidJS` 钉住这一点：漏引号会让整页卡在「加载中…」而不报任何错。
  `landingIntro` 那条 Go 常量随之删除（否则它是第二份真源）。顺带修一个既有文案缺陷：
  通用 404 的 `<title>` 借用「材质不存在」——未知 URL 现在给「页面不存在 - 站点名 / Page not found」，
  标题键与正文键分开。
  实测（curl，落地页/设备页/404）：英文落地页 `<html lang="en">` + "Registered users"/"From the skin library"/
  "Create an account"，meta description 换成英文那句；中文落地页逐字照旧；
  `device.html` 英文给 "Device authorization"/"Loading…"/`done: "Done — you can return…"`，
  中文仍给 设备授权/加载中…/同意/拒绝/用户码：；`/nope-xyz` 英文标题 `Page not found - Rig U1`
  正文 `This page does not exist.`。目录 89 key 中英平价；新增 9 个渲染测试（中英各一遍 + 无语言兜底），
  中文侧的断言一字未改，构成「渲染逐字不变」的回归证据。
  演示用本地 rig 起 OIDC 需要注意：issuer 必须是 https（`oidc: scheme for issuer must be 'https'`
  会让启动直接失败），本地验证要显式给 `SKIN_OIDC_ISSUER=https://…`。

- **服务端渲染的页面有了第二种语言**：新建 `internal/webi18n`（两份目录 JSON + `T/F` + `ResolveLang`），
  皮肤详情页 `/skinlib/{tid}` 与它的 404 页整体入表——模板 40 余条中文、Go 里的类型词/来源词/
  兜底名称/SEO 标题与摘要全部走查表，`<html lang>` 随语言变。没有放进 `internal/i18n`：那个包自陈是
  leaf（只有码、无查表、还有 go/ast 读它自己的测试），加数据表会破坏它声明的性质。
  语言判定顺序 = `skin_lang` cookie → `Accept-Language`（按 q 值排序、前缀匹配 `en*`/`zh*`）→ `zh-CN`；
  cookie 里出现我们不发的值当没写过，继续读 header，免得一个手改的值把语言钉死。
  SPA 侧 `setLocale` 与启动时把当前语言镜像进 cookie（显示偏好，非机密：不 HttpOnly、`SameSite=Lax`
  好让启动器顶层跳 `/device.html` 也带着、https 下才 Secure、一年有效对齐 localStorage），
  否则「在应用里切了英文、每个可被抓取的页面还是中文」。
  `viewer-src.js` 的 7 条运行时文案不再硬写中文，改读 SSR 注入的 `window.__SKIN_I18N__`——
  注入值类型是 `template.JS`：在 `<script>` 里 html/template 会把别的类型当字符串值加引号转义，
  页面拿到的就是一个字符串而不是对象。目录 key 一律不带 `errors.` 前缀（那是错误码的命名空间，
  `check-locales` 的反向孤儿闸会把没有对应码的 `errors.*` 判 fail）。
  实测：`Accept-Language: en` → `<html lang="en">` + "Download the PNG"/"Texture ID"/"uploaded by"，
  注入 `__SKIN_I18N__={"lang":"en"…}`；无 hint → 中文照旧；`Cookie: skin_lang=zh-CN` 压过 `en` header；
  英文 404 页给 "Texture not found - Rig U1 / This texture does not exist…"；
  剥掉 `<style>` 与注释后英文页面零残留中文。浏览器里 SPA 切英文 → cookie 由 `zh-CN` 变 `en` →
  重新加载详情页整页英文，♥ 取消收藏再收藏都正常（计数 1↔0，无错误提示），canvas 照常升级。
  回归证据：详情页原有测试钉的是中文原文（`下载材质 PNG`/`材质 ID`/`7 人收藏`/`Alex 瘦臂`），
  入表后不改断言仍然通过，说明中文渲染逐字未变。新增测试：`webi18n`（查表兜底、占位符、
  九种语言判定组合、目录平价、注入载荷形状与 `</script>` 逃逸）+ 英文渲染与 CJK 残留闸。

- **管理端「角色管理」四条路由从来没通过（真 bug）**：路由绑的是 `{pid}`，handler 用的是读 `"id"` 的
  `idParam`——Go 的 ServeMux 不会发现这种错配，`PathValue` 一律答空串，于是改名、换主、换肤、删除
  对任何调用方都返回 `400 common.bad_id`。本轮改换肤链路时用 curl 打管理端才撞见：修复前四条全 `400 bad_id`，
  修复后 `rename 200 / owner 200 / delete 9999 → 404 player.not_found`。
  补一道 AST 闸 `routeparams_test.go`：本包路由声明的每个 `{param}` 必须至少被一处 `PathValue` 读到，
  反向也查（读了没人绑的名字同样永远是空串）——这一类缺陷只会静默上线，散文提醒挡不住。
  顺手补上同一处缺的校验：管理端换肤原先不比对材质类型与槽位，披风能挂进皮肤槽（启动器拿到的是画坏的外观，
  这不是策展选择），现与用户侧同规 `422 texture.type_mismatch`；存在性检查从 `TextureOwner` 换成
  `TextureType`，同一次查询两用。测试覆盖为真机 curl（管理端集成测试仍缺会话桩），已登记。

- **衣柜就是能穿的那一堆（塌缩）+ 叫法统一成一个词**：换肤此前有两条并行的准入线——
  「我自己上传的」∪「我收进衣柜的」，于是同一个东西在界面上一会儿叫纹理、一会儿叫材质，
  「改名」在三个地方是三件事。现在只剩一条线：`SetTexture` 删掉 owner 支路、
  `WearableTextures` 只查 `user_closet`，新增 `0022_closet_backfill.sql`（三套树同步，
  反连接 `LEFT JOIN ... WHERE c.id IS NULL` + 只给仍在册的账号补行）把历史上没入过柜的自上传补进柜，
  与「上传即自动入柜」在同一次发布里落地——已发布过的站点不存在「自己上传却不在柜里」的中间态，
  因此回填不会复活谁主动移出的东西；代价是语义收紧：**自己的上传一旦被移出衣柜就不能再穿**（与 blessing 一致）。
  迁移约定同步改写：数据语句只在「重跑必然是 no-op」时允许进树，否则留在 Go 里（`tracked.go` / `0012` 注释同改）。
  叫法往 blessing 收：纹理→材质（SPA 12 处 + 详情页「下载材质 PNG」「收藏 N 人」），♥ 从「喜欢」改「收藏」
  （`skinlib.likeTip`/`sortLikes`/SSR 按钮与 SEO 摘要同改），一个「改名」拆成改备注名（衣柜）/ 改材质名（皮肤库）/ 改角色名（角色页），
  角色名旁边写清「就是你在游戏里显示的名字」并预写 3–16 位规则、昵称预写 2–24 位规则（原先 label 只说「MC 风格」），
  衣柜空态那句自相矛盾的「先上传再入柜」改成事实句并挂上两个跳转按钮，`EmptyState` 的 `hint`+插槽在衣柜/我的材质/皮肤库三处用起来
  （此前 9 个调用点一个都没用），设备授权页「没有可选角色」给出「去角色管理建一个」而不是让人自己找，
  披风在三处标注「展开图，穿上效果见角色管理」，死 key `dashboard.closetAdded` 删除。
  顺手补一个真洞：封禁（`permission = -1`）原先只挡启动器和后台，**Web 登录照进、照样能往公共皮肤库发东西**；
  现在登录阶段直接 `403 auth.banned`（新码，中英各一条），封禁落定时一并清空该账号的 `web_sessions`。
  计划里的「封禁横幅」因此不需要了——会话不再带着 -1 停在页面上。
  实测：本地 rig 用旧二进制先移出衣柜条目再换肤得 `200`（owner 支路），换新二进制重启后同一条 `PUT /me/players/1/skin`
  得 `403 {"code":"texture.not_owned","message":"this texture is not in your closet"}`，且 `/me/wearable` 不再列出它；
  重启本身就在真实库上跑了 0022（重启前手动移出的那条被补回，符合「未入柜的自上传」定义，条目与别名不重复）；
  封禁 bob 后原会话 `GET /me` 由 200 变 401、登录得 `403 auth.banned`，解除后恢复 200，
  站内通知里 `account.banned`/`account.unbanned` 两条都在。浏览器双语走查：注册卡昵称规则、角色页规则行、
  衣柜三种改名按钮、披风 tab 的展开图说明、皮肤库「改材质名/收藏（加入衣柜）」逐项读到。
  词典 775 key 中英平价、错误码目录 110 条。

- **降低上手门槛第二轮：弹窗、待决态、可达性与反馈一致性**：SPA 里 42 处原生对话框
  （7 prompt + 34 confirm + 1 alert）全部改为应用内 `<dialog>`（`DialogPanel` 一个外壳：顶层渲染、
  焦点陷阱、Tab 成环、Escape 关闭、关闭后焦点回触发元素），样式沿用服务端详情页
  `.report-dialog` 的令牌；`dialog.js` 提供 `promptDialog/confirmDialog/alertDialog`，返回值语义与原生一致。
  这不只是好看：沙箱插件 iframe 没有 `allow-modals`，子页确认只能委托宿主，而宿主此前用
  `window.confirm` —— 后台标签页里浏览器直接拒绝、返回 false，插件会把「弹不出来」当成「用户拒绝」。
  衣柜与角色页补齐待决态（按行 `busy` + `:disabled` + 首次骨架 + `reqSeq` 序号守卫）：
  此前改名/移出/设为头像/换肤在请求飞行中可连点，tab/搜索/分页三种触发还可能让慢响应盖掉新结果。
  可达性与一致性一次收口：58 处错误/成功横幅加 `role="alert"`/`role="status"`（此前全站只有 4 个
  live region）；上传拖放区旁边给出真正的 `<button type=button>选择文件`（原 zone 只能鼠标点击，
  且包内已有一个按钮，不能再套 `role=button`）；`--danger-ink: #ef7a70` 专供面板上的红字
  （`--danger` 落在 `--panel` 上实测 4.21:1 不达 AA，新令牌 6.01:1，取值走
  `var(--danger-ink, var(--danger))` 回退，不给已有主题包带来回归）；粗指针下
  `button.small/ghost`、`.like-btn`、`.toast-x` 恢复 44px 命中区（scoped 属性选择器会压过全局
  `button` 规则，故在组件内补）；字节格式化两处合一、日期改按界面语言（`Intl` + `i18n.global.locale`）；
  预览图 URL 与举报缩略图不再由前端硬写 `?v=5`，改由服务端下发；`index.html` 启动占位、
  页脚/面包屑 `aria-label`、预览 `alt`、「密」徽章文字进词典。
  真机实测：衣柜改名弹窗焦点落在输入框、确认后行内显示新备注名、按 Escape 不写库；
  把 fetch 人为拖慢 600 ms 可观测到飞行中该行四个按钮全部 disabled、落地后全部恢复；
  `/reset-password` 无令牌页的错误条 `role=alert` 且计算色为 `rgb(239,122,112)`；
  选文件按钮可 Tab 聚焦，用真实文件走完上传（命中重复图，返回「你已经上传过这张图」）；
  上传提示显示「单个文件不超过 1.0 MB」、用量显示「1.2 MB」、缩略图为 `/preview/<tid>?v=5&small=1`。
  词典 764 key 中英平价，错误码目录 109 条。

- **上传限额理顺 + 失败原因分码 + 登录锁定倒计时（降低上手门槛第一轮）**：
  `maxBody` 把整个 `/api/v1` 的请求体一律截到 1 MiB，而纹理上传允许 2 MiB、像素上限 1M——
  1 MiB 以上的皮肤 PNG 在 multipart 解析阶段就被截，玩家读到的是「请求内容无法解析」，
  管理员把 `max_upload_file_size` 调到 2048 也完全无效（闸在传输层，先到先杀）。
  现在照管理端已有的做法给上传路由单独放行，并由 handler 自己收口（文件硬顶 + multipart 帧余量），
  其余 JSON 路由的 1 MiB 防线不变；`/site-config` 新增 `max_upload_kb`，上传表单直接报出实际生效的
  上限并在本地先拦一次。实测（HEAD 二进制为对照）：同一张 1,230,047 B 的 640×640 皮肤，
  修复前 `400 common.bad_body`、把旋钮调到 2048 仍是 `400`；修复后 `201 created`；
  旋钮降回 1024 时 1,983,432 B 的文件得到 `413 texture.too_large`；
  3,148,217 B 触发新的体积闸，同样是 `413 texture.too_large`（不再是 bad_body）；
  2 MB 的 JSON 打 `/login` 依旧 `400 common.bad_body`。
  失败原因不再四因一果：尺寸不合法 / 像素炸弹 / 元数据填错 / 非 PNG 原先共用 `texture.invalid`，
  现在拆出 `texture.dimensions_invalid`（宽高作为字段下发，词典直接插值进句子）、
  `texture.pixel_bomb`、`texture.metadata_invalid`，`texture.invalid` 回归「不是 PNG」本义；
  `texture.model_invalid`/`source_invalid` 的所属权从 `i18n` 移到 `account`——状态表在 account，
  查不到的码会兜底 500，等于把玩家笔误算成服务器故障。注册冲突同样拆开：原先一律报
  「昵称或邮箱已被占用」，现在分别报 `auth.nickname_taken` / `auth.email_taken`，
  且非冲突的建号失败不再被误报成重名。
  前端三条：登录失败锁定的 429 现在带 `retry_after_seconds`（响应头之外再放一份到 body——
  页面的 fetch 层拿不到响应头），登录卡按截止时间戳渲染「请 N 秒后再试」并把按钮置灰，
  四张认证卡统一加 in-flight 守卫防连点；传输超时不再写死中文，改走词典
  `errors.networkTimeout`（文案同步改成与上传无关的「请求超时」）。
  实测：连续 5 次错密码后第 6 次得 `429`，界面显示「尝试次数已达上限，请 681 秒后再试」且
  `disabled=true`、期间零请求；切英文立即变「Too many attempts — try again in 680 seconds」；
  人为注入 AbortError 后中/英分别显示「请求超时，请重试」/「The request timed out, please try again」。
  错误码目录 109 条 × 双语，界面文案 748 key，中英平价。

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
