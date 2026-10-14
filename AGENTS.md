# Fast Note Sync Service

自己部署的 Obsidian 笔记同步服务端（Go）：多端实时同步，外加管理后台、REST API 和给 AI 用的 MCP 接口。源自 haierkeys/fast-note-sync-service，上游已停更；本仓库独立开发，不再跟上游。

## 地图
- internal/routers/：三种入口。websocket_router（同步）、api_router（REST）、mcp_router（MCP）
- internal/service/：业务规则，三种入口共用；internal/domain/ 定义接口，internal/dao/ 实现
- internal/model/、internal/query/：`make gen` 生成，不手改
- frontend/：管理后台的构建产物，不手改；源码和上线步骤见 [后台的 atlas](../fast-note-sync-webgui/docs/atlas/README.md)
- 整套系统（含插件和后台）的设计见 [docs/atlas/README.md](docs/atlas/README.md)

## 验证
- `make test`：Go 单元测试，约 20 秒
- `go build ./...`：编译检查
- 改了 MCP 工具定义：`go test ./internal/routers/mcp_router/ -run TestStreamableHTTPHandshakeThroughGin`

## 硬约束
- 服务端的版本检查和一键升级只用本 fork 的 Release（pkg/fileurl/service_release.go），不回退到上游
- Go module 路径保持 `github.com/haierkeys/fast-note-sync-service`，不改
- 发版本只靠推 `v*` tag（.github/workflows/fork-release.yml）；开发和发布都在 master
- 改 internal/proto/v1/sync.proto 时，插件仓库同步改

## 改动前先读
| 如果你要改… | 先读 |
|---|---|
| 同步消息、合并、冲突（internal/routers/websocket_router/、internal/service/conflict_service.go、pkg/app/websocket.go、internal/proto/） | [同步](docs/atlas/sync.md) |
| MCP 工具、搜索（internal/routers/mcp_router/、internal/dao/note_search_repository.go、internal/middleware/mcp_oauth.go） | [AI 接入](docs/atlas/ai-access.md) |
| 数据层、历史版本、软删除（internal/dao/、internal/domain/、internal/service/、internal/upgrade/、internal/task/task_note_history.go） | [笔记库与存储](docs/atlas/storage.md) |
| REST 接口（internal/routers/router_api.go、internal/routers/api_router/、internal/dto/） | [REST API](docs/atlas/rest-api.md) |
| 登录、令牌、权限（internal/middleware/、internal/oidc/、internal/oauth/、pkg/app/token.go、pkg/app/permission.go） | [账号与授权](docs/atlas/auth.md) |
| 分享、短链接（internal/service/share_service.go、pkg/shortlink/） | [分享](docs/atlas/share.md) |
| 备份、镜像、Git（internal/service/backup_service.go、internal/service/git_sync_service.go、pkg/storage/） | [备份、镜像与 Git](docs/atlas/backup-git.md) |
| 发布、升级（.github/workflows/、pkg/fileurl/service_release.go、internal/task/task_check_version.go、docker/） | [发布与升级](docs/atlas/release.md) |

## 维护
- 改了某一块的边界、流程，或踩到新坑：同一次改动里更新对应的 atlas 节点，刷新 verified。
- 硬约束、根节点的 30 秒版和愿景与思想属于守护层：不直接改，在改动说明里单独列出建议，等主人确认。
- 代码和守护层冲突时，停下来问主人，不要自行二选一。
- 提交前跑 `python3 scripts/atlas_audit.py`。
