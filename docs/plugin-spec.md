# SkinNexus 插件创作规范（plugin-spec）—— 宿主 v2

> 面向想为 SkinNexus 写插件的人（包括让 AI 代劳）。**零构建原则**：插件 = `plugin.json` +
> 一个入口 JS（可选静态 UI 目录），不需要 Node/打包/编译；放入 `<data>/plugins/<name>/`
> 后由管理面板启用即生效。
>
> 宿主实现：`server/internal/plugin/`（校验规则以代码为准，本规范与代码逐项对齐）。
> 当前只有**单一插件 API 版本**（`hookApi: 2`）。项目尚无存量第三方生态，不维护旧版兼容层。

## 1. 信任模型与安全边界

- **只装你信任的插件**。插件代码在**服务进程内**的 goja VM 中执行：没有网络、没有文件系统、没有定时器、
  没有 `require`，但**不是沙箱**——它能通过声明的 hook/服务影响响应内容，且与宿主共享进程内存。
- v2 的配置、数据、UI、路由全部走**宿主托管服务**：插件不直接接触 MySQL、宿主表、任意文件或任意网络。
- 启用/停用/重载/配置修改**仅超级管理员（permission=2）网页会话**可操作；`X-Admin-Token` 机器通道一律拒绝。
- 插件默认 **disabled**：放入目录不会自动执行，必须显式启用。
- 所有启停/重载/配置修改写审计（`plugin_enable` / `plugin_disable` / `plugin_reload` /
  `plugin_config_update` / `plugin_guards_bypass`）。
- UI 第三方代码只能运行在 sandbox iframe 中（不带 `allow-same-origin`）；不要尝试把插件 HTML 直接注入主 SPA。
- 安装来源与签名等级会写进 `plugin_meta` 并在后台「插件」页标注：
  `verified`（可信签名）、`unknown-key`（未知密钥）、`unsigned`（未签名）、`invalid`（签名/摘要不匹配）。
  市场安装**只接受 verified**；本地上传的其余三档必须由超管在风险弹窗中显式勾选确认后才可安装，
  且确认绑定包摘要（`expectDigest`），换包即失效。

## 2. 安装 / 启用 / 停用 / 重载

```
<data>/plugins/<name>/
  plugin.json       # 必需：清单
  index.js          # 必需：入口（文件名由 entrypoint 指定）
  ui/               # 可选：声明为 ui.assets 的静态资源
```

- 放入目录后**重载或重启**才会被发现（宿主进程内一次加载）。
- 管理面板「插件」页可启用/停用/重载、编辑配置、查看健康/路由/最近日志。
- 修改入口 JS 或 UI 资源后必须**重载**（或重启）才生效。
- 推荐安装方式：上传/市场安装**签名 bundle zip**（含 `plugin.json`、`index.js`、`market.json`、`bundle.json`）。
  宿主校验 Ed25519 签名 + 内容摘要后，staging 解压 → 原子替换 → 默认 disabled；
  升级保留启停状态并保留上一版用于回滚。市场安装的未签名/未知密钥/签名错误包一律拒绝；
  本地上传可在风险确认后安装，并在插件列表永久标注来源与信任等级。
- 作者打包/签名/发布 registry 的完整流程见 [`plugin-publishing.md`](plugin-publishing.md)。
- 安装状态在 `<data>/plugins/.state/`；手动放目录仍可用于开发，但不会获得签名校验/回滚。

### 2.1 签名 bundle 格式

```text
player-notes_1.2.0.zip
  plugin.json          # hookApi=2 清单
  index.js
  ui/...               # 可选
  bundle.json          # {"name","version","keyId","signature"}
```

`bundle.json` 的 Ed25519 签名覆盖除它自身外的全部 zip 条目内容摘要（按路径排序）；
`dist.sha256`（市场）覆盖 zip 字节，`bundle.json` 签名覆盖解压后的内容。两层都必须通过。
管理员在「插件市场」上传/安装；name/version 不一致、zip 越界一律拒绝。
本地上传未签名/未知密钥/签名错误的包时，后台先展示检查结果（插件名/版本/权限/签名等级/密钥/摘要），
超管勾选风险确认后才能安装；市场路径没有这个覆盖开关。

## 3. plugin.json 字段

