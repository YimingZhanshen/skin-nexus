# SkinNexus 主题创作规范（theme-spec）—— 含 AI 生成提示词模板

> 面向任何人（包括让 AI 代劳）创作 SkinNexus 主题包。**零构建原则**：主题 = theme.json + 纯 CSS（+ 可选 JS），
> 不需要 Node/打包/编译，放入 `data/themes/<id>/` 并**重启服务**即生效。
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
  --bg: #0f1115;          /* 页面背景 */
  --panel: #171a21;       /* 面板/卡片背景 */
  --border: #262b36;      /* 边框 */
  --text: #d7dae0;        /* 主文字 */
  --muted: #8b93a1;       /* 次要文字 */
  --accent: #4f8cff;      /* 主题色（按钮/链接/高亮） */
  --danger: #e5534b;      /* 错误/危险 */
}
```

另有两个应用侧局部变量（可一并覆盖）：`--fg`（局部文字色，消费点自带 fallback）、
`--text-dim`（弱化文字色，皮肤库页裸引用——覆盖它可直接生效）。

**注入顺序保证（2026-10-25 起）**：主题 CSS → 自定义 CSS，且两者都注入在应用编译产物
CSS **之后**（同特异性后定义者胜）——所以平实的 `:root { --bg: ... }` 就能生效。
应用 CSS 必须单 bundle 在 `<head>` 中先加载；异步页面 chunk 只拆 JS，不再注入页面 CSS
（否则后注入的 scoped 样式会在同特异性下压过主题，2026-10-31 azure-glass 管理 dock 回归）。
无需 `!important`。访客首页（landing）消费**独立的落地页 token 命名空间**（见文末
「落地页 token 命名空间」专节），主题切换即整站换肤。
变量集以 `web/src/style.css` 的实际定义为准（AI 生成前可对照最新版）。

## 4. 选择器稳定区（安全区）

✅ **允许**：基于语义类名与 data 属性样式化：

- 全局元素/类：`body`、`h1/h2/h3`、`label`、`input`、`textarea`、`select`、`table`、`th`、`td`、`a`、
  `button`（含 `button.secondary`、`button.danger`）、`.card`、`.badge`、`.muted`、`.error`、`.success`、
  `.ghost`、`.danger-outline`、`.container`、`.container.wide`、`.row`
- 组件类（注意下方特异性说明）：`.nav-item`、`.nav-title`、`.nav-group`、`.user-shell`、`.user-side`、
  `.user-main`、`.admin-nav-item`、`.tab`、`.md`（公告 Markdown，内含 `:deep(a)` / `:deep(code)` 风格）、
  `.ali`、`.avatar-box`、`.avatar-empty`、`.danger-text`、`.cell-input`、`.uploader`、`.stat-card`、`.stat-label`、`.like-btn`

❌ **禁止**：依赖 Vite 构建生成的 hash class（如 `index-BlotKZVo`）——它们随构建变化，主题会碎。

**特异性说明**：带 `data-v-…` 的组件内类（scoped 样式）特异性是 (0,2,0)，主题同名类选择器 (0,1,0)
打不过——覆盖 `.nav-item`、`.tab`、`.ghost` 等组件类时需提高特异性（如 `.card .tab`、`html .tab`）
或在属性冲突时用 `!important`；变量覆盖（§3）不受此限。

## 5. 插槽协议（预留）

`slots` 声明可替换区块，值为 HTML 片段（建议 key 命名 `<区域>_<子块>`，如 `home_hero`、`footer_note`）：

```json
"slots": { "home_hero": "<h1>欢迎来到我的皮肤站</h1>" }
```

当前版本字段已解析但**不渲染**；待渲染端落地后另行公告。`extends`（父主题继承）同为预留。

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

## 7. 安装 / 切换 / 卸载 / 调试

- **安装**：包目录放入 `data/themes/<id>/` → **重启服务**（注册表仅启动时扫描，不重启不识别）→
  admin「外观」下拉选中保存即生效（保存即时，无需再重启）。
- **切换**：admin「外观」选其它主题保存；选「无（默认样式）」回到默认。
- **卸载**：删除包目录 + 重启；若 `active_theme` 指向已删除的包，表现为「无主题」（不报错）。
- **改样式调试**：改 CSS 后浏览器可能吃缓存（资源缓存 86400s）——把 theme.json 的 `version`
  递增（链接自动带 `?v=` 破缓存）或强制刷新（Ctrl+F5）。
- **排查清单**：① 查看网页源码，确认 `<!--skin-theme-->` 等标记已被替换（看到 `/theme-assets/...`
  链接即注入成功）；② 浏览器 Network 确认 `/theme-assets/<id>/<file>` 返回 200（404 = 文件名
  不合规则/不在包根）；③ DevTools → Computed 看 `--bg` 等变量是否你的值；④ 深链接（刷新一次
  任意页面）确保拿的是注入后的页面；⑤ 还不行看服务日志 `theme: skip ...` 原因。

## 8. 高级：自定义 head HTML

`custom_head_html`（admin 外观配置，随自定义 CSS 之后注入 `<head>`）可贴任意 head 片段
（`<link>`/`<meta>`/`<script>` 等），适合挂统计脚本、字体、favicon——当前无独立输入框，
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

输出格式：两个代码块分别标注 theme.json 和 style.css，不要额外解释。
```

