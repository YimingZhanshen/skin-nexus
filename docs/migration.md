# 从 blessing-skin-server 迁移（migrate 子命令手册）

> 目标读者：自行部署 SkinNexus 并要把既有 blessing-skin-server（老皮肤站）数据
> 全量迁过来的运营者。全流程实测于 2026-10-24（生产库 67 用户 / 67 角色 /
> 61 纹理 / 394 邀请码，零丢行、UUID 零漂移）。
>
> 部署新站本体见 [`deploy.md`](deploy.md)（Docker 优先）；构建见 [`build.md`](build.md)。
> Docker 部署时迁移 CLI 不需要装 Go——`docker compose exec skin-nexus /app/skin-nexus migrate ...`，详见 deploy.md §四。
>
> **执行位置提示**：应用镜像里没有 `mysql`/`mysqldump`/`tar`/`cmp`/`openssl` 等工具，
> 本文的 SQL/备份/校验命令在**宿主机**或内置库容器（`docker compose exec db`）里执行；
> `migrate` 命令本身在 skin-nexus 容器内执行（`--env` / `--textures-*` 都是**容器内路径**）。

## 〇、开始前 checklist

- [ ] SkinNexus 已能启动并连上**空的目标库**。建表推荐直接 `SKIN_AUTO_MIGRATE=true`
      启动一次（自动应用 `migrations/` 全部文件（0001…0021）并写 `schema_migrations` 记标）。
      ⚠️ 手动执行 DDL 有两处必炸：① `0009` 的 ALTER 与 `0001`/`0005` 部分列重叠，
      直接 `mysql < 0009.sql` 会 ER1060 中止，需 `mysql --force` 逐文件执行；
      ② 手动执行不写 `schema_migrations` 标记，而 **import 实跑强查
      `0008_legacy_preserve.sql` 的记标**（缺标记直接拒绝执行）——手动路径必须
      补 `INSERT INTO schema_migrations ...` 记标，否则用自动迁移
- [ ] 手上有老站 `.env`（必填：`PWD_METHOD`/`PASSWORD_METHODS`；`SALT` 缺省=空盐合法，`APP_URL` 仅展示）与老站纹理目录 `storage/textures/` 的读权限
- [ ] 老站数据库只读账号（或 mysqldump 快照）——import 对老库只做查询，但仍建议只读凭据兜底
- [ ] **老站表变体预检**（魔改/老版本插件会缺列，缺列会在 import 中止报 `Unknown column`）：

      ```bash
      mysql -N -e "SHOW COLUMNS FROM legacy.uuid"          # 应含 pid 列；没有则：
      # mysql -e "ALTER TABLE legacy.uuid ADD COLUMN pid INT NOT NULL DEFAULT 0;"
      mysql -N -e "SHOW COLUMNS FROM legacy.invitation_codes"  # 应含 description；没有则：
      # mysql -e "ALTER TABLE legacy.invitation_codes ADD COLUMN description VARCHAR(255) NULL;"
      ```

      （`uuid.pid` 是部分插件变体才有的列、`description` 是 fork/魔改版扩展列——
      import 的查询固定要这两列）
- [ ] 目标机磁盘余量 ≥ 纹理目录体积 × 2（备份 + 拷贝各一份）
- [ ] 目标纹理目录（`SKIN_TEXTURES_DIR`，compose 默认 `/data/textures`）可写
- [ ] **若老站有非 bcrypt/argon2 的弱哈希用户**（preflight 哈希分布可查）：准备把老站
      `.env` 挂载进 skin-nexus 容器并在 compose 设 `SKIN_LEGACY_ENV=/data/legacy.env`，
      否则这些用户**无法登录**（按 .env 盐链逐个试验证，支持 12 种公式；bcrypt/argon2 PHC 格式不依赖）。
      ⚠️ `SKIN_LEGACY_ENV` 指向的文件解析失败 = **服务启动即失败**（日志前缀 `legacy env:`）——
      挂错路径/坏文件要当场看日志；缺挂载时弱哈希用户登录表现为「账号或密码错误」
- [ ] 已读完本文 §三「切流断言」与 §六「切流到正式域名」（含签名钥步骤）

**原则**：老站只读、目标先备份、先 dry-run 后实跑、跑完对账。

## 一、迁移能带走什么

`skin-nexus migrate import` 按 blessing 存储约定逐表搬运，**主键保留**：

