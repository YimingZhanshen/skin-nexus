---
description: "SkinNexus 可选服务器接入模块：平台原子创建租户与首位 owner、服主门户、按服验证、策略、事件、凭证、导出与宽限期删除。"
---

# 服务器接入

SkinNexus 的服务器接入模块让一个站点为多个 Minecraft 服务器提供共享身份与皮肤服务。玩家仍使用原来的全局账号和 Yggdrasil API；服主只管理自己租户内的服务器、接入策略与进服结果。

模块默认关闭。开启入口是管理面板的站点设置项 `serveraccess_enabled`，环境变量 `SKIN_SERVERACCESS_ENABLED` 只在选项不存在时兜底。选项存在且合法时以选项为准；读取错误、空值或仅含空白、非法值均强制关闭，不会回退到可能开启模块的环境变量。合法值为 `1`、`true`、`on`、`yes`、`0`、`false`、`off`、`no`，忽略首尾空白且不区分大小写。关闭后租户 API 与验证 API 返回 404，旧全局 Yggdrasil 入口不受影响，已有 `sa_*` 数据保留。`/servers` 页面仍会打开并显示模块不可用。

本模块没有公开自助注册或计费入口。平台超管可在现有管理面的服务器接入视图中原子创建租户与首位 owner；后续成员由 owner 在门户中邀请，接受邀请前必须完成邮箱验证。首位 owner 由平台超管基于现有账号人工建立，不经过成员邀请链接。玩家不需要成为租户成员。

## 接受成员邀请

已被邀请但尚未加入租户的已登录用户，在 `/servers` 的 membership-forbidden 提示之前会看到邀请接受卡片。用户把 owner 一次性交付的令牌粘贴进密码型输入框，并显式点击“接受邀请”；输入或打开页面不会自动消费令牌。

卡片只调用 `POST /api/serveraccess/v1/invites/accept`，请求体为 `{"token":"..."}`。令牌不进入 URL、query 或 fragment，也不写入本地存储；提交后立即清空输入框。成功后页面重新加载租户成员关系和服务器目录。未验证邮箱返回 403 `auth.email_verify_required`；无效、过期或已使用的令牌返回 404 `serveraccess.invite_invalid`；已属于其他启用中租户的账号返回 403 `serveraccess.forbidden`。这些错误在卡片内以可访问的 alert 显示，不清空页面或伪装成成功。

## 角色与可见范围

| 角色 | 能做什么 | 不能做什么 |
|---|---|---|
| 玩家 | 使用全局账号登录、进服、上传与穿戴皮肤 | 查看租户管理面、其他服主的策略或事件 |
| 租户 viewer | 查看本租户成员、服务器、策略、白名单、封禁、事件与凭据元数据 | 写操作、全局玩家查询、邀请、导出、请求删除 |
| 租户 admin | 管理服务器、策略、白名单、封禁与凭据；导出租户数据；查询全局玩家身份 | 管理成员、请求删除租户 |
| 租户 owner | 拥有 admin 能力，并可邀请/移除成员、请求删除租户 | 访问其他租户或平台紧急控制 |
| 平台超管 | 在现有管理面创建租户与首位 owner，查看租户/服务器状态与配额，紧急停服、吊销凭据、切换全局 fallback、执行到期删除与查看审计 | 用机器管理员令牌调用平台服务器接入接口 |

租户范围来自当前网页会话的成员关系，不信任请求中的租户 id。平台接口只接受 `permission=2` 的网页超管会话，不接受 `X-Admin-Token`。服主门户使用普通同源会话，不使用管理员令牌。平台停服、配额、fallback、凭据吊销与到期删除，以及平台创建租户、服主组合更新服务器，均与对应审计在同一事务中提交；审计写入失败时操作回滚，不出现“操作已生效但没有审计”。

## 全局玩家查询

