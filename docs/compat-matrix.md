# 兼容矩阵 —— blessing / yggdrasil-connect 1:1 复刻进度

> 依据：2026-10-23 对线上 legacy 站点全量只读实测（报文留档于维护者本地，不随仓库分发）
> 与上游源码级审计。图例：✅ 达成 / 🔧 实测后修复 / ⚠️ 缺口 / ➖ 不复刻。

## 1. Yggdrasil 面（authlib-injector 硬契约）

| 端点 | legacy 实测行为 | SkinNexus | 状态 |
|---|---|---|---|
| `GET /api/yggdrasil/` | meta + `feature.non_email_login` + `feature.openid_configuration_url` + skinDomains + PEM 公钥 | 同形；skinDomains 按旧站公式（配置 ∪ Union 成员站 ∪ site_url host ∪ 请求 host，2026-10-23 对齐） | 🔧 |
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
| 「换肤必须先入柜」 | ➖ 不复刻 | 拍板：那是老站 UI 流程而非数据约束，强加会破坏已验收的 setSkin 直连；衣柜按收藏集合上线 |
| 认证限流返回 429 | 🔧 偏差备案 | legacy 契约从未定义限流；只追加 TooManyRequests，冻结的错误体形状不变，阈值面板可调 |
| `api/profiles/minecraft/{name}` 别名、`minecraftservices/publickeys` | ➖ 不复刻 | 旧站自身 404，平价即可 |
