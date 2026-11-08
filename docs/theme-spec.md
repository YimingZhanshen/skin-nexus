# SkinNexus 主题创作规范（theme-spec）—— 含 AI 生成提示词模板

> 面向任何人（包括让 AI 代劳）创作 SkinNexus 主题包。**零构建原则**：主题 = theme.json + 纯 CSS（+ 可选 JS），
> 不需要 Node/打包/编译，admin「外观」上传主题包 zip（热生效）或放入 `data/themes/<id>/` 并**重启服务**即生效。
> 加载器实现：`server/internal/theme/theme.go`（校验规则以代码为准，本规范与代码逐项对齐）。

## 1. 主题包布局

```
data/themes/<id>/
  theme.json      # 必需：清单（字段见 §2）
  style.css       # 常规入口样式（inject.css 引用）
  any-asset.png   # 可选资源：平铺放在包根目录（图片/字体等）
  preview.png     # 可选：预览图（预留，当前面板不展示）
```

- `<id>` 只能是 **小写字母/数字/连字符/下划线**，首字符为字母或数字（`^[a-z0-9][a-z0-9_-]*$`），**且必须与 theme.json 的 `id` 字段一致**。
- **资源文件平铺放包根目录、文件名全小写**（同上正则，允许点号扩展名）——`assets/` 之类的子目录**不可用**（资源路由只解析单层文件名，多段路径会 404）；CSS 里引用写 `url("bg.png")` 而非 `url("assets/bg.png")`。
- 目录穿越（`..`、绝对路径）会被加载器直接拒绝（白名单正则 + 路径前缀双重校验，无法读包外文件）。

## 2. theme.json 字段

```json
{
  "id": "dark-elegance",
  "name": "暗色优雅",
  "version": "1.0.0",
  "author": "your-name",
  "description": "一句话描述主题风格",
  "inject": { "css": ["style.css"], "js": [] },
  "palette": { "primary": "#6366f1" },
  "requires": { "skin_nexus": ">=0.1" }
}
```

| 字段 | 必需 | 说明 |
|---|---|---|
| `id` | ✅ | 与目录名一致（不一致整个包被跳过并记日志） |
| `name` | ✅ | 展示名 |
| `version` | ✅ | 语义化版本；同时用作 CSS/JS 链接的 `?v=` 缓存破除号（改样式请升版本号） |
| `inject.css` / `inject.js` | ✅ | 按序注入的包内文件列表（css 与 js 至少各有一个列表非空） |
| `author` / `description` | | 展示用 |
| `requires.skin_nexus` | | 版本约束，**加载器真实校验**：仅支持 `>=x.y`（可带 patch）；不满足则跳过该包并记日志；约束写错（如 `~1`）按不满足处理 |
| `palette` | | 声明式色板（文档/预览用） |
| `extends` / `slots` | | **预留字段**（解析但当前不生效，见 §5） |

> `name`/`version`/`inject` 是硬性必需——缺任何一个包会被跳过，日志（`docker compose logs skin-nexus`）
> 里有 `theme: skip ...` 原因；id≠目录名、JSON 坏、约束不满足同样只在日志可见。

## 3. CSS 变量契约（设计令牌）——优先改变量，别硬编码

SkinNexus 的应用样式定义了如下 CSS 自定义属性；**主题覆盖它们即可换整套配色**：

```css
:root {
  --bg: #101825;          /* 页面背景 */
  --panel: #16202e;       /* 面板/卡片背景 */
  --border: rgba(169, 138, 95, .24); /* 边框 */
  --text: #e9edf1;        /* 主文字 */
  --muted: #a8b4c3;       /* 次要文字 */
  --accent: #b0453a;      /* 主题色（按钮/链接/高亮） */
  --danger: #d9564c;      /* 错误/危险 */
}
```

> 上面七个默认值是 `server/cmd/skin-nexus/theme-base.css` 里 `:root` 的抄本，
> `scripts/check-theme-contract.mjs` 会逐键比对两者（CI `docs` job）。真源改了而这里没改，
> 主题作者就会照着过期色板写包——所以这条闸不许放宽，只能两边一起改。

