# SkinNexus — 仓库协作说明

> 供 AI agent / 新贡献者快速上手。本文是仓库地图与铁律的入口；专题文档见文末表格。
> 部署者视角的文档在 `docs/build.md`（构建）、`docs/deploy.md`（部署）、`docs/migration.md`（迁移）。

## 结构

```
server/         Go 后端（模块 skin-nexus/server）
  cmd/skin-nexus/     入口：路由组装（yggdrasil / oidc / union / api/v1 / admin / public / textures / avatar / preview / SPA）
  internal/config     运行时配置（全部 SKIN_* 环境变量的唯一入口）
  internal/ygg        Yggdrasil 协议核心（models/service/http/signer）—— 兼容面冻结区
  internal/oidc       yggc/OIDC 免密登录内置（zitadel/oidc 内核 + 自持 discovery/userinfo/device 页 + JWT token 双态）
  internal/union      MUA Union 成员站模块（可开关：三头验签 / 入站出站 / OAuth2 grant / UnionSigner）—— wire 契约冻结区
  internal/auth       12 条旧密码算式 + argon2id + AuthSource 注册表
  internal/texture    上传管线（IHDR 门禁→解码→重编码→内容寻址）+ 存储
  internal/avatargen  服务端头像/预览渲染（Go 立方体光栅化；RenderVersion 控制预览 URL 版本化）
  internal/migrate    迁移引擎（.env 解析 / uuid 五步清洗 / 纹理树审计 / import）
  internal/store      MySQL 实现（ygg.Store / oidc.Backend 端口 + web/admin 查询）
  internal/webapi     站内 API（注册/登录 session/上传/角色/衣柜/skinlib/union 用户面）
  internal/adminapi   管理 API（管理员账号会话 permission>0 门禁 + X-Admin-Token 机器/API 回退）
  internal/publicapi  公开只读 API + legacy/usm/CustomSkinAPI 兼容路由
  internal/appearance 站点外观（自定义 CSS/JS、公告消毒）
  internal/announce   公告 Markdown 双层消毒（goldmark + bluemonday）
  internal/texturemd  材质描述 Markdown（更严格的 UGC 策略：禁图片/iframe）
  internal/seo        SEO 注入 / sitemap / robots
  internal/accesslog  访问/通信日志旁路记录器（C1）
  internal/mailer     SMTP 发信（A1 找回密码）
  internal/theme      主题包注册与加载（docs/theme-spec.md）
  internal/i18n       服务端多语言资源
  internal/ratelimit  认证限流（滑动窗口 + 失败锁定）
  internal/plugin     goja 宿主 v2（manifest/config/secrets/KV/docs/事件/guard/UI/三档路由/签名安装）+ signedpkg/marketfmt/outbound
  cmd/skin-plugin     离线打包/签名/registry/校验 CLI（私钥不入仓）
  migrations/         DDL（InnoDB utf8mb4；0001 基础 … 0017 材质元数据/举报，追踪式逐文件记标）
web/            Vue 3 + Vite（npm ci && npm run build → web/dist）
docs/           构建 / 部署 / 迁移 / 兼容矩阵 / 竞品证据 / 主题规范 / CHANGELOG（公开）；
                开发过程文档在上级 docs/（本地，永不入库）
```

## 铁律（拍板记录不随仓库分发）

1. **`/api/yggdrasil` 全路径原语义不可破**：204 清单、错误体 `{error,errorMessage}`、textures 空对象 `{}`、毫秒时间戳、PEM 严格格式——改动必须先跑 `go test ./internal/ygg/` 且不动契约断言。
2. **UUID 零漂移**：`OfflineUUID` 逐字节复刻旧公式；改名不重算。
3. **取证实测优先**：官方文档可能过时，以源码/线上 curl 为准。
4. 隐私：任何文件/提交不得出现真实姓名，署名用 `YMZS (YimingZhanshen)`。
5. **每轮开发收尾同步文档状态**：更新 README「状态」一句话摘要 + `docs/CHANGELOG.md` 逐轮条目，并同步受影响的必要文档（`docs/deploy.md` 等），过时即改、不留待办口吻的死条目——防止过时文档污染后续会话记忆。
6. **wire 契约冻结区零改动**：`internal/union` 的字段名/头名/拼接顺序与 hub 互操作冻结（见该包 doc 注）；`internal/plugin` hook 合约同理。确需变更先开偏差备案。
7. **数据兼容铁律（2026-10-23 拍板）**：任何持久化数据（表/列/字段语义）必须满足二者之一——①与 blessing-skin-server 及其插件生态的存量数据**同构可直迁**；②`internal/migrate` 提供**无损导入适配**。对既有代码与未来开发同时生效：密码凭证保持 12 算式可验证 + argon2id 惰性重哈希；新功能先考古老站存储约定（如 `invitation_codes.description`）再建表，不确定老站怎么存的先实测/读线上源码，不自造结构。

## 协作纪律（人类与 agent 同等）

- **任务粒度**：一个 agent 一个 PR 级任务，写范围（write scope）不重叠；越界问题只记 issue，不顺手改。
- **证据铁律**：结论附 `文件:行号` 或命令输出；「不存在」类结论必须穷尽检索；推断与事实分开陈述。
- **验证门**：每步贴真实输出；失败即停、最多重试 1 次；不以「应该可以」代替验证，契约测试与前端构建是合并门禁。
- **文档先行**：协议/接口变更先改 `docs/` 再改代码；分支模型、提交格式与 PR 要求见 `CONTRIBUTING.md`。
- **重大选型**：多 AI 独立评析 → 分歧对齐 → 发起人终裁。

## 常用命令

```bash
# 后端（Windows 开发机，GOPROXY 用国内源）
cd server
go build ./... && go vet ./... && go test ./...

# 前端
cd web && npm ci && npm run build

# MySQL 集成测试（需可达的测试库）
SKIN_TEST_DSN='user:pass@tcp(127.0.0.1:3306)/skin_nexus_test?parseTime=true' \
  go test -tags integration ./internal/store/

# 提交规范：feat|fix|chore(scope): 中文/英文摘要
```

构建产物、Docker、CI 详解见 `docs/build.md`；改动后自检的最短路径见其 §八。

## 接口契约优先

新代码通过既有端口（`ygg.Store`、`texture.Store`、`plugin.Host`）扩展，不直接散落 SQL——需要新查询时加方法到 `internal/store`。admin/web/public 各自独立 package，路由在 `cmd/skin-nexus/main.go` 统一组装。

## 面向部署者的文档入口

| 想做什么 | 读哪篇 |
|---|---|
| 从源码/Docker 构建 | `docs/build.md` |
| 新站部署、环境变量、反代、运维 | `docs/deploy.md` |
| 从 blessing-skin-server 迁数据、切流 | `docs/migration.md` |
| 检查与老站的协议兼容缺口 | `docs/compat-matrix.md` |
| 写/分发主题包 | `docs/theme-spec.md` |
| 参与贡献（翻译/主题/代码） | `CONTRIBUTING.md` |
| 了解每轮开发改了什么 | `docs/CHANGELOG.md` |