`GET /api/serveraccess/v1/players/lookup?q={name-or-uuid}` 只允许当前租户的 admin/owner 使用普通网页会话调用；viewer 返回 403。查询按角色名（不区分大小写）或 32 位 UUID 精确解析全局玩家，成功仍只返回 `{uid,name,uuid}`，未找到返回 404。该端点不返回邮箱、IP 或全局资料；这是服务端强制边界，不依赖门户是否显示查询表单。

## 平台创建租户与首位 owner

平台超管使用网页会话进入现有管理面的服务器接入视图，填写租户名称、服务器配额（1–10000）和已存在用户的 owner UID。界面在提交期间禁用重复操作，失败保留输入并显示错误，成功后刷新租户与审计列表。

对应接口为 `POST /api/admin/v1/serveraccess/tenants`，只接受同源、JSON 请求体和 `permission=2` 的网页超管会话：

```json
{"name":"example","serverLimit":5,"ownerUid":42}
```

服务端去除名称首尾空白，要求名称非空且不超过 190 个字符；拒绝未知字段，因此客户端不能指定 `tenant_id` 或 `tenantId`。owner 必须存在且不能已属于其他启用中的租户。租户、owner 成员关系与 `serveraccess_tenant_create` 审计在同一事务中提交，任一校验、写入或读取失败都会回滚；并发创建同一 UID 时只允许一个成功。成功返回 HTTP 201 和 `{"tenant": {...}, "owner": {...}}`。

### 平台列表搜索

平台租户与服务器列表的 `q` 参数按名称进行不区分大小写的子串搜索，首尾空白会被去除；空查询不加过滤条件。查询中的 `%`、`_` 与 `!` 都是字面字符，不是通配符或转义入口，因此搜索 `%` 只匹配名称中实际包含百分号的记录。该行为使用数据库方言自带的转义谓词，在 MySQL、PostgreSQL 与 SQLite 上保持一致。

## 服主列表分页与配额

服主的服务器、邀请、白名单与封禁列表统一接受 `page` 与 `size` 查询参数，按 `id DESC` 返回。页码默认 1，`page<1` 或无法解析时按 1；每页默认 50，`size<1` 或无法解析时按 50，超过 200 时钳制为 200。超出末页时返回空数组和完整的 `total`，不会静默截断或返回错误。

- `GET /api/serveraccess/v1/servers?status={status}`：保留 `servers` 键，响应为 `{"servers":[],"total":0,"quotaUsed":0,"page":1,"size":50}`。`total` 是当前租户内符合 `status` 的服务器数；`quotaUsed` 始终是当前租户全部服务器数，不受状态筛选或分页影响。
- `GET /api/serveraccess/v1/tenants/me/invites`：响应为 `{"invites":[],"total":0,"page":1,"size":50}`。
- `GET /api/serveraccess/v1/servers/{id}/whitelist`：响应为 `{"whitelist":[],"total":0,"page":1,"size":50}`，总数只统计当前租户的指定服务器。
- `GET /api/serveraccess/v1/servers/{id}/bans`：响应为 `{"bans":[],"total":0,"page":1,"size":50}`，总数只统计当前租户的指定服务器。

平台租户、服务器与审计列表继续使用 `page`/`size`，默认每页 20、最大 200，响应保留 `total`、`page`、`size`。服主与平台的分页默认值互不影响。导出、恢复与验证仍使用原有非分页查询和独立上限，不调用这些分页方法。

服主门户的服务器、邀请、白名单与封禁列表使用共用分页组件，每页默认 50；平台列表仍为每页 20。服务器徽章和创建门槛使用响应中的 `quotaUsed`，不再把当前页条数当作租户总用量。切换服务器或状态筛选会回到第一页，删除邀请、白名单或封禁导致末页为空时会自动钳制到最后一个有效页；较晚返回的旧列表响应不会覆盖新页面。

## 服主组合更新

`PATCH /api/serveraccess/v1/servers/{id}` 可稀疏更新 `name`、`status` 与 `policy.whitelistEnabled`、`policy.premiumFallbackEnabled`。未提供的字段保持锁定行中的当前值，不会用请求前的旧快照覆盖平台配额或另一项 owner 开关。

