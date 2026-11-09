# 兼容矩阵 —— blessing / yggdrasil-connect 1:1 复刻进度

> 依据：2026-10-23 对线上 legacy 站点全量只读实测（报文留档于维护者本地，不随仓库分发）
> 与上游源码级审计。图例：✅ 达成 / 🔧 实测后修复 / ⚠️ 缺口 / ➖ 不复刻。

## 1. Yggdrasil 面（authlib-injector 硬契约）

| 端点 | legacy 实测行为 | SkinNexus | 状态 |
|---|---|---|---|
| `GET /api/yggdrasil` 与 `/api/yggdrasil/` | meta + `feature.non_email_login` + `feature.openid_configuration_url` + skinDomains + PEM 公钥 | 同形；skinDomains 按旧站公式（配置 ∪ Union 成员站 ∪ site_url host ∪ 请求 host，2026-10-23 对齐）。**两种拼写各自注册**：不带斜杠才是规范地址（`X-Authlib-Injector-API-Location` 广告的就是它，启动器配置框里粘贴的也是它），而 Go 的 ServeMux 对 `{$}` 模式的不带斜杠形式会自己回一个 307——那条重定向上没有任何处理器头，跨源读它的浏览器直接按 CORS 失败处理。2026-11-09 补 | 🔧 |
| `authserver/authenticate` | 单角色自动绑定 `selectedProfile`；token 为 JWT | 同形 | 🔧 |
| `authserver/validate` / `invalidate` / `signout` | 恒 204 | 同 | ✅ |
| `authserver/refresh` | 旧 token 立废；支持 `selectedProfile` 选角 | 同（选角解析修复于 2026-10-23） | 🔧 |
| `sessionserver/join` | `selectedProfile` 为**裸 UUID 字符串**；强制 token↔角色绑定（403 中文错误体） | 双形态容错（字符串/对象、32hex/连字符）+ 同款错误面 | 🔧 |
| `sessionserver/hasJoined` | 未命中恒 204；命中出签名 profile | 同（含 IP 比对 fork 语义） | ✅ |
| `sessionserver/profile/{uuid}` | 签名矩阵 | 同 | ✅ |
| `api/profiles/minecraft`（POST）/ `api/users/profiles/minecraft/{name}` | 200 / 未命中 204 | 同 | ✅ |
| `api/profiles/minecraft/{name}` 别名、`minecraftservices/publickeys` | 旧站 404 | 同样 404（平价） | ➖ |

JWT access token 实测形状：`aud="2"（shared_client_id）、sub=uid、scopes[Select,Server.Join]、selectedProfile=<uuid 字符串>、jti、无 iss`。

## 2. yggc / OIDC 免密面（SJMCL 等社区启动器契约）

| 环节 | 契约要点 | 状态 |
|---|---|---|
| 发现 | 启动器读 meta `feature.openid_configuration_url` → discovery 取 `shared_client_id`+端点；两者非空才出「OAuth 登录」按钮 | ✅ |
| discovery | shared_client_id、device/token/jwks 端点、RS256、S256、claims 面 | ✅ |
| device 流 | RFC 8628 纯净流、`/device` 页、`authorization_pending/slow_down` | ✅ |
| id_token | RS256 + JWKS keys[0] + aud + `selectedProfile{id,name}`（对象） | ✅ |
| userinfo | GET+Bearer、`sub`=uid 字符串、scope 分面 claims、401 `Bearer realm="openid"` | ✅ |
| token 贯通 | OAuth JWT 直通 ygg join/validate（scope 门 + jti 台账） | ✅ |
| discovery 挂载 | 根路径 + `/api/yggdrasil/.well-known/...`（启动器截断启发式）双挂载 | 🔧 |

## 3. 兼容面

