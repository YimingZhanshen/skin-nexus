# 扩展发布与签名市场（插件 + 主题）

本文面向作者/站长：如何生成签名 bundle、构建签名 registry、发布到 GitHub Pages，
以及生产站如何信任它。安装/运行时语义见 [`plugin-spec.md`](plugin-spec.md)，
主题包的产品语义见 [`theme-spec.md`](theme-spec.md) §7.1。

> 私钥只留在发布者离线环境。仓库、CI、生产服务器、registry 站点都只出现公钥。

**两个市场同一条链路，两套密钥**：插件走 `v1/` + `market-2026-10`，主题走 `themes/v1/` +
`theme-market-2026-11`，同一个分发仓库、同一套 CLI 与 CI 环境；一把钥匙泄露不会同时沦陷两个面。

## 0. 官方市场投稿（推荐）

官方插件源码统一在 <https://github.com/YimingZhanshen/skin-nexus-plugins>：

1. 在 `plugins/<name>/`（插件）或 `themes/<id>/`（主题）添加源码与 `market.json`；
2. 向该仓库提 PR —— CI 只运行 `skin-plugin lint`（无密钥），校验 manifest、
   market 元数据、权限、归档安全与版本单调性；
3. 维护者按下文 [`REVIEW.md`](https://github.com/YimingZhanshen/skin-nexus-plugins/blob/main/REVIEW.md)
   评审并合并；
4. 合并后 CI 在 `market-signing` 环境用密钥打包/签名、重建 `v1/registry.json`
   并更新 Pages；主站市场最迟 10 分钟可见。主题由独立的
   `sign-themes.yml` + `scripts/sign-themes.sh` 走同一步骤，产物落 `themes/v1/`；
5. 版本一经发布不可覆盖，升级请递增 `version`。

下文 1–8 节是本机离线签名/续签/轮换的手工流程，CI 不可用或需要演练时使用。


## 1. 生成密钥（一次）

```bash
cd server
go run ./cmd/skin-plugin keygen \
  --id market-2026-10 \
  --out ~/keys/market-2026-10.pem
```

- 输出：`market-2026-10:<base64(raw 32-byte public key)>`；
- 私钥是 PKCS#8 PEM，权限 0600；请离线加密备份两份；
- 公钥串写入生产 `SKIN_PLUGIN_TRUST_KEYS`（官方市场公钥已编译内置，只有自建市场/轮换才需要设）；私钥**不要**提交、上传服务器或放 CI secret。
- 主题市场**另建一把**：`--out ~/keys/theme-market-2026-11.pem`、keyId `theme-market-2026-11`，
  写入 `SKIN_THEME_TRUST_KEYS`；两把钥匙不得互用，也不得复用同一个 keyId。

## 2. 插件目录与 market.json

官方插件源码在独立仓库 `skin-nexus-plugins` 的 `plugins/<name>/`；目录约定：

```
plugins/<name>/
  plugin.json      # 必需：hookApi=2 清单
  index.js
  market.json      # 市场元数据（title/description/changelog/capabilities...）
  ui/              # 可选
```

`market.json` 示例：

```json
{
  "schema": 1,
  "title": { "zh-CN": "我的插件", "en": "My Plugin" },
  "description": { "zh-CN": "一句话说明", "en": "One-line description" },
  "author": "Your Name",
  "changelog": "1.0.0 — first release",
  "requires": { "plugins": {}, "server": ">=0.4.0" },
  "conflicts": [],
  "capabilities": ["config", "http:routes"],
  "minServer": ">=0.4.0"
}
```

硬约束（`pack` 会校验）：

- `capabilities` 必须与 `plugin.json.permissions` 集合完全一致；
- `market.json` 与 `plugin.json` 同时声明 `requires`/`conflicts`/`minServer` 时必须一致；
- 插件清单同样要有 `minServer`，否则市场展示与安装校验会不同步。

## 3. 打包与签名

投稿前可先用无密钥的 `lint` 自检（与 PR CI 相同）：

```bash
cd server
go run ./cmd/skin-plugin lint \
  --dir ../plugins/my-plugin \
  --registry ../v1/registry.json \
  --host-version 0.4.0 --json
```

打包：

```bash
cd server
go run ./cmd/skin-plugin pack \
  --dir ../plugins/my-plugin \
  --out ../dist/my-plugin-1.0.0.zip \
  --key ~/keys/market-2026-10.pem \
  --key-id market-2026-10 \
  --json
```

产物根目录包含 `plugin.json`、代码/UI 和 `bundle.json`；签名覆盖除 `bundle.json`
外的全部内容摘要。`pack` 会拒绝：

- 符号链接、`..`/绝对路径/反斜杠/Windows 保留名/大小写冲突；
- 超过 32 MiB 单文件、64 MiB 总量、异常压缩比；
- 缺失入口或 `ui.assets` 目录；
- 已存在的输出文件（除非 `--force`，市场制品应升版本而不是覆盖）。

校验：

```bash
go run ./cmd/skin-plugin verify \
  --bundle ../dist/my-plugin-1.0.0.zip \
  --keys "market-2026-10:<base64-public-key>" \
  --host-version 0.4.0
```

### 3.1 主题包（`pack-theme`）

主题包用**主题密钥**（`theme-market-2026-11`）签，产物根目录是 `theme.json` + CSS/JS/字体资源：

```bash
go run ./cmd/skin-plugin pack-theme \
  --dir ../themes/my-theme \
  --out ../themes-dist/my-theme-1.0.0.zip \
  --key ~/keys/theme-market-2026-11.pem \
  --key-id theme-market-2026-11 \
  --json
```

`pack-theme` 用同一套归档安全与确定性打包（同版本重签字节级一致），`--host-version`
参数不适用——主题的版本门是 `theme.json` 的 `requires.skin_nexus`。registry 用
`registry --kind theme`（§4 同一条命令加一个 flag），落到 `themes/v1/`。
主题产品侧规则见 [`theme-spec.md`](theme-spec.md) §7.1。

## 4. 构建 registry

```bash
go run ./cmd/skin-plugin registry \
  --dist ../dist \
  --url-base https://<user>.github.io/skin-nexus-plugins/v1/artifacts \
  --key ~/keys/market-2026-10.pem \
  --key-id market-2026-10 \
  --expires-days 90 \
  --out ../dist/registry.json \
  --json
```

- registry 只保留每个插件的最高版本；`dist.url` 由 `url-base + 文件名` 生成；
- 默认 90 天有效期；过期后站点市场会 503（磁盘 last-good 也会被过期校验拒绝）；
- `verify-registry` 可用公钥离线复核：

```bash
go run ./cmd/skin-plugin verify-registry \
  --registry ../dist/registry.json \
  --keys "market-2026-10:<base64-public-key>"
```

## 5. 发布到 GitHub Pages

官方市场仓库是 <https://github.com/YimingZhanshen/skin-nexus-plugins>，目录：

```
plugins/<name>/                 # 插件源码（PR 投稿）
v1/registry.json                # CI 生成并提交
v1/artifacts/<name>-<version>.zip
v1/keys.json                    # 公钥透明清单
```

合并 PR 后由该仓库的 `sign.yml` 自动完成「打包 → 签名 → 重建 registry → 提交
`v1/` → Pages」。人工发布顺序（应急）：

1. 提交 artifacts（文件名不可变；同版本不同字节会被脚本拒绝）；
2. 覆盖 `v1/registry.json`；
3. 等待 Pages 构建（约 1 分钟）；从生产容器内验证：

```bash
wget -qO- https://yimingzhanshen.github.io/skin-nexus-plugins/v1/registry.json | head
```

要求：HTTPS、200、无 301/302；下载侧上限 registry ≤1 MiB、单个制品 ≤32 MiB（`pack` 侧的 64 MiB 是解包后总量上限，两者不是同一个数）。
GitHub Pages 可能有分钟级缓存，发布后不要立刻判定失败。

## 6. 生产配置

主站默认指向官方市场（`https://yimingzhanshen.github.io/skin-nexus-plugins/v1/registry.json`）；
注册表地址与官方公钥都**编译内置在镜像里**，compose 里的两行只是把覆盖点写明白，
`docker run` 裸跑同样开箱可用。下游二改可用 env 覆盖或关闭。

自建市场（或轮换密钥）时在 `/opt/skin-nexus` 的 `.env`（0600，不入库）或 compose `environment` 中设置：

```env
SKIN_PLUGIN_MARKET_URL=https://<user>.github.io/skin-nexus-plugins/v1/registry.json
SKIN_PLUGIN_TRUST_KEYS=market-2026-10:<base64-public-key>
SKIN_PLUGIN_STATE_DIR=/data/plugins/.state
SKIN_PLUGIN_UPLOAD_MAX=67108864
SKIN_IMAGE_TAG=sha-<release-commit>
```

`SKIN_PLUGIN_MARKET_URL` 显式设为空字符串可关闭市场；只改可信公钥可换用别的 registry。
市场安装只接受可信签名；本地上传未签名/未知密钥/签名错误的包时，超管需在风险弹窗中确认，
安装后插件列表会永久标注来源与信任等级（`verified` / `unknown-key` / `unsigned` / `invalid`）。

重启必须用 `docker compose up -d`（env 变更需要重建容器，`restart` 不会换 env）。
备份 `skin-data`（含 `/data/plugins/.state` 与 `/data/plugin-secrets.key`）与数据库。

验证：

1. 启动日志两行各报自己那套 keyring：`plugins: market=… trustKeys=N stateDir=… uploadMax=…` 与 `theme market: market=… trustKeys=N themesDir=…`；
2. 超管进入「插件市场」，catalog 显示已发布插件；
3. 安装 → 默认 disabled → 启用 → 升级 → 回滚 → 卸载；主题市场同理（安装/卸载热生效，无需重启容器）；
4. 错误 key/过期 registry/篡改 zip/301 重定向均被拒绝。

## 7. 过期与轮换

- registry 有效期 90 天；分发仓库的 `resign.yml`（插件）与 `resign-themes.yml`（主题）
  **每周一自动检查，剩余不足 60 天就重新签名并推送**——不依赖人工，也不需要本地跑 `registry`。
  阈值取 60 天而非 30 天还有第二层原因：GitHub 对 60 天没有任何推送的公开仓库会**自动停用
  schedule**，每 ~5 周产生一次续签提交正好让这个仓库一直保持"活跃"，定时任务不会悄悄停摆；
- 轮换 key：先把新旧公钥同时写入 `SKIN_PLUGIN_TRUST_KEYS`（主题写 `SKIN_THEME_TRUST_KEYS`）→
  重启 → 用新 key 重签 artifacts 与 registry → 旧产物退役后移除旧公钥；
- 私钥丢失只能换 key 并重签全部产物；已安装插件不受影响，但新安装会验签失败；
- registry 过期后宿主**连磁盘 last-good 缓存也一并拒绝**（缓存同样过到期校验），表现为市场
  浏览/安装 503；此时**已安装的插件与主题照常运行，本地上传也照常可用**——只有 registry 带有效期。
- v0.4.0 起，官方插件仓库已采用 PR 投稿 + CI 环境密钥自动签名/续签（见该仓库的
  `CONTRIBUTING.md` / `SPEC.md` / `KEY-ROTATION.md`）；本机手工流程仍可用于离线签名与演练。

## 8. 故障排查

| 现象 | 原因 |
|---|---|
| `plugin market is not configured` | URL 为空 |
| `plugin trust keys are not configured` | 公钥未配置/格式不是 `id:base64` |
| catalog 503 `unknown signing key` | keyId 与公钥不匹配 |
| catalog 503 `expired` | registry 超过 `expiresAt` |
| `sha256 mismatch` | artifact 被改动/上传不完整 |
| `signature verification failed` | bundle/registry 内容被改动或私钥不匹配 |
| 301/302 | Pages/反代重定向；客户端拒绝跟随 |
| 连接失败 | 主机是私网/回环，或 DNS 解析到非公网地址 |