store 按 tenant → server → policy 顺序加锁，在同一个事务内校验租户状态、服务器停服状态、写入全部请求字段并记录 `serveraccess_server_update`。租户处于 deleting 时拒绝更新；请求改变已停服服务器的状态时返回 403，平台停服不会被 owner 更新覆盖。仅改名或仅改策略仍保留既有允许行为，且不会清除停服状态。任一字段写入或审计失败都会回滚名称、状态与策略，响应仍为 `{"id":...,"name":...,"status":...}`。

## 零组件接入

零组件模式只需要一个按服的高熵 URL secret，不需要在游戏服务器或独立代理上安装插件。

1. 平台超管建立租户与首个 owner 的成员关系，owner 登录 `/servers`。
2. owner 创建服务器，在服务器详情中选择 `url_secret` 模式并创建接入凭据。明文 secret 只显示一次，数据库仅保存 SHA-256 摘要。
3. 将启动器或 authlib-injector 的验证 API root 配置为：

   ```text
   https://skin.example.com/v/<secret>
   ```

   `<secret>` 是完整随机路径段。不要追加 `/api/yggdrasil`，也不要把 secret 放到 query 参数。authlib-injector 启动时会直接 GET 这个 root（有无末尾斜杠均可）读取 `meta`、`skinDomains` 与 `signaturePublickey`；凭据无效、过期、吊销或模块关闭时同样返回 404。
4. 启动器仍使用全局 Yggdrasil 登录面；进服验证请求由该按服 root 转发到：

   ```text
   GET /v/<secret>/sessionserver/session/minecraft/hasJoined
   ```

5. 用测试账号验证允许与拒绝两条路径，再分发配置。URL secret 本身就是凭据，只能通过受控渠道交付。

有效凭据的策略拒绝、未命中或验证故障保持标准空 204；无效、过期、吊销、跨服或模块关闭统一返回 404。成功时返回本地签名 profile，或在满足 fallback 条件时返回官方原样响应。验证响应带 `Cache-Control: no-store` 与 `Referrer-Policy: no-referrer`。

## 可选验证代理

需要 Header 模式或统一本地入口时，可以运行可选的 `skin-nexus verify-proxy` 子命令。它不连接数据库，只持有 public server id、上游 API root 与 Bearer credential。

- Header 验证端点：`GET /api/serveraccess/v1/servers/<public_id>/hasJoined`，请求头为 `Authorization: Bearer <secret>`。
- proxy 本地标准入口：`GET /sessionserver/session/minecraft/hasJoined`，另有 `/api/yggdrasil/sessionserver/session/minecraft/hasJoined` 别名与 `/healthz`。
- proxy 默认只监听环回地址；它去掉客户端 Authorization/Cookie，只转发 `username`、`serverId`、`ip`，并注入自己的 Bearer credential。
- 成功 profile 原样返回；204、4xx、认证失败、重定向与最终上游故障均保持空 204。只有网络瞬断、超时和 5xx 有限重试。
- proxy 不是零组件模式的前置条件。URL secret 模式不依赖 proxy。