另有一个应用侧局部变量可覆盖：`--fg`（局部文字色，消费点自带 `inherit` 回退，只在少数组件里引用）。
其余 `--text-dim` 之类的名字**没有宿主消费点**，覆盖了没有任何效果——只认 §3 列出的契约键与派生键。

**注入顺序保证（2026-10-25 起）**：主题 CSS → 自定义 CSS，且两者都注入在应用编译产物
CSS **之后**（同特异性后定义者胜）——所以平实的 `:root { --bg: ... }` 就能生效。
应用 CSS 必须单 bundle 在 `<head>` 中先加载；异步页面 chunk 只拆 JS，不再注入页面 CSS
（否则后注入的 scoped 样式会在同特异性下压过主题，2026-10-31 azure-glass 管理 dock 回归）。
无需 `!important`。访客首页（landing）消费**独立的落地页 token 命名空间**（见文末
「落地页 token 命名空间」专节），主题切换即整站换肤。
变量集以 `server/cmd/skin-nexus/theme-base.css` 的实际定义为准（AI 生成前可对照最新版）。

**`color-scheme` 也要自己声明**（`:root { color-scheme: light }` 或 `dark`，与你的 `--bg` 明暗一致）：
插件页的 HTML 由宿主注入本主题的**根令牌**，`color-scheme` 是其中被带过去的一条；主题不写它，
插件自己那句 `:root { color-scheme: dark }` 就留下——浅色主题配深色滚动条与表单控件。

**令牌必须写在 `:root` 或 `html` 上**：宿主只把这两个选择器上的自定义属性（加 `color-scheme`）
交给插件页，写在 `body` 或其它选择器上的令牌进不了沙箱文档，那一页会先画插件默认色、
等 JS 桥到达才纠正。反过来，你样式表里的裸元素规则（`body{background-image}`、`h1,h2{font-family}`、
`input{font-size}`、`table{}`、`th,td{}`）**不会**漏进插件页——插件的字体与排版由插件自己决定，
这是有意为之，别指望主题能改它们。宿主桥取的九个源令牌是
`--bg --panel --border --text --muted --accent --jade --danger --ok`（链接取 `--jade`、成功色取 `--ok`，
这两个在 §11 的派生令牌里，别忘了覆盖）。

## 4. 选择器稳定区（安全区）

✅ **允许**：基于语义类名与 data 属性样式化：

- 全局元素/类：`body`、`h1/h2/h3`、`label`、`input`、`textarea`、`select`、`table`、`th`、`td`、`a`、
  `button`（含 `button.secondary`、`button.danger`、`button.ghost`、`button.danger-outline`）、
  `.card`、`.badge`、`.muted`、`.error`、`.success`、
  `.container`、`.container.wide`、`.row`、`.crumb`、
  `.site-footer`、`.site-footer-links`、`.site-footer-text`、`.site-footer-credit`
- 组件类（注意下方特异性说明）：`.nav-item`、`.nav-title`、`.nav-group`、`.user-shell`、`.user-side`、
  `.user-main`、`.admin-nav-item`、`.tab`、`.md`（公告 Markdown，内含 `:deep(a)` / `:deep(code)` 风格）、
  `.ali`、`.avatar-box`、`.avatar-empty`、`.danger-text`、`.cell-input`、`.uploader`、`.stat-card`、`.stat-label`、`.like-btn`

❌ **禁止**：依赖 Vite 构建生成的 hash class（如 `index-BlotKZVo`）——它们随构建变化，主题会碎。

页脚内容由后台「外观 → 页脚」统一配置（文字 + 友链/备案链接），SSR 与 SPA 使用同一
`.site-footer` 结构；主题只能样式化，不替代配置数据。

**特异性说明**：带 `data-v-…` 的组件内类（scoped 样式）特异性是 (0,2,0)，主题同名类选择器 (0,1,0)
打不过——覆盖 `.nav-item`、`.tab`、`button.ghost` 等组件类时需提高特异性（如 `.card .tab`、`html .tab`）
或在属性冲突时用 `!important`；变量覆盖（§3）不受此限。

## 5. 插槽协议（预留）

`slots` 声明可替换区块，值为 HTML 片段（建议 key 命名 `<区域>_<子块>`，如 `home_hero`、`footer_note`）：