| 字段 | 必填 | 说明 |
|---|---|---|
| `name` | ✅ | 唯一 ID，必须等于目录名；`^[a-z0-9][a-z0-9_-]{1,63}$` |
| `version` | ✅ | 字符串，≤64 字符 |
| `hookApi` | ✅ | 必须为 `2`（单一 API 版本） |
| `entrypoint` | ✅ | 目录内单个 `.js` 文件名（拒绝 `..`/绝对路径/符号链接） |
| `permissions` | ✅ | 声明的能力数组（见 §4）；未知权限直接拒绝加载 |
| `runtime` | ⬜ | v2 目前只接受 `goja`（native/sidecar 为后续路线） |
| `minServer` | ⬜ | 最低宿主版本，`>=x.y[.z]` / `<x.y` 组合；不满足则标记错误 |
| `title` / `author` / `homepage` / `description` | ⬜ | 展示元数据 |
| `config` | ⬜ | v2 配置 schema（见 §5） |
| `store` | ⬜ | v2 命名空间数据声明（见 §6） |
| `routes` | ⬜ | v2 路由声明（见 §7） |
| `ui` | ⬜ | v2 页面/插槽/表单/资源/i18n（见 §8） |
| `net` | ⬜ | 受控出站 HTTP（见 §10.4） |
| `jobs` | ⬜ | 持久任务处理器（见 §10.5） |
| `media` | ⬜ | 外部媒体重定向白名单，如 `{"avatarHosts":["cdn.example.com"]}`（见 §10.1） |
| `requires` / `conflicts` | ⬜ | P2 依赖解析预留；当前解析但不执行 |

上限：脚本 ≤1 MiB、manifest ≤64 KiB、单插件路由 ≤32、全局路由 ≤64、插件总数 ≤64、UI 页面/插槽各 ≤32。

## 4. 权限声明

权限是**声明式 + 加载时校验**：注册某个 hook/服务却未声明对应权限，插件加载失败并显示原因。

| 权限 | 允许 |
|---|---|
| `profiles:read` | `onUserProfileFilter` |
| `textures:read` | `onTextureURLFilter` |
| `textures:write` | `onTextureUploaded` |
| `auth:hooks` | `onUserLifecycle`、`onAuthPhase` |
| `http:routes` | public 路由 |
| `http:routes:session` | v2 session 路由 |
| `http:routes:admin` | v2 admin 路由 |
| `http:routes:superadmin` | v2 superadmin 路由 |
| `http:routes:bearer` | v2 `bearer` 路由（`Authorization: Bearer`，无 cookie/CSRF） |
| `config` | `skin.config` |
| `store:kv` | `skin.store.kv` |
| `store:docs` | `skin.store.collection`、`onInstall`/`onUpgrade` |
| `ui:page` / `ui:slot` / `ui:assets` | UI 页面 / 插槽 / 静态资源 |
| `ui:forms` | `ui.forms`：注册/上传的宿主渲染字段 |
| `media:avatar` | `onAvatarFilter` + `media.avatarHosts` |
| `account:read` | `skin.account.get/limits` |
| `players:read` / `players:write` | `skin.players.list/get` / `create/rename/delete/setTexture` |
| `events:subscribe` | `skin.events.on` |
| `auth:guards` | `skin.guards.on` |
| `net:http` | `skin.net.get`；manifest 必须声明 `net.http` |
| `mail:send` / `cli:commands` | 预留（当前无运行时实现） |
| `schedule:jobs` | `skin.jobs.register/submit/list/status/cancel/resume`；manifest 必须声明 `jobs` |
| `plugins:read` | `skin.host.info()` / `skin.host.hasPlugin()` 只读内省 |

## 5. 配置（`skin.config`）

`config.schema` 是 JSON Schema 子集：

```json
{
  "type": "object",
  "required": ["token"],
  "properties": {
    "message": { "type": "string", "default": "hello" },
    "count": { "type": "integer", "minimum": 1, "maximum": 10, "default": 1 },
    "mode": { "type": "string", "enum": ["a", "b"], "default": "a" },
    "token": { "type": "string", "format": "secret" }
  }
}
```

- 支持 `type`（object/string/integer/number/boolean/array）、`properties`、`required`、`default`、
  `enum`、`minimum`/`maximum`、`format: "secret"`、`x-ui`（text/textarea/select/checkbox/password/number/list）。
