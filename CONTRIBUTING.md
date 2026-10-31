# 贡献指南（CONTRIBUTING）

> **English TL;DR** — Contributions are welcome in three shapes: **translations**
> (add a language pack under `web/src/locales/`), **theme packs** (follow
> [`docs/theme-spec.md`](docs/theme-spec.md); themes are distributed outside this
> repo), and **code / docs** (fork → feature branch → PR against `main`; must pass
> CI and the compatibility iron rules in [`AGENTS.md`](AGENTS.md)). By
> contributing you agree to license your work under **AGPL-3.0**. AI-assisted
> contributions are welcome — the human submitter is responsible for verifying
> the output. Details below (Chinese).

## 0. 总则

- 本项目由 **YMZS（[GitHub @YimingZhanshen](https://github.com/YimingZhanshen)）** 发起并享有原始著作权，治理模式 BDFL（见 [`GOVERNANCE.md`](GOVERNANCE.md)）：日常改动走 PR，合并权归发起人。
- 贡献即同意以 **AGPL-3.0** 授权你的改动（见 [`LICENSE`](LICENSE)）。
- **隐私铁律**：任何提交不得出现真实姓名、真实站点域名、真实 IP 等隐私信息；示例一律用 `skin.example.com` / `legacy` 之类占位。署名只用 `YMZS (YimingZhanshen)`。
- 欢迎 AI 辅助贡献，但**提交者对输出负全责**：贴真实验证输出（测试/构建结果），不以「应该可以」代替验证（证据铁律，见 [`AGENTS.md`](AGENTS.md) §协作纪律）。

## 1. 语言翻译（i18n）

界面文案全部键值化，前端语言包在 `web/src/locales/`（`zh-CN.json` 为键基准，
`en.json` 为完整样例）。新增一门语言（以 `ja` 为例）：

1. 复制 `web/src/locales/zh-CN.json` 为 `ja.json`，逐键翻译（**全量键对齐**，
   漏键会回退 zh-CN；含 `%{name}` 之类插值占位符的键保持占位符原样）；
2. 在 [`web/src/i18n.js`](web/src/i18n.js) 注册：`messages` 加 `'ja': ja`，
   并把语言代码加进 locale 保存值白名单（同文件 resolveLocale 判断处）；
3. 在 [`web/src/App.vue`](web/src/App.vue) 的语言下拉（`<option>` 列表）加一项；
4. 自检：`cd web && npm ci && npm run build`，起站后右上角切换语言逐页检查。

补充规则：

- 服务端只发错误码（`server/internal/i18n`），翻译全部落在前端语言包——改翻译**不需要动 Go 代码**；
- 术语沿用 `zh-CN.json` 既有译法（皮肤/披风/角色/衣柜/皮肤库/邀请码…），新概念先在 PR 描述里定译名；
- 翻译 PR 也走 CI（前端构建 job 会拦语法/引用错误）。

## 2. 主题包（UI 自定义）

主题包让皮肤站换整套视觉，规范见 [`docs/theme-spec.md`](docs/theme-spec.md)：

- **契约三件套**：`theme.json`（清单）+ `style.css`（覆盖 CSS 变量与稳定选择器）+ 可选 JS；
  CSS 变量契约（`--bg/--panel/--border/--text/--muted/--accent/--danger`）与稳定
  选择器安全区是主题的覆盖面保证——**优先改变量，少碰选择器**；
- **AI 生成**：把 theme-spec.md §9 的提示词模板整段粘给任意 LLM，产物打成 zip 走
  admin「外观」→ 本地主题包上传（热生效、免重启）；手工放 `<data>/themes/<id>/` 则需重启才被扫到；
- **主题包不进主仓**（社区独立分发，建议开源并附仓库链接；官方三包源码在分发仓库 `themes/<id>/`）；对**主题规范本身**
  的改进——新增稳定选择器/设计令牌、修 schema、补 AI 模板——欢迎直接 PR
  `docs/theme-spec.md`（会同步核对 `web/src` 实际消费点，规范必须与代码一致）；
- 想深度定制（改布局/交互）用 admin「外观」的自定义 CSS/JS（blessing 同款），
  与主题同一信任面、无沙箱——只装你信任的代码。

## 3. 插件（服务端扩展）

插件在服务进程内的 goja VM 中运行，规范见 [`docs/plugin-spec.md`](docs/plugin-spec.md)（v2：配置/密钥、命名空间数据、三档路由、事件/guard、sandbox iframe UI）：

- **单一 API 版本**：插件只支持 `hookApi: 2`（项目尚无存量生态，不维护旧版/blessing 兼容层）；契约以 `server/internal/plugin/contract.go` 为准；
- **签名与信任分级**：市场安装只接受官方 keyring 验签通过的 bundle；本地上传允许未签名/未知密钥包，
  但必须由超级管理员网页会话显式确认风险，且插件列表会永久标注来源与信任等级
  （`verified` / `unknown-key` / `unsigned` / `invalid`），详见 `docs/plugin-spec.md` §1；
- 示例插件在 `examples/plugins/`（`hello-world` / `hello-ui`，离线无依赖）；官方插件源码在独立仓库 `skin-nexus-plugins`。改动示例须保持 `go test ./internal/plugin/` 全绿；
- 插件**不进主仓分发**（与主题包同口径）；对**规范本身**的改进欢迎 PR `docs/plugin-spec.md`。

## 4. 代码与文档

### 4.1 开发环境与自检

见 [`docs/build.md`](docs/build.md)。最短路径：

```bash
cd server && go build ./... && go vet ./... && go test ./...   # 后端三连
cd web && npm ci && npm run build                              # 前端构建
# 涉及存储层再跑集成测试（需 MySQL 5.7 或 8.0 测试库）：
SKIN_TEST_DSN='user:pass@tcp(127.0.0.1:3306)/skin_nexus_test?parseTime=true' \
  go test -tags integration ./internal/store/
```

CI 会跑全量门禁（后端 build/vet/test、**MySQL 5.7 与 8.0 双矩阵**集成测试、
前端构建、Docker 镜像）——本地至少把上面三连跑绿再提 PR。

### 4.2 分支与提交

- trunk-based：从 `main` 拉短生命周期分支 `feat/<scope>-<desc>` / `fix/...` / `docs/...`；
- Conventional Commits（`feat|fix|docs|refactor|test|chore`），scope 用模块名
  （`auth` / `texture` / `ygg` / `migrate` / `web` 等），摘要中英文均可；
- 涉及数据库的改动必须带迁移脚本（`server/migrations/`，追踪式逐文件记标）；
- 一个 PR 一件事；越界问题只记 issue，不顺手改。

### 4.3 兼容红线（摘要，完整定义与理由见 [`AGENTS.md`](AGENTS.md) 铁律）

以下为冻结面，破坏性改动会被直接拒：

1. `/api/yggdrasil/**` 全路径原语义（204 清单、错误体形状、毫秒时间戳、PEM 格式）；
2. 玩家 UUID 零漂移（`OfflinePlayer:` md5 变体，改名不重算）；
3. 持久化数据与 blessing-skin-server 存储约定同构可直迁（数据兼容铁律）；
4. `internal/union` wire 契约与 `internal/plugin` hook 合约冻结；
5. 新代码走既有端口（`ygg.Store`、`texture.Store`、`plugin.Host`）扩展，不散落 SQL。

协议面改动请附**兼容性验证记录**（对照 [`docs/compat-matrix.md`](docs/compat-matrix.md)）；
响应语义变更须带 JSON 快照测试更新。

### 4.4 文档同步

- 接口/行为变更先改 `docs/` 再改代码（文档先行）；
- 修完顺手同步受影响文档（含 README 状态行与 [`docs/CHANGELOG.md`](docs/CHANGELOG.md) 的一句话），
  过时文档会污染后续开发记忆——这是本仓的硬要求。

## 5. 报告问题

Issue 请给：版本号或 commit sha、部署方式（Docker/源码）、复现步骤、
相关日志（`docker compose logs skin-nexus` 或 journalctl）。涉及协议兼容的
问题附客户端名与 authlib-injector 版本。**不要贴真实域名/账号/密码**。

## 6. 评审与合并

- PR 模板要素：动机 / 改动 / 测试（贴真实输出）/ 迁移影响；
- CI 绿 + 至少一人评审后由发起人合并；重大选型走「多 AI 评析 → 分歧对齐 → 发起人终裁」；
- 合并贡献进入 `git shortlog` 永久记录，重大贡献在 README 与 CHANGELOG 致谢。
