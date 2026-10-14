---
kind: part
parent: README.md
code: [internal/routers/router_api.go, internal/routers/api_router/, internal/dto/]
verified: 2026-10-14 @ 4cf8ffac
---
# REST API

> **30 秒版**：`/api` 下的 HTTP 接口。管理后台的所有操作都调它，脚本和外部程序也可以用它增删改查笔记、管理笔记库和系统配置。它和 WebSocket、MCP 共用同一个服务层，规则一致。

## 边界

负责：HTTP 路由、参数校验、响应格式、限流和超时；接口文档（Swagger）。

不负责：业务规则（在服务层，见[笔记库与存储](storage.md)）；令牌校验的实现（见[账号与授权](auth.md)）；MCP 接口（虽然也挂在 `/api/mcp`，见 [AI 接入](ai-access.md)）。

## 怎么运转

路由在 [router_api.go](../../internal/routers/router_api.go)，按身份分成几组：

| 分组 | 谁能调 | 例子 |
|---|---|---|
| 不需要登录 | 任何人 | 健康检查、版本、OIDC 登录跳转和回调；`/api/user/sync` 是 WebSocket 的入口 |
| 只限后台、不需要登录 | 请求声明自己是后台（见[账号与授权](auth.md)） | 注册、登录、后台配置 |
| 分享 | 持分享令牌的访客 | 读分享的笔记和附件，见[分享](share.md) |
| 需要登录 | 持用户令牌的调用方 | 笔记、文件夹、附件、笔记库、历史、回收站 |
| 只限后台 | 后台登录签发的令牌 | 系统配置、备份、Git、存储、用户管理、一键升级 |

每个请求先经过一串公共中间件：应用信息、追踪 ID、限流、超时、语言、访问日志、异常恢复。处理函数在 [api_router/](../../internal/routers/api_router/)，一类资源一个文件，请求和响应结构在 [internal/dto/](../../internal/dto/)。

接口文档有两份：手写的 [REST_API.md](../REST_API.md)，和由代码注释生成的 [swagger.yaml](../swagger.yaml)（`make docs`）。社区贡献的扩展接口（编辑操作、反向链接、健康检查）记在 [API-EXTENSIONS.md](../API-EXTENSIONS.md)。

## 坑

- **改了接口不重新生成 Swagger，文档就过期**。→ `swagger.yaml` 不会自己更新。→ 改了处理函数上的注释后跑一次 `make docs`。
- **后台专用的接口用 `RequireWebGUI` 限定**。→ 新加的管理类接口忘了放进后台分组，普通 API 令牌也能调。→ 新接口先想清楚它属于哪一组。