- 未知字段/类型不符/越界/缺必填会在保存时返回 400；加载时配置非法会把插件标记为 error。
- `format: "secret"` 的字段**加密存储、后台只显示掩码**；不要给 secret 声明 `default`。
- 运行时读取：`skin.config.get(key)`、`skin.config.all()`；配置修改立即生效，无需重载。

## 6. 命名空间数据（`skin.store`）

插件**不能**直接访问数据库或宿主表；只能使用自己声明的命名空间。

```json
"store": {
  "schemaVersion": 1,
  "kv": { "maxKeys": 50000 },
  "collections": { "notes": { "maxRows": 1000, "maxBytes": 65536 } }
}
```

```js
skin.store.kv.set("last", "value", { ttlMS: 86400000 });
skin.store.kv.get("last", "default");
skin.store.collection("notes").put("id1", { text: "hello" });
skin.store.collection("notes").list({ prefix: "", limit: 100 });
```

- KV：单值 ≤64 KiB；默认每插件 ≤1000 keys，可用 `store.kv.maxKeys` opt-in（硬顶 50000，
  适合需要按条目记录游标的导入类插件）；支持 TTL。
- 集合：单文档 ≤256 KiB（且受 `maxBytes` 约束），每集合 ≤1 万行（且受 `maxRows` 约束）。
- 宿主托管迁移：`schemaVersion` 递增时，在启用/重载时调用
  `skin.store.onInstall(fn)`（0→N）或 `skin.store.onUpgrade(fn)`（旧→新，`fn` 收到 `{from,to}`）；
  迁移失败会关闭 VM 并把插件标为 error。迁移只能调用 `skin.config` / `skin.store`。
- 禁用不删数据；删除目录即卸载，数据默认保留（P2 提供清理入口）。

## 7. 路由（`skin.route` + manifest `routes`）

所有插件 API 在 `/api/plugins/<name>/...` 下。**manifest 是权限与策略的唯一来源**，运行时注册必须与声明一致：

```json
"routes": [
  { "method": "GET",  "path": "/notes",      "tier": "session" },
  { "method": "POST", "path": "/notes",      "tier": "session", "bodyLimit": 16384, "csrf": true },
  { "method": "GET",  "path": "/notes/{id}", "tier": "session" },
  { "method": "GET",  "path": "/files/{rest...}", "tier": "public" }
]
```

```js
skin.route("GET", "/notes", function (req) {
  return { items: [], user: req.user.id };
});
```

| 档位 | 鉴权 | CSRF | 注入 |
|---|---|---|---|
| `public` | 无 | 不适用 | 无 |
| `session` | `skin_session` | 非 GET 必须 | `req.user = {id,nickname,permission,admin}` |
| `admin` | permission > 0 | 非 GET 必须 | 同上 |
| `superadmin` | permission = 2 | 非 GET 必须 | 同上 |
| `bearer` | `Authorization: Bearer` | **不需要** | `req.user`（Ygg/OIDC token → uid） |

- bearer 档由宿主 `BearerResolver` 解析：接受不透明 Yggdrasil access token 和 OIDC
  access token（Yggdrasil Connect scope），`permission < 0` 返回 403；401 带
  `WWW-Authenticate: Bearer`。原生客户端无需 cookie/CSRF。

- 请求对象：`{method, path, params, query, body?, user?, ip, headers}`；**不含 cookies**。
- 只读安全 header 子集（content-type/accept-language/user-agent/origin/x-request-id）。
- 路径参数：`{id}` 单段、`{rest...}` 末尾捕获；禁止 `..` 与 `/_csrf`（宿主保留）。
- body 默认 64 KiB，最大 1 MiB；声明 `bodyEncoding:"base64"` 的路由可到 4 MiB，
  原始字节以 base64 字符串传入（JSON 请求仍按 JSON 解析）。响应 1 MiB；
  每路由可声明 `rateLimit`（如 `"60/m"`）与 `timeoutMS`（100–10000，默认宿主 1s；
  二进制上传建议 10000）。
- CSRF：`GET /api/plugins/<name>/_csrf` 获取 token（同时设置 `skin_csrf` cookie）；
  非 GET 的 session/admin 路由必须带 `X-CSRF-Token`。iframe bridge 会自动处理。