| 端点 | legacy 实测 | SkinNexus | 状态 |
|---|---|---|---|
| `GET /{player}.json`（CustomSkinAPI R1） | 200 | 同形 | ✅ |
| `GET /usm/{player}.json`、`/skin\|cape/{player}.png` | 旧站 404（插件未装） | 已实现（超集） | ✅ |
| 联盟 hub 的浏览器端成员站取名 | hub 页面用访客的浏览器跨源读 `bs_root + /api/yggdrasil` 的 `meta.serverName`，再读 `/api/union/member` 的 `enabledFeatures` 决定按钮能否用；两条 fetch 共用一个 catch，任一条挂掉成员站就显示成「✕ 代号」且禁用 | 两个端点都要在**不带斜杠**的规范地址上直接返回 200 并带**恰好一个** `Access-Control-Allow-Origin`。hub 侧的成员清单里 53 个站都**不存** `name` 字段，名称全靠这条跨源读取回填，所以它坏了等于站名在联盟里消失。2026-11-09 修前半（`{$}` 的裸 307 不带 CORS）。后半同日实测：反代若整块 `add_header Access-Control-Allow-Origin *`，同一个响应就出现两个值，浏览器连 200 一起丢弃（控制台 `multiple values '*, *'` + `net::ERR_FAILED`），站名同样是空——`/api/union/member/oauth2/` 那条带凭据的更是重复值与 `*` 双踩。真凶是**宝塔网站防火墙的「跨域设置」**（它只在请求带 `Origin` 时注入，`nginx -T` 里搜不到），关掉后计数变成 `1/1/1/0`、hub 当场回填站名。修法与验收见 [`deploy.md`](deploy.md) 的「CORS 头」小节。hub 上 ECNU 与 nekoplay 至今仍是这个坏法 | ✅ |
| 成员站列表里的「站名」列（本站页面） | 插件让**访客浏览器**逐个读成员站 `/api/yggdrasil` 取 `meta.serverName` 回填（yggdrasil-connect servers.twig:22-33），读到什么显示什么，读不到就空着 | 本站的 CSP 是 `connect-src 'self'`，那种跨源读在离开浏览器前就被拦死，所以改由**本站后台**读：`union.MemberNames` 复用插件/主题市场那套受控出站（只连公网地址、拒 IP 字面量、禁重定向、只放 `80/443`、限响应体、每主限速），URL 只取清单里的 scheme + host，路径固定为我们自己的 `/api/yggdrasil`。名字后台补（24h 正缓存、30min 负缓存、同一时刻只跑一轮），打开页面那一瞬不等待，所以首屏可能只显示地址，下一次就有名字。地址行始终保留。不想让服务器外呼的站长可关：面板 `union_resolve_names` 或 `SKIN_UNION_RESOLVE_NAMES=false`，关掉后连已读到的名字也不再显示 | ✅ |
| 联盟安全级别（SL） | `POST {hub}/code` 用成员钥匙换本站代号，再 `GET {hub}/backend/{code}/security/level`，响应体是裸数字；玩家页每次看页都跑两跳、不缓存，失败 `abort(500)` 而前端不查 `response.ok`，徽章于是空白（UnionController.php:30-42 + union.twig:177-190） | `GET /api/v1/union/security-level` 走同样两跳，`SL3` 绿、其余黄。差别在：结果缓存 10 分钟（失败 1 分钟后重试），上游失败明确报 502 并在页上写「暂时读不到」——`SL0` 是一个真实结论（未接入联合黑名单），拿它当兜底会冤枉站长 | ✅ |
| 角色改名 / 玩家自删的 hub 通告 | 核心事件 `PlayerProfileUpdated`、`PlayerWillBeDeleted` 各发一次 `PUT /profile/{uuid}`、`DELETE /profile/{uuid}`（bootstrap.php:97-119），失败只写日志；插件与核心都没有任何定时对账 | 同样三条腿（建 / 改名 / 删）都由 `account.Service` 通告，覆盖玩家自服务、管理面板直连存储、以及插件经账号桥发起的改名；uuid 不重算（改名零漂移铁律）。失败写审计 `union_sync_{create,rename,delete}_failed`，面板「同步角色数据」可手动收敛 | ✅ |
| 联盟自助诊断（管理面板） | 管理员点一次 `POST {hub}/diagnose`（10s），hub 把它**回探本站**那一跳的证据原样带回：`healthy`、`delay`、`timestamp`、`tls_handshake`（GnuTLS 调试轨迹全文）、`status_code`、`headers`、`body`。弹窗把这些**平铺**成一张表，只有成功/失败两态，导出是一份硬编码中文的 HTML（config.twig:115-159） | 同一跳、同一形状（wire 未动，`POST /diagnose` + 空 JSON 体 + 成员钥匙头都有测试钉住）。差别在三处，都是刻意的：① **按腿分家**——`status` 为 ok 时那四个键描述的是 hub 回探本站，为 error 时描述的是 hub 对我们这一跳的答复；平铺读会产出「双向连通正常」下面挂着 `状态码 200` 与一段响应头这种自相矛盾的表（老插件即如此）。② **本站那一跳由本站自己观测**：TLS 版本与套件、对端证书到期日与剩余天数、以及 hub `Date` 头与本地钟的偏差，判据直接复用入站验签的 `VerifyPastSkew`/`VerifyFutureSkew`，所以面板不可能再说「时钟没问题」而隔壁正在拒收 hub 回调。③ **三态**（通过/注意/失败）+ 失败原因给一个稳定 token（`http_key` 与 `dns` 的修法完全不同，老插件只留一句 exception 文本），GnuTLS 全文折进「原始内容」，摘要只留版本、最后保留的套件与断言行数。导出多一份机器可读 JSON（解释过的腿 + hub 原样载荷） | ✅ |
| `enabledFeatures` 能力声明 | hello 里按 `option('require_verification')` 与 `option('invitation_codes_for_union_enabled')` 追加两项；后者由邀请码插件的启用/停用直接写 true/false（MUAlliance/invitation-codes callbacks.php:23-28），语义就是「本站注册要不要邀请码」 | `unionBlacklist` 一直有；`emailVerification`、`invitationCodesForUnion`、`unionOAuth2` 现按请求读 `require_verification` / `invite_required` / `union_enable_oauth2`，与老站一样改完即生效，不缓存成启动时的快照 | ✅ |