```json
"slots": { "home_hero": "<h1>欢迎来到我的皮肤站</h1>" }
```

宿主解析这两个字段但**不渲染**：写了不报错，也不会改变页面。要改布局或交互请用 admin「外观」的自定义 CSS/JS（§6），要做站内功能区块请用插件 slot（见 plugin-spec.md）。`extends`（父主题继承）同样解析、不生效。

## 6. 安全边界与 JS 时机

- 主题 JS 与 admin 自定义 JS **同一信任面**：原样注入、无沙箱——只安装/使用你信任的主题。
- 主题包不包含任何服务端代码；加载器只读 theme.json + inject 列出的文件 + 平铺资源。
- 分发主题时请附带源码仓库链接，社区主题建议开源（MIT/GPL 均可）。
- **JS 时机**：主题 JS 在 `<head>` 同步加载，自定义 JS 在 `</body>` 前内联——两者都在 Vue
  应用挂载**之前**执行，直接 `querySelector` 应用界面会抓空。要操作界面 DOM 一律包一层：

  ```js
  document.addEventListener('DOMContentLoaded', () => {
    // 此时应用已挂载，可安全操作 DOM
  });
  ```

## 7. 安装、市场与生效

### 7.1 主题市场与本地上传

官方主题注册表：`https://yimingzhanshen.github.io/skin-nexus-plugins/themes/v1/registry.json`
（源仓库 `themes/<id>/`，与插件市场同仓不同路径 + **独立 Ed25519 密钥**
`theme-market-2026-11`——一把钥匙泄露不会同时沦陷两个市场）。地址与公钥都**编译内置**，
零配置即开箱可用；要换源或关闭才需要设环境变量：

| 环境变量 | 作用 |
|---|---|
| `SKIN_THEME_MARKET_URL` | 注册表地址；显式置空 = 关闭市场浏览（上传仍可用） |
| `SKIN_THEME_TRUST_KEYS` | `id:base64pub,...` 钉死的验签公钥；显式置空 = 市场安装关闭 |

管理面板「外观」内置 **主题市场**（目录 / 安装 / 更新 / 卸载）与 **本地主题包上传**
（zip ≤16 MiB，两步：检查 → 确认安装）。要点：

- 市场包四道校验：注册表签名 → 制品 size/sha256 → 包内 `bundle.json` 验签 →
  `theme.json` 结构校验（id 一致、name/version/inject 必填、`requires.skin_nexus` 满足）。
- **热生效**：安装/卸载即时重扫主题注册表，无需重启；正在使用的主题禁止卸载（先切换）。
- 未签名本地包 = 管理员信任面（与自定义 JS 同权），需显式勾选风险确认；
  安装/卸载/上传仅超级管理员网页会话可操作（X-Admin-Token 拒绝 + 同源 CSRF 闸）。
- 发布方工具：`skin-plugin pack-theme --dir <id>/ --out artifacts --key … --key-id …`
  与 `skin-plugin registry --kind theme …`（确定性 zip，同版本字节级不可变）。
- 打包规范：`theme.json` 在压缩包根（或单层顶层目录内）；资源文件平铺包根、
  文件名全小写（同 §1 约束）。

### 7.2 手工安装 / 切换 / 卸载 / 调试

- **安装**：首选 admin「外观」→ 主题市场安装或本地主题包上传（**热生效，无需重启**）；
  或把包目录手工放入 `data/themes/<id>/` → **重启服务**（手工落盘不触发重扫）→
  admin「外观」下拉选中保存即生效（保存即时，无需再重启）。
- **切换**：admin「外观」选其它主题保存；选「无（默认样式）」回到默认。
- **卸载**：admin「外观」→ 主题市场卸载（热生效，正在使用的主题禁止卸载，需先切换）；
  或手工删除包目录 + 重启；若 `active_theme` 指向已删除的包，表现为「无主题」（不报错）。
- **改样式调试**：改 CSS 后浏览器可能吃缓存（资源缓存 86400s）——把 theme.json 的 `version`
  递增（链接自动带 `?v=` 破缓存）或强制刷新（Ctrl+F5）。