- 错误体：宿主错误统一 `{error, message, code}`；插件返回的 JSON 原样透传。
- 所有路由必须先在 manifest `routes` 中声明，未声明即加载失败。

## 8. UI（`ui`）

v2 的 UI 采用**固定插槽 + iframe/schema 页面**，不允许第三方直接注入 Vue 组件或任意 HTML。

```json
"ui": {
  "assets": "ui/",
  "pages": [{
    "id": "notes", "path": "/ext/player-notes/notes", "area": "user",
    "nav": { "label": "Notes" },
    "render": { "type": "iframe", "src": "notes.html" },
    "perm": 0
  }],
  "slots": [{
    "id": "skinlib.detail.sidebar",
    "render": { "type": "iframe", "src": "slot.html" },
    "height": 180
  }],
  "i18n": {
    "zh-CN": { "title": "笔记", "nav.notes": "角色笔记" },
    "en": { "title": "Notes", "nav.notes": "Player notes" }
  }
}
```

- `nav.<page-id>` 用于宿主侧栏/页面标题的本地化；缺省回退 `pages[].nav.label`。
- iframe 通过 `init.locale` 收到当前语言，插件 UI 负责切换自己的文案，
  并设置 `document.documentElement.lang`；不要假设宿主会翻译 iframe 内容。

固定 slot ID（全部已接线）：`app.topbar.after`、`auth.login.after`、`auth.register.after`、
`dashboard.cards`、`texture.upload.after`、`skinlib.list.toolbar`、`skinlib.detail.sidebar`、
`profile.card.footer`、`player.card.footer`、`footer.before`。
导航入口请用 page + `nav`，不再提供 `app.nav.*` slot。

### 8.1 宿主表单字段（`ui.forms`）

```json
"ui": {
  "forms": {
    "texture.upload": {
      "schema": {
        "type": "object",
        "properties": {
          "source": { "type": "string", "enum": ["self", "other"], "default": "self" },
          "credit": { "type": "string", "default": "" }
        }
      }
    }
  }
}
```

- formID 固定为 `auth.register` / `texture.upload`；每插件最多 2 个，schema ≤16 KiB，顶层字段 ≤20，
  值字符串 ≤512，**禁止 `format:"secret"`**（不要在表单里收密码/密钥）。
- 宿主用 `SchemaForm` 渲染；提交值经 JSON `pluginData`（注册）或 multipart `pluginData`（上传）传入，
  由宿主按已启用插件的 schema 补默认值、拒未知键，再送 guard 与事件的 `data.pluginData`。
- 宿主不持久化表单值：插件在 guard 阶段用昵称/邮箱哈希暂存，事件里拿到 userId/hash 后落自己的集合
  （见 `signup-survey` / `texture-provenance` 示例）。
- 声明了必填字段的插件会让不带 `pluginData` 的 API 客户端注册/上传失败（400）。

- 页面落在 `/ext/<name>/<page>`；服务端只对已启用插件的已声明页面返回 SPA shell，未知路径仍 404。
- `iframe` 页面/插槽从 `/plugin-assets/<name>/...` 加载，`sandbox="allow-scripts allow-forms"`；
  **不带 `allow-same-origin`**，iframe 无法读取宿主 cookie/localStorage。
- 资产 URL 自动追加 `?v=<插件版本>`，响应头为 `Cache-Control: public, max-age=0, must-revalidate`：
  升级插件后浏览器最多做一次 304 校验即可拿到新文件，不需要清缓存；升级时**必须递增版本号**。
- iframe bridge（`postMessage`，信封 `{v:1, nonce, id, type, payload}`）：
  - 宿主→frame：`init`（plugin/src/origin/path/locale/user/csrf/theme）；`path` 是宿主当前 URL，便于 slot 识别上下文；
    `theme` 提供 `{bg,panel,border,text,muted,accent,dark}`，插件页面应把它写入自己的 CSS 变量，
    不要硬编码浅色文字/透明背景（iframe 不继承宿主样式）；
  - frame→宿主：`ready`、`resize`、`toast`、`navigate`、`confirm`、`request`。
  - `request` 由宿主代发到该插件路由，自动带 cookie/CSRF，返回 `{status, body}`。
  - iframe 首次 `ready` 可用 `targetOrigin: "*"`（此时 origin 为 opaque）；之后用 init 里的 `origin`。
