# 变更日志

> 迭代历史（按日期倒序，每天一条，只记要点）。开发细节见对应 commit；README 状态行只留一句话摘要。

## 2026-11-01

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

## 2026-10-28

- **插件市场 P2 Core**：单源签名 registry（Ed25519）+ SHA-256 + 签名 bundle、staging 解压/zip-slip/压缩比防护、原子替换、升级回滚、卸载保留/purge、依赖/冲突与能力 consent、后台市场页与本地上传；新增 0013 安装事务表；修复 `DeletePluginData` 列名与 `.state` 扫描问题。
- **API 收敛**：项目尚无存量生态，插件只支持 `hookApi: 2`；删除 v1 兼容分支、未使用的 `profiles:write`/`admin:menu` 与死字段，示例/文档同步迁移。

## 2026-10-28

- **插件平台 v2（P0+P1）**：manifest v2（config/store/routes/ui/events/guards）、JSON Schema 配置 + AES-GCM 密钥、`0012` 插件平台迁移（meta/secrets/kv/docs/migrations）、public/session/admin/superadmin 三档路由（身份注入/CSRF/限流/body 上限/访问日志）、命名空间 KV/集合与宿主托管迁移、通用事件目录与 fail-closed guard（含超管破窗）、sandbox iframe UI 页面/固定 slot/`/plugin-assets`/`/ext` 深链、插件健康/路由/日志指标；当时保留 v1 `hookApi: 1` 兼容（已于 2026-10-28 P2 Core 收敛时移除）；新增 4 个 v2 离线示例与规范重写。
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