代理配置、TLS、私密凭据文件权限、日志与指标采集见 [部署手册](deploy.md#可选-skin-nexus-verify-proxys3s4-不依赖)。凭据轮换后需重启 proxy 加载新文件；吊销与停服不需要重启。

## 策略顺序

每次验证按以下顺序判断：

1. 模块、租户与服务器状态；
2. 凭据有效；
3. 平台封禁；
4. 本服封禁；
5. 本地账号白名单；
6. 验证配额与并发限制；
7. 本地 join session；
8. 满足条件时执行官方 fallback；
9. 返回签名 profile 或空 204。

平台控制优先于服主设置。本服封禁只作用于该服务器；租户停用或删除请求会暂停其全部服务器。白名单按本地账号 `uid`，不按昵称认领官方身份。

### 白名单、封禁与 fallback

- 白名单开启时，未知正版 UUID 与没有本地 join session 的已知账号都拒绝，且完全禁用官方 fallback。
- 本服封禁支持永久或到期自动失效；平台封禁仍优先。
- 未开启白名单时，官方 fallback 只在真实本地 session miss、按服开关、平台全局开关与既有 Yggdrasil fallback 开关同时允许时回源。
- deny、参数错误、数据库故障、封禁、停用与吊销不会触发 fallback。
- 按服并发上限同时约束本地验证与官方 fallback。fallback 连续 5 次上游故障打开 30 秒熔断，半开只允许 1 次探测；健康 204 不算故障。
- `VerifyRateLimit` 是每分钟原子额度，`VerifyBurst` 是每秒原子突发额度；两者都在策略允许后、本地 session 与 fallback 之前判定，超额统一记录 `quota_exceeded` 并返回空 204。
- 熔断与并发计数是进程内状态，多副本各自独立。需要全局限流时，在网关或单副本层部署验证面。

## 信任级别与事件可见性

| 来源模式 | 事件信任标记 | 实际证明 |
|---|---|---|
| `url_secret` | `possessed_credential` | 请求方持有按服 secret |
| `header` | `header_credential` | 请求方持有 Bearer credential；主要改善日志卫生 |
| `attested` | 未实现 | 当前没有组件身份证明，Header 模式也不能当作 attested |

零组件事件表示一次凭据验证，不表示真实进服人数，也不证明游戏服务器身份。

服主时间线按服务器隔离，默认展示最近 24 小时，可用 `before` 游标与 `from`/`to` 缩小范围，单页最多 200 条，时间范围最长 366 天。每条只返回 `id`、`createdAt`、`outcome`、`reasonCode`、`durationMs`、`sourceMode`；不返回玩家名、UUID、uid、IP、邮箱、token、请求 id 或 join-session id。

事件保留 worker 在模块开启时立即清理一次，此后每小时运行；按每服 `EventRetentionDays` 删除严格早于 UTC 截止时间的事件，并按同一 UTC 截止时间分批删除已过期的分钟/秒配额窗口，每批 1000 条。单服失败会记录指标并继续其他服务器。多实例同时清理是幂等的，但没有分布式 leader。

## 凭据轮换与吊销

- 创建或轮换产生 256 位随机 secret；明文只出现一次。
- 轮换默认保留旧凭据 24 小时重叠期，便于先验证新配置再吊销旧配置。
- 吊销立即生效；过期凭据自动失效。轮换、吊销与到期都不能恢复旧 secret。
- 怀疑泄漏时跳过重叠等待，立即吊销并换新，同时按事件流程检查反代、WAF、CDN、APM、端侧日志与抓包副本。
- 数据库备份包含的是凭据摘要与状态，不包含可恢复的明文 secret。恢复后旧 secret 仍无法从备份还原。

## 租户数据导出

owner/admin 可在 `/servers` 点击“导出租户数据”，浏览器下载真实 JSON 附件。导出包含：

- 租户 id、名称、状态、服务器配额、创建时间、删除请求时间 `deletionRequestedAt` 与删除截止时间 `deletionDueAt`；
- 服务器 id/public id/名称/状态/来源模式/创建时间；
- 服务器策略与配额；
- 白名单 uid、note、创建时间；
- 封禁 uid、reason、expiry、创建时间；
- owner 可见进服结果；
- 凭据 id、mode、status、创建/最近使用/到期时间。

导出不包含邮箱、IP、token、secret/hash、scopes、邀请、全局用户/玩家/纹理或事件中的玩家身份。审计只记录租户 id，不记录导出内容。

非空租户固定使用 6 条批量数据查询。首版硬上限为 1000 个服务器、10000 条白名单、10000 条封禁、100000 条进服事件、10000 条凭据元数据，以及含末尾换行的 16 MiB JSON。任一超限返回 413 与 `serveraccess.export_too_large`，不静默截断、不返回附件、不写导出审计。内部配置只能降低上限，不能抬高。

## 租户删除生命周期

1. owner 在门户请求删除，必须输入与租户名称完全一致的确认文本。
2. 服务端在一个事务内把租户置为 `deleting`、固定 `deletion_requested_at` 与 `deletion_due_at`、暂停全部服务器并吊销全部凭据。重复请求返回原截止时间，不延长期限。
3. 默认宽限期为 7 天，`SKIN_SERVERACCESS_DELETION_GRACE_HOURS` 可设为 1–8760 小时。修改配置不改变已经承诺的期限。
4. 宽限期内服务器不可用，owner 仍可导出需要保留的数据。删除中的租户与服务器不能重新启用。
5. 平台超管在视图中看到请求时间与截止时间；只有截止时间到达后“删除租户”才可用。未请求或未到期返回 409。
6. 到期删除只级联 `sa_*` 数据，并在平台审计留痕；全局用户、角色与纹理保留。重复物理删除返回 404。

恢复演练覆盖整站备份，不承诺租户级单点恢复。数据库、签名密钥、插件密钥与 proxy 凭据文件的备份/恢复步骤见 [部署手册](deploy.md#_2-6-备份与恢复)。

## 可复现验收

在仓库根目录运行 `node scripts/e2e-serveraccess.mjs`。脚本会构建当前二进制，通过 Node API 预留可用的临时回环端口，在 `127.0.0.1` 上启动隔离 SQLite 实例，并通过真实 HTTP API 创建两个租户、两台服务器与两名玩家，覆盖 authlib-injector 元数据、URL secret 与 Header 两种验证、白名单拒绝/允许、租户封禁隔离、平台封禁、跨租户读写/轮换/吊销/导出 404、事件隔离、凭据脱敏、模块热开关及全局 Yggdrasil 回归。子进程只接收操作系统运行所需的白名单环境变量与显式 `SKIN_*` 测试配置，不继承外部服务配置；普通请求设有 10 秒超时，失败时打印有界的 stdout/stderr 尾部，并在确认子进程退出后清理临时目录。脚本只使用临时目录与回环地址，不连接生产环境。

该脚本不替代真实 Minecraft/authlib-injector 进服测试；公开公测前必须完成真实客户端、服务器与外部数据库的验收矩阵。

## 排错

| 现象 | 检查 |
|---|---|
| `/servers` 显示不可用 | 面板 `serveraccess_enabled` 是否开启；模块关闭时 API 返回 404 |
| 玩家全部收到 404 | secret 是否完整、是否已吊销/过期、服务器是否停用、模块是否关闭；不要把 404 改成重定向 |
| 玩家收到空 204 | 查看本服事件的结果与 reason；依次检查平台封禁、本服封禁、白名单、配额、本地 session 与 fallback 开关 |
| 白名单玩家仍被拒 | 白名单按 uid；确认账号已绑定本地角色，且有有效本地 join session；白名单开启时不会回源官方身份 |
| fallback 不工作 | 确认真实本地 miss、未开白名单、按服/平台/全局 Yggdrasil 三级开关均开启，并检查熔断是否打开 |
| 导出返回 413 | 按错误消息定位超限集合；导出不会截断，需在受控数据治理后减小范围 |
| 平台删除返回 409 | 尚未请求删除或未到 `deletionDueAt`；以服务端时间为准 |
| 事件缺失 | 检查 `serveraccess_event_write_failures_total` 与结构化日志；事件写入失败不改变验证结果 |
| 保留清理失败 | 检查 `serveraccess_retention_cleanup_failures_total`、数据库权限、磁盘与每服保留策略 |
| 怀疑 secret 泄漏 | 立即吊销并轮换，清理各层已采集副本；只删除某一层日志不能撤销已泄漏凭据 |