- 插件 UI 基线：只用宿主 `theme` 令牌/`var(--...)`；必须有加载/空/错误三态；
  按钮提交中禁用；移动端触控目标 ≥44px；不加载外部字体/CDN；图片 lazy；
  错误文案可翻译；宿主在 iframe 8s 未 `ready` 时显示重试层。
- SSR 详情页 slot 会自动追加宿主上下文查询参数：`?tid=<id>&hash=<texture-hash>`，
  slot 页面可直接从 `location.search` 读取（见 `texture-provenance`）。
- slot 高度统一 clamp 80–600；`schema` 渲染面仍未开放，`PluginSlot` 会跳过非 iframe 声明。
- 主题 `slots` 与插件 slot 独立；主题仍是管理员信任的 CSS/JS 面。

## 9. 事件与 guard

```js
skin.events.on("player.added", function (e) { /* e.playerId/e.playerName/e.userId/e.ip */ });
skin.guards.on("user.registration.check", function (e) {
  if (blocked(e.data.email)) return { allow: false, code: "x.blocked", message: "不允许注册" };
  return { allow: true };
}, { onError: "deny", timeoutMS: 50 });
```

- 事件目录：`user.registered`、`user.logged_in`、`user.deleted`、`player.added`、`player.renamed`、
  `player.deleted`、`profile.updated`、`texture.uploaded`、`texture.deleted`。事件异步、有界队列，失败 fail-open。
- guard 目录：`user.registration.check`、`auth.login.check`、`auth.join.check`、`profile.update.check`、
  `player.rename.check`、`texture.upload.check`、`texture.delete.check`。
  guard 同步执行，必须显式 `onError: "deny"|"allow"`；超时/异常按该策略处理。
- guard payload：注册 `{ip,data:{email,nickname,pluginData?}}`；登录 `{ip,data:{identifier}}`；
  资料修改 `{userId,ip,data:{nickname,email}}`；角色改名 `{userId,playerId,playerName,data:{oldName}}`；
  上传 `{userId,type,ip,data:{filename,pluginData?}}`。
- guard 不接收密码/cookie；deny 返回通用 403（错误体带插件提供的 message/code）。
- 超管可在管理面板开启 `plugin_guards_bypass` 破窗（审计 + 横幅）；仅用于插件故障救急。

## 10. 基础 hook

```js
skin.hooks.onUserProfileFilter(fn, { timeoutMS })
skin.hooks.onTextureURLFilter(fn, { timeoutMS })
skin.hooks.onTextureUploaded(fn)
skin.hooks.onUserLifecycle(fn)
skin.hooks.onAuthPhase(fn)
skin.hooks.onAvatarFilter(fn, { timeoutMS })   // 见 §10.1
skin.log(...args)
```

返回语义：profile 返回 `{SKIN,CAPE}` 覆盖（空字符串删除、undefined 不改）；texture 返回字符串；
事件返回值忽略；所有 hook 超时/异常 fail-open。过滤器严格在 `AssembleTextures` 签名之前执行。

### 10.1 头像重定向（`onAvatarFilter` + `media`）

```json
"permissions": ["media:avatar"],
"media": { "avatarHosts": ["cdn.example.com", "*.img.example.net"] }
```

```js
skin.hooks.onAvatarFilter(function (a) {  // {hash, size, threeD}
  return "https://cdn.example.com/avatar/" + a.hash + "/" + a.size + ".png";
});
```

- 仅作用于 `/avatar/*`，默认头像与 `/preview/*` 永不经过；返回 `undefined` 即回落 PNG。
- 返回 URL 必须是 HTTPS、无 userinfo、默认端口、≤2048，且 host 命中**同一插件**声明的 `media.avatarHosts`；
  否则静默回落。命中时返回 302 + `Cache-Control: public, max-age=60`。
- 注意：启用后客户端 IP 会暴露给该 CDN；预算 50ms，超时/异常 fail-open。

### 10.2 宿主内省（`skin.host`，需 `plugins:read`）