- **排查清单**：① 查看网页源码，确认 `<!--skin-theme-->` 等标记已被替换（看到 `/theme-assets/...`
  链接即注入成功）；② 浏览器 Network 确认 `/theme-assets/<id>/<file>` 返回 200（404 = 文件名
  不合规则/不在包根）；③ DevTools → Computed 看 `--bg` 等变量是否你的值；④ 深链接（刷新一次
  任意页面）确保拿的是注入后的页面；⑤ 还不行看服务日志 `theme: skip ...` 原因。

## 8. 高级：自定义 head HTML

`custom_head_html`（admin 外观配置，随自定义 CSS 之后注入 `<head>`）可贴任意 head 片段
（`<link>`/`<meta>`/`<script>` 等）——**能贴什么由本站 CSP 决定，不是由这个字段决定**：
`script-src 'self' 'unsafe-inline'`、`style-src 'self' 'unsafe-inline'`、`font-src 'self' data:`。
所以内联 `<script>`/`<style>` 与**站内自托管**的 `.js`/`.css`/字体都可以（favicon 属于
`img-src`/文档请求，同样没问题）；而 `src="https://某个统计站/…"` 这种**第三方 origins 的脚本、
样式表与字体一律被浏览器拒掉**——控制台会报 CSP violation，页面上不留痕迹地不执行。要挂第三方统计，
要么把它的脚本自托管到站点里，要么在反代最外层改 CSP（改法见 deploy.md §3「安全响应头」，两处只配一层）。
适合的场景因此是：自托管资源、结构化数据、`<meta>`、内联小段脚本。当前无独立输入框，
可经 admin 外观 API（`POST /api/admin/v1/appearance`，字段 `custom_head_html`）设置。
与自定义 CSS/JS 同一信任面。

**长度上限**：自定义 CSS/JS/head HTML 存于数据库 TEXT 列，实用上限约 64KB（请求体上限 1MiB）。

## 9. 🤖 给 AI 的完整提示词模板（直接粘贴给任意 LLM）

```
请为 SkinNexus 皮肤站生成一个主题包，要求输出两个文件：

1. theme.json —— 遵循以下 schema：
   {"id":"<小写字母数字连字符下划线，如 my-theme>","name":"<中文展示名>","version":"1.0.0",
    "author":"<作者>","description":"<一句话描述>",
    "inject":{"css":["style.css"],"js":[]},"palette":{"primary":"<主色>"}}

2. style.css —— 纯 CSS，无需编译。规则：
   - 优先覆盖 CSS 变量，不要硬编码颜色到具体选择器：
     --bg（页面背景）、--panel（面板/卡片背景）、--border（边框）、--text（主文字）、
     --muted（次要文字）、--accent（主题色）、--danger（错误/危险）；
     局部变量 --fg、--text-dim 可选覆盖
   - 只使用语义选择器：.card、.badge、.muted、.error、.success、
     button（含 button.secondary、button.danger）、.ghost、.danger-outline、
     table、input、select、a、h1/h2。禁止使用构建生成的 hash class。
   - 覆盖带 data-v 属性的组件类（.nav-item/.tab/.ghost 等）时提高特异性或用 !important。
   - 风格描述：<把你的需求写这里，例如「暗紫色赛博朋克风、大圆角、柔和阴影」>
   - 保持可读性：正文对比度不低于 4.5:1。
   - 不要引入外部资源；如需字体只用系统字体栈。
   - 尺寸走宿主刻度，别自造一档：字号 `--fs-xs…--fs-2xl`、间距 `--sp-1…--sp-8`、
     圆角 `--r-s/--r-m/--r-l`、层级 `--z-topbar/--z-menu/--z-toast/--z-modal`、
     等宽 `--mono`（id/哈希/时间戳）。一个包里同时出现 11/13/13.5/15/15.5px 就是随手敲的产物。
   - 层次不要画成颜色：`--glow-*` 与 `--shadow*` 保持中性，暗底靠面阶 + 发丝线 + 顶边高光分离；
     循环播放的装饰动画（背景漂移、呼吸光）在系统减少动效外还要自己关。
   - **渐变字的坑**：`background-clip:text` + `-webkit-text-fill-color:transparent` 是一个整体。
     把渐变色板换成实色时必须整块删掉，否则背景实心、文字透明——不报错、不告警，只是看不见。

输出格式：两个代码块分别标注 theme.json 和 style.css，不要额外解释。
```

