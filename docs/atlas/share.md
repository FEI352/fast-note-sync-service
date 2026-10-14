---
kind: part
parent: README.md
code: [internal/service/share_service.go, internal/routers/api_router/handler_share.go, internal/middleware/share_auth_token.go, internal/domain/domain_user_share.go, pkg/shortlink/, internal/config/short_link.go]
verified: 2026-10-14 @ 4cf8ffac
---
# 分享

> **30 秒版**：把一篇笔记生成一个链接发给别人。对方不用登录，在浏览器里就能看，笔记里引用的图片、音视频也一起显示。分享可以设访问密码、过期时间，可以生成短链接，还能看访问次数。

## 边界

负责：创建和取消分享、分享令牌、密码和过期校验、访问统计、短链接；分享页的后端接口。

不负责：分享页本身的界面（后台仓库里单独的入口 `share.tsx`，构建成 `frontend/share.html`，见[后台仓库的 atlas](../../../fast-note-sync-webgui/docs/atlas/README.md)）；登录用户的令牌（见[账号与授权](auth.md)）。

## 怎么运转

1. 用户在插件或后台对一篇笔记点"分享"，服务端建一条分享记录，签发一个分享令牌（`share-token-key` 签名，默认 30 天），返回分享链接（[share_service.go](../../internal/service/share_service.go)）。
2. 访客打开链接，浏览器加载分享页，分享页调 `/api/share/note` 和 `/api/share/file`。这组接口只认分享令牌（[share_auth_token.go](../../internal/middleware/share_auth_token.go)），先校验过期和密码，再返回笔记和它引用的附件，同时累加访问次数。
3. 需要短链接时，服务端把完整的分享链接交给外部短链服务（默认 sink.cool，[pkg/shortlink](../../pkg/shortlink/)），把返回的短链接存进分享记录。

## 坑

- **短链接必须指向客户端看到的那个分享链接**。→ 服务端如果自己重新生成一次链接，令牌不同，短链打开就失效。→ 客户端创建短链时把完整链接传上来，服务端直接用它。修过一次，见提交 e64875f4。
- **在反向代理后面，服务端拼出的链接可能丢端口**。→ 分享链接打不开。→ 服务端读 `X-Forwarded-Host`；代理要把它带上。
- **短链接会把分享链接发给第三方服务**。→ 分享链接里带着访问令牌。→ 介意的话不要用短链接，或在 `short-link` 配置里换成自己部署的服务。