## 4. 取舍与偏差（blessing 1:1 口径）

兼容主干——Yggdrasil 全量、OIDC/yggc、CustomSkinAPI、USM、邀请码、衣柜、改密与吊销、
找回密码、认证限流、迁移导入器、材质举报与彻底删除——已逐项达成，落地时间与实现细节见
[`CHANGELOG.md`](CHANGELOG.md)。本节记录与老站的**刻意差异**与**已知偏差**，不再复述完工历史。

| 项 | 状态 | 说明 |
|---|---|---|
| 邮箱验证门 | 🔧 语义 | `email_verified` 字段、发信验证链路与 join 门禁就位（面板 `require_verification`，默认关；`email_verify_grace_days` 给新号宽限期）。老站迁移过来的账号仍只有管理员代设标志一途——迁移不带验证状态 |
| `uploadableTextures` 按能力位裁剪 | 🔧 取舍 | 本站恒答 `skin,cape`（`server/internal/ygg/models.go:103`），且与事实一致——上传不按类型设限（`type` 只校验 `skin\|cape`）。老站可按用户能力逐位裁剪，前提是有按位能力位；本站 `permission` 是整型档位（0 普通 / >0 管理员 / -1 封禁，`server/internal/store/web.go:151`），要做「站长关掉披风上传」得先重做权限模型，不属兼容缺口 |
| 纹理广场 · OAuth 社交登录 · API PAT 面 · 积分商城 · 2FA · 验证码 · `/api` 根装饰 | ➖ 超出兼容范围 | 老站的增强面，不在「协议与数据可直迁」的口径内（站内通知另见下） |
| 站内通知 | 🔧 语义 | 老站是逐用户站内信 + 永不过期的链接；我方是**处置决定的留痕收件箱**（举报成立/驳回、材质下架与解除违规、封禁与解禁），90 天保留、拉取式未读角标，不做评论/@ 等社交流 |
| 删除材质后已下发的纹理 URL | 🔧 取舍 | `/textures/{hash}` 只查磁盘、不校验行与可见性，且响应是 `max-age=31536000, immutable`。删除保证**源站停止供源**并保留取证记录，不保证浏览器/中间缓存/CDN 上的旧副本失效（hash 即 URL，无法主动失效）。加每请求 DB 校验会推翻纹理分发的性能模型，故不做 |
| 私有材质的渲染面 | 🔧 偏差备案（2026-11-04 起） | 老站的头像/渲染路由不查可见性，`/avatar/{tid}` 与详情页 WebGL 用的 `/texture-render/{hash}` 谁都能拿（tid 可枚举）。我方现在按「作者本人或管理员」放行，私有渲染回 `private, no-store` + `Vary: Cookie` + `X-Robots-Tag: noindex`。**行为变化要记账**：以前发出去的私有头像/渲染直链现在对匿名访客是 404，被 CDN 缓存过的旧副本仍会存活到过期（与上面「删除材质后已下发的纹理 URL」同一局限）。按内容地址直取原始文件的 `/textures/{hash}` 与 `/avatar/{hash}` 不变——那是能力地址模型，不是可枚举入口 |
| 新站初始化 | 🔧 偏差备案（2026-11-04 起） | 老站空库直接开注册，谁先到谁当管理员。我方未初始化时启动生成一次性安装令牌打进日志，`POST /api/v1/setup` 必须带它（`SKIN_SETUP_TOKEN` 可固定），占位与建号同事务、并发只有一个超管。装好之后端点永久 404，向导是一扇门不是开关 |
| 全站安全响应头 | 🔧 取舍（2026-11-04 起） | 页面与 API 统一带 CSP/`X-Frame-Options: DENY`/`Referrer-Policy`/`Permissions-Policy`。CSP 刻意允许 `script/style 'unsafe-inline'`：站长自定义 CSS/JS、主题包、插件包都是这个能力的使用者，nonce 化等于砍掉三项功能。`/plugin-assets/` 是唯一允许被本站 iframe 的前缀（`SAMEORIGIN` + `frame-ancestors 'self'`），其余一律拒绝被嵌；插件/主题 HTML 文档另带 `Sandbox: allow-scripts allow-forms`，落到不透明源里拿不到 cookie。反代在站点前时以站点自身响应为准，覆盖需反代显式改写 |
| 违规内容地址的解禁面 | 🔧 取舍 | 判定违规（`moderation='violation'`）后彻底删除的材质，其内容地址进墓碑并被上传收口点拒绝，任何人都传不回来，私有导入也一样（私有副本可穿，放行等于留后门）。**撤销误判只有数据侧一途**：面板不提供墓碑视图，因为解禁必须指明是谁撤销了谁的判定，而那需要整套处置历史界面而非一个按钮。老站没有等价物（它根本没有彻底删除） |
| 行 id 不复用 | 🔧 偏差备案 | `textures/users/players` 的行 id 会被举报、墓碑、衣柜长期按 id 引用，回收即错配。SQLite 靠 `AUTOINCREMENT`（新装走 DDL，已装库在启动时幂等重建）；MySQL `AUTO_INCREMENT`、PostgreSQL `BIGSERIAL` 本就不回退。**MariaDB 的 InnoDB 计数器不持久化**，重启后按 `MAX(id)+1` 重算，理论上仍可回收——本机无 docker，未复现，只备案 |
| 「换肤必须先入柜」 | ➖ 不复刻 | 拍板：那是老站 UI 流程而非数据约束，强加会破坏已验收的 setSkin 直连；衣柜按收藏集合上线 |
| 认证限流返回 429 | 🔧 偏差备案 | legacy 契约从未定义限流；只追加 TooManyRequests，冻结的错误体形状不变，阈值面板可调 |
| 老站 `password` 列为空的账号 | 🔧 偏差备案（2026-11-07 起） | 老站的摘要比较里，「未计算的 method」返回空串，碰上 `password` 列也是空串时**相等即通过**——空密码列等于任何密码都能登录。本站现在一律先拒空摘要（`server/internal/auth/password.go` 的 `verifyLegacyOne`，断言在 `server/internal/auth/empty_stored_test.go`）。**行为变化要记账**：`password` 列为空的迁移行不再能用任何密码登录，需走找回密码或由管理员代设；这与「空密码可登录」的老站行为是刻意偏差，那条行为本身是个洞不是兼容面 |
| `skinDomains` 带端口条目 | 🔧 偏差备案（2026-11-09 起） | authlib-injector 一类客户端把纹理主机与 `URI.getHost()` 比对，而 `getHost()` **不含端口**，所以列表里 `site.invalid:8443` 这种条目永远匹配不上——账号能登、皮肤静默不加载。本站现在为每个带显式端口的条目**同时发布去端口孪生**（`server/internal/ygg/http.go` 的 `portlessTwin`，测试 `skindomains_test.go`）。**只增不改**：已发布的条目一字不动、位置不变（孪生追加在快照之后），无端口的部署输出一模一样；能看见差异的是 `skinDomains` 数组变长这一件事 |
| `uploadableTextures` 属性的签名 | 🔧 已知偏差（取证 2026-11-07） | 本站每条 property 都签（`textures` 与 `signatures.textures` 成对），**唯独 `uploadableTextures` 不签**——值恒为 `skin,cape`，与本站不按类型设限的上传策略一致，所以它不携带任何需要证明的授权信息。参考实现的两侧（yggdrasil-connect `Profile.php:90-96`、AsterYggdrasil `properties.rs:66-71`）是逐条签所有 property。要不要补齐到"每条都签"，属客户端可见的 wire 面变更：先要确认现网启动器在校验签名集时对**多出来的签名**怎么处理，不在无人值守里改。出处：仓库外 `analysis/g10-e3-protocol-surface.md` |
| `api/profiles/minecraft/{name}` 别名、`minecraftservices/publickeys` | ➖ 不复刻 | 旧站自身 404，平价即可 |