**安装与生效要点（生成后执行）**：① 目录名必须等于 theme.json 的 `id`（全小写）；
② 打包成 zip 走 admin「外观」→ 本地主题包上传（热生效、免重启），或放入
`data/themes/<id>/` 后**重启服务**才识别，面板选中保存才生效；③ 改样式后升
`version` 或强刷缓存；④ 资源文件平铺放包根、全小写；⑤ `extends`/`slots`/`requires`
里前两个当前不生效、`requires` 生效（写 `">=0.1"` 即可）；⑥ 如需挂 head 内容（自托管统计、
`<meta>`、结构化数据），用 `custom_head_html`（admin 外观 API）而不是主题 JS；**第三方 origins
的脚本/样式/字体会被本站 CSP 拒绝**，边界见 §8。

生成后：按上方「安装」步骤部署，admin 面板「外观」选择该主题保存即生效。

## 10. 落地页 token 命名空间

访客落地页（`/`，服务端渲染）采用自有设计语言「月下仙山」（中国古风：黛青底 / 月白字 /
朱砂主按钮 / 赭石纹线），token 键为：`--ink` `--ink-2` `--ink-3` `--line` `--moon`
`--moon-2` `--cinnabar` `--cinnabar-hi` `--on-cinnabar` `--ochre` `--font-serif`。

注入顺序不变：主题包与 admin 自定义 CSS 仍在落地页内联样式**之后**注入，可按上述新键
整体覆盖落地页配色；旧键 `--bg/--panel/--text/--accent` **不影响落地页**。主题若要适配落地页，
覆盖这些新键即可。

**SSR 侧不许手抄令牌值**（2026-11-09 起为硬要求，此前详情页 404 复制过一份 `:root`、
落地页手抄过雾色，都属漂移）。落地页与详情页要颜色就消费令牌：雾 `var(--fog)`、
远山 `var(--mtn-far|--mtn-mid|--mtn-near)`、错误文字 `var(--danger-ink, var(--danger))`；
404 页与另两面一样注入 `theme-base.css` 的令牌块，自身只留排版规则。SVG 里 `fill=` /
`stop-color=` 这类**呈现属性不解析 `var()`**，要跟着主题走就用 class 上色（`.mtn-far{fill:var(--mtn-far)}`），
这与 SPA 场景板同一写法。`<meta name="theme-color">` 必须是字面色值，是令牌块之外唯一
被允许的浏览器色，改 `--bg` 时记得同步它。

## 11. v3 全站设计体系（派生令牌速查）

默认皮肤为国画色系（黛青底 / 月白字 / 朱砂主操作 / 青瓷链接 / 赭金警示）。
**token 架构（主题作者必读）**：旧 7 契约键（`--bg/--panel/--border/--text/--muted/--accent/--danger`）
仍是唯一真源——默认值换成国画色而已；**新增语义键全部别名到契约键**
（`--ink/--ink-2/--line/--moon/--moon-2/--cinnabar` 等）。主题覆盖旧键时新键自动跟随，
旧主题不需要任何改动即可兼容；新主题建议同时覆盖以下派生键以求完整：

`--ink-3`（嵌套面/悬停底）· `--line-strong`（强描线）· `--jade`（链接/选中/健康）·
`--jade-hi` · `--gold`（警示）· `--cinnabar-hi`（主按钮 hover）· `--on-cinnabar`（主按钮上的字）·
`--danger-hi` · `--danger-ink`（面板上的错误文字，比 `--danger` 提亮到 AA）· `--ok` ·
`--hover-wash`（表格行悬停薄雾）· `--r-s/--r-m/--r-l`（圆角三档，现值 6/10/14）·
`--shadow/--shadow-lift`（层次两层）· `--mono`（等宽栈，id/哈希/时间戳）· `--ease/--t-fast/--t-mid`（动效）。

