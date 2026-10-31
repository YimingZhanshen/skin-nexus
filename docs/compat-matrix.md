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
| `api/profiles/minecraft/{name}` 别名、`minecraftservices/publickeys` | 旧站 404 | 未实现（平价；超集待议） | ➖ |

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
| `GET /usm/{player}.json`、`/skin|cape/{player}.png` | 旧站 404（插件未装） | 已实现（超集） | ✅ |

## 4. 功能缺口优先级（blessing 1:1 复刻主干）

- **P1+（首位，2026-10-23 ✅ 基础功能落地）**：邀请码模块 1:1（基准=线上魔改版）：`invitation_codes` 六列表（含 `description` 老站约定备注列——人可读标识，旧库直迁）+ 注册流（`SKIN_INVITE_REQUIRED` 两态：默认开放选填 / `true`=线上必填，核销进注册事务封死双用竞态）+ 管理面三端点（生成/列表/改备注）+ 注册页输入框与管理面板。**插件候选（插件宿主完成后引入）**：① CSV 备注清单一键发码（写 description）② 邀请码查询页（行为规格已取证存档）
- **P1（2026-10-23 部分落地，2026-10-27 复核）**：
  - ~~衣柜 closet~~ ✅（`user_closet` 1:1 三列同构 0007，四端点+前端，孤儿行可见可删；**「换肤必须先入柜」规则已拍板：不启用**——老站 UI 流程而非数据结构，强加会破坏已验收的 setSkin 直连；衣柜按收藏集合上线）
  - ~~改密端点~~ ✅（`POST /api/v1/me/password`，改密即吊销全部 ygg+OIDC 授权）· ~~管理面选项面板~~ ✅（env 迁 UI：site_name/server_name/skin_domains/invite_required/require_verification 白名单，开关类请求时即时生效）· ~~管理面用户改密/删除~~ ✅（令牌/OIDC 撤销联动、事务删凭据、纹理/审计保留、uid1 禁删）
  - ~~找回密码~~ ✅（A1 2026-10-26 上线：SMTP 全链路，防枚举 / 单次令牌 / 面板热配置）；邮箱验证流延后（独立线，未排期）
  - ~~ygg_rate_limit~~ ✅（2026-10-23：`internal/ratelimit` 滑动窗口+失败锁定双层，认证四端点接线；429 响应为**新增偏差备案**——legacy 契约从未定义限流，冻结清单/错误体不受影响，仅追加 TooManyRequests；阈值面板可调）
  - **P1 主干至此全清**（邮箱验证流延后为独立线）
- **四路代码审查修复 ✅（2026-10-23）**：迁移链（clean 规范化先于去重/自排序/悬空 pid、预检告警、零日期容错、options 幂等改写、closet BINARY、DSN 校验、0005 拆分、0009 形状自愈）+ 凭据/令牌生命周期（verify 错误分流、refresh 归属校验与轮换单次性、end_session 双吊销、device code 单次兑换、改密副作用加固、XFF 末跳、衣柜 IDOR、player_limit 执法、settings 严格校验）。约 30 项发现分级处理，P1/P2 全清。
- **迁移导入器 ✅（2026-10-23）**：`skin-nexus migrate import` apply 路径——ID 1:1 保留、七表幂等、uuid 五步清洗接线、`legacy:bcrypt` 等算法分类 + PHC 短路（加密兼容：`$2y$` 直迁直验、不依赖盐链挂载）；生产数据 dry-run 对账（2026-10-23 演练口径；10-24 全量实跑口径见 migration.md §三）66/66/59/87/82/393 全一致、uuid 66 行零异常、59 文件零损坏。
- **P2**：通知/纹理广场 · OAuth 社交登录 · API PAT 面 · 插件宿主 · 正版绑定 · 纹理删除
- **举报 ✅（2026-10-31）**：公开材质举报（侵权/色情/暴力/仇恨/垃圾/其他）、去重与频率限制、管理端忽略/已处理/下架（下架即转私有并清公共 dedupe）。
- **P3**：`/api` 根装饰 · 验证码 · 积分商城 · 2FA

详细版（含逐端点请求/应答、JWT 形状、存疑项）保留在维护者本地分析档；本表是公开事实源。