**安装与生效要点（生成后执行）**：① 目录名必须等于 theme.json 的 `id`（全小写）；
② 放入 `data/themes/<id>/` 后**重启服务**才识别，面板选中保存才生效；③ 改样式后升
`version` 或强刷缓存；④ 资源文件平铺放包根、全小写；⑤ `extends`/`slots`/`requires`
里前两个当前不生效、`requires` 生效（写 `">=0.1"` 即可）；⑥ 如需挂统计/字体等 head
内容，用 `custom_head_html`（admin 外观 API）而不是主题 JS。

生成后：按上方「安装」步骤部署，admin 面板「外观」选择该主题保存即生效。

## 落地页 token 命名空间（2026-10-26）

访客落地页（`/`，服务端渲染）采用自有设计语言「月下仙山」（中国古风：黛青底 / 月白字 /
朱砂主按钮 / 赭石纹线），token 键为：`--ink` `--ink-2` `--ink-3` `--line` `--moon`
`--moon-2` `--cinnabar` `--cinnabar-hi` `--on-cinnabar` `--ochre` `--font-serif`。

注入顺序不变：主题包与 admin 自定义 CSS 仍在落地页内联样式**之后**注入，可按上述新键
整体覆盖落地页配色；旧键 `--bg/--panel/--text/--accent` **不再影响落地页**。主题若需
适配落地页，写新键的覆盖即可（详见 CHANGELOG 当轮记录）。

## v3 全站设计体系（2026-10-26「月下仙山」大改）

全站默认皮肤升级为国画色系（黛青底 / 月白字 / 朱砂主操作 / 青瓷链接 / 赭金警示）。
**token 架构（主题作者必读）**：旧 7 契约键（`--bg/--panel/--border/--text/--muted/--accent/--danger`）
仍是唯一真源——默认值换成国画色而已；**新增语义键全部别名到契约键**
（`--ink/--ink-2/--line/--moon/--moon-2/--cinnabar` 等）。主题覆盖旧键时新键自动跟随，
旧主题不需要任何改动即可兼容；新主题建议同时覆盖以下派生键以求完整：

`--ink-3`（嵌套面/悬停底）· `--line-strong`（强描线）· `--jade`（链接/选中/健康）·
`--jade-hi` · `--gold`（警示）· `--cinnabar-hi`（主按钮 hover）· `--on-cinnabar`（主按钮上的字）·
`--danger-hi` · `--ok` · `--hover-wash`（表格行悬停薄雾）· `--r-s/--r-m/--r-l`（圆角三档）·
`--shadow/--shadow-lift`（阴影两层）· `--ease/--t-fast/--t-mid`（动效）。

**新结构类（布局契约，主题可覆盖）**：`.topbar`（sticky 玻璃顶栏，高 `--topbar-h`）、
`.auth-split/.auth-scene/.auth-pane`（登录/注册/找回/重置的分栏场景布局，≤860px 塌缩）、
`.user-chip`（侧栏底部用户卡）、`.nav-item .nav-ico`（图标导航，活动态朱砂左条）、
`.page-head`（页面标题行）。已适配主题：azure-glass / ember-forge / verdigris-atelier
（各追加 §「v3 语义键适配」小节）。

**红线不变**：logo（N 字牌）为品牌资产，任何主题不得改其配色；变色须全品牌资产统一并由站主终审。

## 主题包能力边界（2026-10-26 终版规范，第三拍板）

**主题拥有布局与排版的完全改造权**——主题包的存在意义就是把站点改成完全另一个样子（样板：azure-glass v3「换脸」——默认亮色、超大渐变标题、居中宽幅（1180→1480 随屏宽）、玻璃卡、顶部 dock、全出血 hero，不看文字认不出同站）。当日口径演进如实记录：早前 v3.1 曾以「布局骨架归全站」为名把 dock 缴械（违背 v2 立项初心），当晚即返工并立此终版边界。

**可以改（全放开）**：布局方向与栅格（行/列/sticky/宽度/不对称偏移）、排版（字体栈/字阶/字距/渐变字）、色板（契约键 + v3 派生键全量覆盖）、组件形态（圆角/胶囊/玻璃/阴影/边框/斑马纹/发丝线）、背景与装饰、动效时长与曲线、明暗模式策略（如 html.azg-dark 双态）。

**不可改（底线四条）**：
1. **logo 品牌资产**：N 字牌配色不动（变色须全品牌资产统一并由站主终审）；
2. **内容语义**：不改文案、不伪造状态、不隐藏必要信息（如错误/安全提示）；
3. **注入锚点**：`<!--skin-seo-->` / `<!--skin-theme-->` / `<!--skin-custom-head-->` / `<!--skin-custom-body-->` 结构不动；
4. **可访问性底线**：`:focus-visible` 可见、正文对比度 ≥4.5:1、`prefers-reduced-motion` 被尊重、不引入仅装饰性 emoji 图标。

**共存契约（改结构时的三条工程纪律）**：① sticky 元素为 v3 顶栏让位——`top: calc(var(--topbar-h, 56px) + 间距)`，z 层级低于顶栏（50）；② v3 新结构类（`.user-chip/.nav-ico/.auth-split/.drop-zone` 等）在主题布局里要有合理位置，不得遗留半吊状态；③ 与全站样式对撞时**修共存、不砍结构**（逐点修高度/z/间距/换行），不撤销对方的设计意图。案例存档：dock 事件（v3.1 缴械 → v3.2 返工）。