**层次不用颜色画**：`--glow-cinnabar` / `--glow-danger` 现值是中性墨色，`--shadow` 是
`inset` 顶边高光——暗底上的分离靠面阶与发丝线。键名保留着（已上架的包在覆盖它们，
改名等于让那些覆盖静默变死变量），但**不要**把它们填回品牌色彩晕。

**v3.3 补充键**：派生别名 `--panel-2`（嵌套面板面，默认
`--ink-3`；皮肤卡预览/进度槽）与 `--accent-ink`（主色底文字，默认 `--on-cinnabar`）；
间距刻度 `--sp-1…--sp-8`（4/8/12/16/24/32/48/64px）与层级刻度 `--z-topbar/--z-menu/--z-toast/--z-modal`
（50/60/70/80）——布局类建议按刻度取值但主题可不覆盖。徽章改走变体类
`.badge.danger/.ok/.jade/.gold/.dim`，不再用内联 style 改色（主题覆盖 `.badge` 即联动）。
SSR 落地页/详情页与 SPA 共用同一组字号与动效令牌（`--fs-*`、`--t-*`），值漂移视为 bug。
`:root` 令牌块本体在 **`server/cmd/skin-nexus/theme-base.css`**（唯一真源）：
SPA 经 `web/src/style.css` `@import` 引入，SSR 两页 `//go:embed` 注入 `<style>` 首段；
主题包覆盖契约键的机制不受影响（注入点仍在 `<!--skin-theme-->` 之前）。

**新结构类（布局契约，主题可覆盖）**：`.topbar`（sticky 玻璃顶栏，高 `--topbar-h`）、
`.auth-split/.auth-scene/.auth-pane`（登录/注册/找回/重置的分栏场景布局，≤860px 塌缩）、
`.user-chip`（侧栏底部用户卡）、`.nav-item .nav-ico`（图标导航，活动态朱砂左条）、
`.page-head`（页面标题行）。已适配主题：azure-glass / ember-forge / verdigris-atelier
（源码在分发仓库 themes/<id>/）。

**红线不变**：logo（N 字牌）为品牌资产，任何主题不得改其配色；变色须全品牌资产统一并由站主终审。

## 12. 主题包能力边界

**主题拥有布局与排版的完全改造权**——主题包的存在意义就是把站点改成完全另一个样子（样板：官方
azure-glass 3.3.0，默认亮色、一条中性阶加一个蓝主色、实面卡片 + 发丝线、玻璃只留 dock 与左右侧栏、
顶部 dock、居中宽幅）。

**可以改（全放开）**：布局方向与栅格（行/列/sticky/宽度/不对称偏移）、排版（字体栈/字阶/字距/渐变字）、色板（契约键 + v3 派生键全量覆盖）、组件形态（圆角/胶囊/玻璃/阴影/边框/斑马纹/发丝线）、背景与装饰、动效时长与曲线、明暗模式策略（如 html.azg-dark 双态）。
这一句是**能力清单，不是推荐做法**：渐变字与满屏玻璃拟态是 2026-11-09 那轮从官方 azure-glass 里撤掉的写法（撤的理由见包内文件头）；宿主不会拦，因为主题包的意义就是敢把站点改样。

**不可改（底线四条）**：
1. **logo 品牌资产**：N 字牌配色不动（变色须全品牌资产统一并由站主终审）；
2. **内容语义**：不改文案、不伪造状态、不隐藏必要信息（如错误/安全提示）；
3. **注入锚点**：`<!--skin-seo-->` / `<!--skin-theme-->` / `<!--skin-custom-head-->` / `<!--skin-custom-body-->` 结构不动；
4. **可访问性底线**：`:focus-visible` 可见、正文对比度 ≥4.5:1、`prefers-reduced-motion` 被尊重、不引入仅装饰性 emoji 图标。

**共存契约（改结构时的三条工程纪律）**：① sticky 元素为 v3 顶栏让位——`top: calc(var(--topbar-h, 56px) + 间距)`，z 层级低于顶栏（50）；② v3 新结构类（`.user-chip/.nav-ico/.auth-split/.drop-zone` 等）在主题布局里要有合理位置，不得遗留半吊状态；③ 与全站样式对撞时**修共存、不砍结构**（逐点修高度/z/间距/换行），不撤销对方的设计意图。
