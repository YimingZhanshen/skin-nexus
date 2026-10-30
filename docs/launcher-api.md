# 启动器 API（Launcher API v1）

面向桌面启动器的账号/角色管理接口，由官方插件 `launcher-api` 提供。
规范正文与 OpenAPI 在插件仓库：

- <https://github.com/YimingZhanshen/skin-nexus-plugins/blob/main/plugins/launcher-api/LAUNCHER-API.md>
- <https://github.com/YimingZhanshen/skin-nexus-plugins/blob/main/plugins/launcher-api/openapi.yaml>

基址：`https://<站点>/api/plugins/launcher-api/v1`；`GET /v1/meta` 公开，其余
`Authorization: Bearer <token>`。

## 启动器登录流程

1. 启动器用现有的 **Yggdrasil Connect OIDC 设备码流程**（client `2`，scope 含
   `openid offline_access Yggdrasil.PlayerProfiles.Select Yggdrasil.Server.Join`）
   取得 `access_token`；或复用密码登录拿到的 Yggdrasil `accessToken`。
2. 调 `GET /api/plugins/launcher-api/v1/meta` 探测站点是否安装/启用该插件；
   404 时回退到浏览器。
3. 之后所有请求带 `Authorization: Bearer <access_token>`，不使用 cookie/CSRF。
4. `401` 时刷新或重新认证；改名/删角后旧游戏 token 可能失效，需刷新。

## 能力

- 账号：昵称、头像 URL、角色上限/数量、存储用量
- 角色：列表 / 详情 / 创建 / 改名 / 删除（UUID 零漂移）
- 外观：上传 PNG（skin/cape，可选 slim）或应用已有纹理、清除槽位
- 纹理：个人纹理库、独立上传（按 `uid+type+hash` 幂等）
- 皮肤库：公开纹理搜索/详情、导入为私有副本后应用到角色

错误为 `{"error":"stable.code","message":"..."}`；限流返回 `429 + Retry-After`。
宿主 `0.5.0+` 起，独立上传遇到公共库相同 `(type,hash)` 会返回
`409 texture.duplicate_public`，并附 `textureId/url`；同用户重复上传仍是 `200`。
完整端点、字段、错误码与重试语义见上面的规范。

## 服务端要求

- 宿主 `>=0.4.0`（`bearer-auth.v1`、`binary-body.v1`、`account.v1`）；
  公共纹理去重错误码需 `>=0.5.0`。
- 安装并启用签名插件 `launcher-api`；它需要 `http:routes`、
  `http:routes:bearer`、`account:read`、`players:read`、`players:write`、
  `textures:read`、`textures:write`。
- 角色的所有权、名称规则、数量上限、guard、事件与审计全部由宿主账号服务执行；
  插件只做请求/响应映射。
- 下游复刻站可安装自己的 launcher API 插件；启动器只需按本规范调用固定路径。