| 老站表 | 目标表 | 说明 |
|---|---|---|
| `users` | `users` | 密码哈希原样携带；`password_algo` 自动分类（bcrypt→`legacy:bcrypt`、argon2 PHC→`legacy:argon2`、其余→`legacy` 按 .env 盐链逐个试；12 种算式与 `SKIN_LEGACY_ENV` 要求见 §〇 清单），网页登录时惰性重哈希为 argon2id；`permission` 原样搬运（老站管理员迁移后即管理员） |
| `players` | `players` | 名称/皮肤披风引用原样 |
| `textures` | `textures` | `type` 做 steve/alex→`skin`+`model` 映射；`name`→`description` |
| `uuid` | `uuid_map` | 过五步清洗（同名去重 / 同 UUID 去重 / pid 回填 / 孤儿剔除），**UUID 零漂移** |
| `options` | `options` | 站点配置（`INSERT IGNORE`，先到先得——目标站已写过的同名键不会被老站值覆盖） |
| `user_closet` | `user_closet` | 衣柜（无主键表，`NOT EXISTS` 去重） |
| `invitation_codes` | `invitation_codes` | 邀请码（含 `description` 等老站扩展列） |

派生列（老站没有，由导入器换算）：`users.player_limit` = max(角色数, 3)、
`users.can_join_server` = `permission>=0`、`users.avatar_tid` ← `avatar`（0→NULL）、
`users.email_verified` ← `verified`、`created_at`/`updated_at` ← `register_at`。
**不迁**：`players.preference`（遗留列，保留为空）、`textures.size`/`likes`、
会话/令牌（`web_sessions`、`ygg_tokens` 等，登录态自然重建）、
老站插件表（`ygg_log`、`notifications`、`reports` 等无对应语义）。

纹理文件按内容寻址**字节级拷贝**到目标纹理目录。**问题文件不会被拷贝**：
文件名≠内容 hash 的（`HASH-MISMATCH`）被拒绝拷贝、非 64-hex 文件名的（`BAD-NAME`）
直接跳过——这两类必须在 preflight 清单里逐个处理（修复后重跑或切流后手工补拷），
否则对应皮肤 404。同 hash 多条记录只有一份文件，`files copied` 对的是**文件数**。

> 老站是**魔改/分叉版** blessing 也适用：import 按表名+列名搬运，缺列的表
> 引擎会在执行到该表时报 `Unknown column` 中止（preflight 不连库、暴露不了缺列）——
> 用〇节的 SHOW COLUMNS 预检先行。

## 二、执行序（实测路径）

### 0. preflight（只读体检，不连数据库）

解析老站 .env 的密码算式/盐槽，审计纹理树：

```bash
skin-nexus migrate preflight --env /path/to/legacy/.env \
  --textures /path/to/legacy/storage/textures \
  --dump hashes.txt
#   预期：SALT slots aligned OK；texture scan: checked=<N> ok=<N> hashMismatch=0 badIHDR=0 badName=0
#   hashes.txt = 老站 users.password 列逐行导出（mysql -N -e 'SELECT password FROM legacy.users' > hashes.txt）
#   （--dump 是输入：给引擎哈希清单做分布统计，不是输出文件）
```

读三处输出做决策：

1. **方法/盐对齐表**（盐打码显示）——与老站 `.env` 声明一致才继续；
2. **哈希分布**：`weak-share=0%` → 情景甲（仅惰性重哈希）；`weak-share>0%` → 情景乙（MD5/SALTED 族存量，需 `SKIN_LEGACY_ENV` 挂载 + 惰性重哈希 + 管理面板「弱哈希余量」看板跟踪）；
3. **纹理树审计**：`hashMismatch`（文件名≠内容 hash）与 `badIHDR`（解码异常）、`badName`（非 64-hex 文件名）逐个列出——**`hashMismatch` 与 `badName` 不会被 import 拷贝**；`badIHDR` 但 hash 合法的文件**仍会被拷贝**（文件在、渲染可能异常，记录在案人工复查）。

### 1. 备份

```bash
# 目标库与纹理目录（旧站侧不需要备份——全程只读）；以下在宿主机执行
mysqldump -u root -p skin_nexus | gzip > backup-skin_nexus-$(date +%Y%m%d-%H%M%S).sql.gz
tar czf backup-textures-$(date +%Y%m%d-%H%M%S).tgz /data/textures
```

### 2. 停目标站，清空业务表（重迁场景）

保留 `union_kv`、`oidc_clients`、`schema_migrations` 这些站点身份/基建表
（`web_sessions`、`ygg_tokens` 等会话令牌表可不清——登录态自然失效）。
`TRUNCATE` 是单表语句（各 MySQL 版本皆然），逐表执行：