```js
var info = skin.host.info();
// {version, hookApi, capabilities:[], plugins:[{name,version,title,enabled}], slots:[]}
skin.host.hasPlugin("player-notes");
```

能力表（`capabilities`）：`host.introspect.v1`、`slots.v1`、`forms.v1`、
`avatar.filter.v1`、`bearer-auth.v1`、`binary-body.v1`、`account.v1`、`net.http.v1`、`jobs.v1`。
新能力只增不改；插件应据此降级，而不是试探报错。

### 10.3 账号/角色/纹理桥（`skin.account` / `players` / `textures` / `library`）

只能在**已认证路由**内调用（session 或 bearer）；身份来自路由，JS 不能传 uid。

```js
skin.account.get() / limits()
skin.players.list() / get(pid) / create(name) / rename(pid,name) / delete(pid)
skin.players.setTexture(pid, "skin"|"cape", textureId)   // 0 = 清除
skin.textures.list() / uploadBase64(type, model, desc, b64, meta?) / importBase64(type, model, desc, b64, meta?) / importLibrary(tid)
skin.library.list({type,q,sort,page,perPage}) / get(tid)
```

- 返回 `{ok:true,data}` 或 `{ok:false,error:{code,message}}`；非认证路由返回
  `account.required`，宿主未接线返回 `account.unavailable`。
- `uploadBase64` 是普通上传：同用户重复返回 `created:false`；公共库已有同 `(type,hash)`
  时返回 `texture.duplicate_public`，`error.details` 带 `textureId/url`。
- `importBase64` 是私有导入：写入当前用户的私有纹理，跳过公共重复检查（供导入后再应用到角色）。
- `meta` 可选：`{detail, source, sourceUrl, public}`。`source` 为
  `original|derivative|repost`；**二创/转载必须提供 http(s) 的 `sourceUrl`**，否则宿主拒绝；
  `detail` 为 ≤4000 字的 Markdown（宿主统一消毒后展示）；`public` 仅 `uploadBase64` 生效。
- 权限：`account:read`、`players:read/write`、`textures:read/write`。
- 所有所有权、名称规则、数量上限、guard、事件、审计与 Union 同步由宿主
  `internal/account` 执行；插件只做映射。视图不含 email/uid。
- 示例：`launcher-api`、`skin-importer`（见 [`launcher-api.md`](launcher-api.md)）。

### 10.4 受控出站（`skin.net`，需 `net:http`）

manifest 必须声明 `net.http`：

```json
"net": {
  "http": {
    "hosts": ["api.mojang.com", "sessionserver.mojang.com", "textures.minecraft.net"],
    "allowUserHosts": true,
    "userHostPaths": ["/skinlib/list", "/skinlib/info/", "/textures/"],
    "maxBytes": 2097152,
    "timeoutMS": 4000
  }
}
```

```js
skin.net.get(url) // -> {ok:true,data:{status,contentType,bytes,bodyBase64,retryAfter}}
```

- 只允许 http/https、80/443、无 userinfo、无重定向、固定 UA、无 Cookie/Authorization。
- 静态 `hosts` 精确/`*.` 后缀匹配；用户提供的域名还必须命中 `userHostPaths`。
- 传输层只拨号公网 IP（拒绝 loopback/私网/链路本地/云元数据，防 DNS rebinding），
  响应受 `maxBytes` 与 `timeoutMS` 限制；每 host 2 req/s（burst 4）。
- 失败码：`net.invalid_url`、`net.blocked_host`、`net.blocked_redirect`、
  `net.too_large`、`net.timeout`、`net.rate_limited`、`net.unavailable`。
- 不要把它用于用户可控的任意目标；只在产品确实需要外部源时声明。

### 10.5 持久任务（`skin.jobs`，需 `schedule:jobs`）

manifest 声明处理器：

```json
"jobs": [{ "name": "blessing-import", "timeoutMS": 30000, "maxAttempts": 3 }]
```

```js
skin.jobs.register("blessing-import", function (job) {
  // job = {jobId, attempt, payload, cursor, cancelRequested}
  // delayMS: 可选，0–60000；到点前不会被再次领取
  return { done: false, cursor: {...}, progress: 40, message: "...", delayMS: 250 };
});
skin.jobs.submit("blessing-import", {...}); // 仅 admin 路由
skin.jobs.list(1) / status(id) / cancel(id) / resume(id); // 仅 admin 路由
```

