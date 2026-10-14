---
kind: part
parent: README.md
code: [internal/middleware/, internal/oidc/, internal/oauth/, internal/service/token_service.go, internal/service/user_service.go, pkg/app/token.go, internal/config/security.go, internal/config/user.go]
verified: 2026-10-14 @ 4cf8ffac
---
# 账号与授权

> **30 秒版**：谁能连上来、能动哪些笔记库。用户用账号密码或 OIDC 登录；登录或手动创建后拿到令牌，插件、后台、脚本、AI 客户端都凭令牌访问。令牌可以限定协议、客户端、笔记库和读写权限。

## 边界

负责：用户和管理员；令牌的签发、校验、撤销和权限范围；后台的 OIDC 登录；MCP 的 OAuth 登录。

不负责：分享链接的访问权限（另有分享令牌，见[分享](share.md)）；每个接口属于哪一组（见 [REST API](rest-api.md)）。

## 怎么运转

**用户。** 管理员由配置里的 `admin-uid` 指定。它是 0（默认）时不区分管理员，任何登录用户都能用管理功能，后台登录后会弹窗建议把当前用户设为管理员；这时只有还没有任何用户时才允许注册，所以第一个注册的人就是唯一的用户。多人使用时，把 `admin-uid` 设成管理员的 UID，再用 `register-is-enable` 控制注册。

**令牌。** 令牌是用 `auth-token-key` 签名的 JWT（[token.go](../../pkg/app/token.go)），同时在数据库里有一条记录，用来撤销和限权（[token_service.go](../../internal/service/token_service.go)）。一条令牌记录说明：

| 字段 | 意思 |
|---|---|
| 客户端类型 | 给谁用：后台、Obsidian 等 |
| 签发方式 | 登录时自动签发，或在后台手动创建 |
| 权限范围 | 一个字符串，写明允许的协议、客户端和功能，例如 `p:mcp f:note_r` 表示只能走 MCP、只能读笔记。功能码有 `note_r`、`note_w`、`file_r`、`file_w` 等（校验在 [permission.go](../../pkg/app/permission.go)） |
| 笔记库 | 限定哪些笔记库，空表示不限制 |

后台登录签发的令牌默认 7 天过期，并绑定登录时的 IP；其他令牌默认 365 天。

**各入口怎么校验。** REST 的需要登录分组用 [user_auth_token.go](../../internal/middleware/user_auth_token.go)；后台专用接口再加 [webgui_auth.go](../../internal/middleware/webgui_auth.go)，它查两层：请求声明的客户端类型是后台，并且令牌是登录时为后台签发的，手动创建的 API 令牌不行；WebSocket 在连接时校验（[router_websocket.go](../../internal/routers/router_websocket.go) 的 `UseTokenVerify`）；MCP 用 [mcp_oauth.go](../../internal/middleware/mcp_oauth.go)，同时接受 Bearer 令牌和 OAuth。

**OIDC 和 OAuth。** 后台可以用 OIDC 登录（[internal/oidc/](../../internal/oidc/)，配置见运行手册 [OIDC](../runbook/OIDC.zh-CN.md)）。MCP 客户端可以走 Stytch 提供的 OAuth（[internal/oauth/](../../internal/oauth/)），OAuth 的权限范围映射成上面的令牌权限（`scope_mapper.go`），配置见 [mcp-oauth-stytch](../runbook/mcp-oauth-stytch.zh-CN.md)。

## 坑

- **`auth-token-key` 的默认值写在公开的配置样例里**。→ 不改的话，签名密钥等于公开。→ 部署时必须改。改了以后，已经签发的令牌全部失效，各设备要重新登录。
- **服务端以调试模式（gin 的 debug 模式）运行时，后台专用接口不做任何限制**。→ 方便本地调试和 Swagger，但普通 API 令牌也能调管理接口。→ 线上不要用调试模式。
- **后台签发的令牌绑定 IP**。→ 换网络（手机切到移动数据、走代理）以后，后台突然要求重新登录。→ 这是 `webgui-login-token-bind-ip` 的预期行为，需要时可以关。
- **给 AI 用的令牌，"空选笔记库"不等于"全部笔记库"**。→ 空选不会授予任何笔记库。→ 要全部就选"允许所有笔记库"，它也包括将来新建的笔记库。