```bash
for t in users players textures uuid_map options user_closet invitation_codes; do
  mysql -u root -p skin_nexus -e "TRUNCATE TABLE $t;"
done
```

全新部署跳过本步。

### 3. dry-run（只读预演）

```bash
skin-nexus migrate import \
  --source-dsn 'USER:PASS@tcp(127.0.0.1:3306)/legacy?parseTime=true' \
  --target-dsn 'USER:PASS@tcp(127.0.0.1:3306)/skin_nexus?parseTime=true' \
  --source-prefix 'legacy.' \
  --textures-src /path/to/legacy/storage/textures --textures-dst /data/textures \
  --dry-run
```

> 容器内跑时 DSN 主机别写 `127.0.0.1`（那是容器自己）：host 网络模式才可用
> `127.0.0.1`，桥接模式写 `172.17.0.1` 或宿主机内网 IP（同 deploy.md §2.2）。
> `--textures-src` 同样是容器内路径，老站目录需挂载进容器。

通过标准：

- 各表 `selected` 数与老站 `COUNT(*)` **逐一相等**（7 表皆是）；例外在 `inserted`：
  `uuid_map` 对 `uuid clean: out=` 计数、`user_closet` 对 `selected`（NOT EXISTS 去重）；
- **无 `will be SKIPPED` 类 WARN、无超长告警**（报告里的 `skipped` 计数在 dry-run 下
  大多恒为 0，不能当通过信号——真正的丢行信号是 WARN 文本）；
- 纹理 `checked=N ok=N`（dry-run 不拷文件、无 `files copied` 行，计数检查放到 §三.3 实跑后）。

dry-run 的副作用仅一个：会在目标机创建纹理目录（`--textures-dst`），不写任何库表；
另外 dry-run **不查 0008 记标**，通过≠实跑前置已满足（见〇节）。

### 4. 实跑

去掉 `--dry-run` 即可；引擎**幂等**（INSERT IGNORE / NOT EXISTS），失败可直接重跑。

参数要点：

- **两侧 DSN 必须带 `parseTime=true` 且 `loc=` 一致**（引擎强制校验，不一致直接拒绝）。
- `--source-prefix` **必填**（源码强制，留空直接报错）：老站表前缀/库名限定
  （如 `legacy.` 表示同实例的 legacy 库，或 `legacy_` 表前缀）；老站表无前缀时
  也须显式写库名限定（如 `olddb.`）。
- `--textures-src` 给老站纹理目录；`--textures-dst` 给目标 `SKIN_TEXTURES_DIR`
  （compose 默认 `/data/textures`），不给则只迁库不迁文件。
- 导入会保留主键 ID：目标表必须为空（或与老站同 ID 数据），否则撞主键会 skip。
- 实跑强查 `schema_migrations` 里 `0008_legacy_preserve.sql` 记标（见〇节）。

### 4.5 实跑后对账（跨库 SQL，宿主机执行）

```bash
# ① 两侧行数对照（与 import 报告 selected/inserted 三方一致）
for t in users players textures uuid options user_closet invitation_codes; do
  echo "$t legacy=$(mysql -N -e "SELECT COUNT(*) FROM legacy.$t")"
done
mysql -N -e "SELECT 'users',COUNT(*) FROM skin_nexus.users UNION
             SELECT 'players',COUNT(*) FROM skin_nexus.players UNION
             SELECT 'textures',COUNT(*) FROM skin_nexus.textures UNION
             SELECT 'uuid_map',COUNT(*) FROM skin_nexus.uuid_map UNION
             SELECT 'options',COUNT(*) FROM skin_nexus.options UNION
             SELECT 'user_closet',COUNT(*) FROM skin_nexus.user_closet UNION
             SELECT 'invitation_codes',COUNT(*) FROM skin_nexus.invitation_codes"

# ② 丢行定位（应返回 0 行；有则按 §三.1 处置后清库重跑）
mysql -N -e "SELECT u.id,u.nickname FROM legacy.users u
  WHERE NOT EXISTS(SELECT 1 FROM skin_nexus.users s WHERE s.id=u.id)"

# ③ 凭据完整性：应等于老站用户总数
mysql -N -e "SELECT COUNT(*) FROM skin_nexus.users WHERE password_algo LIKE 'legacy%'"

# ④ Union 键连续性（切流前必比对；options 先到先得，目标站写过的同名键会挡住老站值）
mysql -N -e "SELECT v FROM skin_nexus.options WHERE k='union_member_key'"
#   与老站 SELECT option_value FROM legacy.options WHERE option_name='union_member_key' 比对
```