- 任务持久化在 `host_jobs`，按 step 保存 `cursor/progress`；每步结束释放租约并重新排队，
  下一次 tick 即可继续（不会等 30s 租约过期）；worker 崩溃/重启后从 cursor 续跑。
- **一个 step 只做少量上游请求**：`net.http` 默认每 host 4 次/秒窗口，把整页（list + 每项 info/texture）
  塞进一个 step 会触发 `net.rate_limited`。用 `delayMS` 拉开步距，长任务拆成多条记录级 step。
- 每个 step 在插件专属 job VM 中执行（不占请求 VM 锁）；超时/异常按 `maxAttempts` 退避重试。
- 取消在 step 之间生效（含排队中的任务）；`cancel` 后任务状态为 `cancelling/cancelled`，`resume` 可重新排队。
- 仅管理员可提交/查看/取消/恢复；任务载荷与进度不对普通用户开放。

## 11. 示例与官方插件

主仓库只保留两个最小夹具（供文档与测试）：

- `examples/plugins/hello-world`：零权限日志；
- `examples/plugins/hello-ui`：iframe 页面 + slot。

完整官方插件（12 个：config-greeter / nav-links / player-notes / texture-cdn /
lifecycle-log / signup-guard / texture-provenance / signup-survey / avatar-cdn /
name-policy / launcher-api / skin-importer）的源码与签名市场在独立公开仓库：
<https://github.com/YimingZhanshen/skin-nexus-plugins>。

投稿流程：插件仓库提 PR → CI `skin-plugin lint`（无密钥）→ 维护者评审合并 →
CI 环境密钥签名并更新 registry/Pages → 主站市场 10 分钟内可见。
下载/复制插件目录到 `<data>/plugins/` 仍可在无市场环境手工启用。

## 12. 非目标与后续路线

- **v0.4.0**：slot 全接线、`ui.forms` 注册/上传字段、`media:avatar` 头像重定向、资料/角色改名 guard、
  宿主能力内省、本地包信任分级与来源标识；官方插件仓库 `skin-nexus-plugins` 统一源码，
  PR 投稿 + `skin-plugin lint` + CI 签名/续签/Pages 发布；主站默认官方 registry，下游可用
  `SKIN_PLUGIN_MARKET_URL` 覆盖；新增 Bearer 路由档、账号/角色/纹理桥与二进制上传，
  官方 `launcher-api` 提供启动器 API v1。
- **v0.5.0（开发中）**：公共纹理 `(type,hash)` 去重与普通上传重复拒绝；受控出站 `net:http`
  与持久任务 `schedule:jobs`；官方 `skin-importer` 提供 Mojang/Blessing 单条导入和管理员批量收录。
- **后续按需**：多 registry、mail/CLI、主题 v2、sidecar/native、评分/评论。
- **不做**：插件直接访问数据库/宿主表/任意文件系统/任意网络；不允许第三方 Vue/Twig/HTML 注入主 SPA。
- **兼容策略**：项目尚无存量第三方生态，只维护 `hookApi: 2`；不提供旧版/blessing 插件兼容层。

## 13. 给 AI 的提示词模板

> 为 SkinNexus 插件宿主 v2 写一个插件：目录 `<name>/` 含 `plugin.json`（必要时加 `ui/` 与 `index.js`）。
> 约束：`hookApi=2`；只声明必需权限（config / store:kv / store:docs / ui:forms / media:avatar /
> http:routes:session / net:http / schedule:jobs 等）；数据只用 `skin.store`，配置只用 `skin.config`，
> 路由必须与 manifest `routes` 完全一致；外部请求只能走 `skin.net.get` 并在 manifest `net.http` 声明 host/path；
> 长任务只能用 `skin.jobs` 的 step + cursor，不得把循环爬取塞进一次请求；UI 页面/插槽用 iframe + postMessage bridge，表单字段用 `ui.forms`（不收集密码/密钥）；
> guard 必须显式 `onError`；头像外链 host 必须写进 `media.avatarHosts`；
> 过滤器/事件 fail-open，不改变 Yggdrasil 的 204/错误体/签名顺序。产物需通过 manifest 校验并在管理面板启用。