## 三、切流断言（跑完必须核对）

1. **`users selected==inserted` 且无用户被跳过**。若报告 `skipped>0` 或 WARN 里有
   `will be SKIPPED`：主因是 **email/nickname 的 CI 唯一键撞车**（老站 email 无唯一
   索引、可有多个空邮箱，`email=''` 会互相撞；目标表 UNIQUE 对 NULL 不撞）——
   按 WARN 逐条处置（老站改邮箱/昵称，或先把空邮箱 `UPDATE ... SET email=NULL`），
   再清库重跑；被跳用户的 `players`/`user_closet` 会照常导入成悬挂行，必须同批重跑。
   只有「目标残留行主键撞车」才走清库重跑；
2. 各表 `inserted` 与老站行数逐一相等（本项目 2026-10-24 全量实跑实测
   67/67/61/67/87/84/394）。**两个例外是设计内行为**：`uuid_map` 对报告
   `uuid clean: in=… out=…` 的 `out`（五步清洗丢重复/孤儿）、`user_closet` 对
   `selected`（NOT EXISTS 去重）；
3. 纹理 `files copied=N` 应等于老站纹理**文件数**，且报告无 `HASH-MISMATCH`/
   `BAD-NAME` 丢弃；抽样 `cmp` 老站/目标同名文件逐字节一致：

   ```bash
   cmp /老站/storage/textures/<hash> /目标/data/textures/<hash> && echo MATCH
   ```

   preflight 清单里的问题文件若未修复，切流后**必须手工补拷**（改名/修复后按内容
   hash 落目标目录），否则对应皮肤 404；
4. **UUID 零漂移抽查**：取老站 `uuid` 表一行，对照两处——

   ```bash
   curl -s https://你的域名/api/yggdrasil/api/users/profiles/minecraft/{name}
   curl -s https://你的域名/api/yggdrasil/sessionserver/session/minecraft/profile/{uuid}
   ```

   老站 `uuid` 表 = 名查返回的 `id` = profile 查询路径，三处逐字节一致；
5. `POST /api/v1/login` 用老站原密码登录成功（惰性重哈希生效，
   该用户 `password_algo` 已变 `argon2id`，管理面板用户表可见）。**弱哈希用户
   （`password_algo='legacy'`）必须抽样实测登录**——这是 `SKIN_LEGACY_ENV` 挂载
   是否生效的唯一验证；缺挂载时他们表现为「账号或密码错误」。
   注意惰性重哈希**只发生在网页登录**（`POST /api/v1/login`）；只用启动器
   （ygg authenticate）登录的用户不会换算法，`weakHashUsers` 看板不降属正常。

## 四、告警词典

**良性**：

- `WARN invitation id=N used_at is NULL/zero date`：未使用邀请码的正常形态
  （老站 `used_at` 可空/零日期；导入时零日期折叠为 NULL），不影响使用。
- `options` 比老站多几行是正常的：`/setup` 完成或 union 启用会往 options 写新键。
  **迁移后不会误触发安装向导**（users 非空即视为已初始化，`/setup` 是一次性门），
  无需手工补 `setup_done`。**老站 `permission>0` 的账号迁移后即为管理员**
  （权限原样搬运，无需补建）；仅当老站确无管理员时才用 SQL/管理接口补建
  （或切流前在空目标站先过 `/setup` 建好管理员再导入——ID 不冲突即可并存）。

**非良性（必须处置）**：

- `will be SKIPPED` 系列 WARN：对应行不会写入（email/nickname 撞车等），见 §三.1；
- 超长告警（如 players.name 超 16 字符）：引擎只 WARN 仍会插入，但目标列
  `VARCHAR(16)` 会截断（uuid_map 截断撞主键还会静默丢行）——老站名超长的先改名
  再迁，或迁后核对；
- 纹理 `HASH-MISMATCH` / `BAD-NAME`：对应文件不拷贝（§三.3）；`BAD-IHDR` 若 hash 合法
  仍会被拷贝（文件在、渲染可能异常），需人工复查。

## 五、回滚

目标库 `mysqldump` 快照 + 纹理目录 tar 包直接恢复即可；老站数据全程未写，
无需回滚。跨版本重迁：清库后重跑（引擎幂等）。

切流观察期回滚触发条件：**登录失败率 >1% / UUID 不一致 >0 / 验签失败 >0**
→ 立即把反代切回旧站（旧站只读保留至少 30 天）。

## 六、切流到正式域名

迁移完成≠切流。域名切换（如 `test-skin.example.com` → `skin.example.com`，本项目
2026-10-25 实测完成）：

1. **签名钥对齐（必做，最先做）**：老站 `ygg_private_key` option（PEM 文本）
   写入目标 `data/sign-key.pem`（`SKIN_SIGN_KEY` 指向）后再切域名——否则
   authlib-injector 客户端缓存的 `signaturePublickey` 与新钥失配、全网皮肤
   签名校验失败。union hub 键连续性由 options 导入自动保持，但 options 是
   先到先得（§一），**切流前务必按 §4.5 ④ 人工比对 `union_member_key`**。
   - **union 模式例外（2026-10-25 线上实测）**：`union_enabled=1` 时对外
     `signaturePublickey` 来自 union hub 私钥，与老站 yggdrasil-connect
     插件所用密钥同指纹（双侧实测一致），切流无需改
     `sign-key.pem`；一旦关闭 union 回退本地签名，必须先把老站
     `ygg_private_key` 写入 `sign-key.pem`（老站 option 值可能存成单行、
     换行转义为字面 `\n`，写入前需解开转义）。`sign-key.pem` 必须是 **PKCS#8**（`BEGIN PRIVATE KEY`）：老站若是 PKCS#1（`BEGIN RSA PRIVATE KEY`），先 `openssl pkey -in old.pem -out sign-key.pem` 转换，否则启动即失败。
   - 从老站导出该 option 并比对指纹的参考命令（不打印私钥）：

     ```bash
     # 老站库导出（老站 options 表列名是 option_name/option_value；值为单行 PEM，\n 是字面转义）
     mysql -N -e "SELECT option_value FROM legacy.options WHERE option_name='ygg_private_key'" > yggk.txt
     sed 's/\\n/\n/g' yggk.txt > sign-key.pem
     # 双侧公钥指纹比对（应一致）
     openssl pkey -in sign-key.pem -pubout | openssl pkey -pubin -outform DER | sha256sum
     # union 模式：比对的是「线上实际下发」的公钥（与 hub 私钥对应）：
     curl -s https://你的域名/api/yggdrasil/ | python3 -c "import sys,json,base64,hashlib;print(hashlib.sha256(base64.b64decode(''.join(json.load(sys.stdin)['signaturePublickey'].splitlines()[1:-1]))).hexdigest())"
     ```

2. 改 `SKIN_BASE_URL` 与 nginx `server_name`、证书（`SKIN_BASE_URL` 改后需重启容器
   才影响纹理 URL——见 deploy.md §2.3）；
3. 管理面板改 `site_url`（options 优先于 env；重启后才生效，纹理 URL 只随
   `SKIN_BASE_URL` 变）；
4. yggdrasil 客户端的服务器地址需随域名更新（老玩家改一次启动器配置）；
5. 联盟（Union）成员站注册信息若含域名，需在 hub 侧同步。

## 七、迁移后运营检查

- **邀请码**：`invitation_codes` 394 条已随迁；老站「注册开放/必填邀请码」语义由管理面板「站点设置」`invite_required` 控制（默认注册开放、邀请码选填）。
- **皮肤库/衣柜**：用户衣柜 `user_closet` 已迁；皮肤库页点赞数 = `COUNT(user_closet)` 实时统计，零 schema 变更。
- **弱哈希余量**：管理面板仪表盘 `weakHashUsers` 归零前保持关注（用户**网页登录**即惰性换 argon2id；只用启动器的用户不触发换算，属正常）。
- **高清皮肤**：原生分辨率保留渲染，预览 URL 带 `?v=` 版本号；若浏览器看到旧缓存图，刷新一次即可。
- **公告/设置**：老站 `options` 已导入，管理面板各设置页直接可见；老站自定义 CSS/JS、主题不随迁（老站为 PHP 模板，语义不兼容），在新站「外观」重新配置。

## 附：`migrate uuid` 子命令（可选预演）

```bash
skin-nexus migrate uuid --rows uuid.tsv --players players.tsv
```

对**导出的 TSV 快照**（`uuid` 表 `id\tname\tuuid\tpid`、`players` 表 `id\tname`，
TSV 的行序无关（内部自动按 id 排序；uuid TSV 第 4 列 pid 可选））做五步清洗的只读预演并输出 KEEP 清单。import 内部含同款清洗，
本子命令用于提前看清「哪些行会被清洗掉」；不连数据库。
