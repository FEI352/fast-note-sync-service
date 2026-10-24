# Flowix 接入 FNS：背景与待办

> 状态：进行中
> 相关仓库：`/code/outer/fast-note-sync-service`（本仓库，Go 服务端 + 前端构建产物）、
> `/code/outer/fast-note-sync-webgui`（前端源码）

## 背景

Flowix 桌面端已经接入 Fast Note Sync Service，作为笔记与附件的同步后端：

- 客户端实现：`flowix/app/flowix-fns`（Rust，独立 crate）
- 绑定模型：一个笔记本绑定一个 token 和一个 vault
- 客户端 `clientType` 固定为 `flowix`
- 因此服务端 token 的 scope 需要满足 `p:rest,ws c:flowix f:*`
- 每个 token 可以通过 `vaults` 限制可访问的 vault（逗号分隔，留空表示不限制）

### 已踩过的坑

1. **客户端类型不匹配 → 315**
   - 用 Obsidian 插件签发的 token（scope `c:obsidianPlugin`）配 `client=flowix`，
     握手通过但 `NoteSync` 返回 `NoteSyncAck 315 Auth token Scope restricted`。
   - 服务端的校验只是对 scope 里的 `c:` 做字符串匹配，不存在“支持哪些客户端”的白名单。

2. **vault 白名单中文编码损坏 → 315**
   - 在 Windows Git Bash 里用 `curl -d '{"vaults":"工作"}'` 签发，中文被以非 UTF-8 发送，
     服务端存成 `U+FFFD` 替换字符，导致白名单永远匹配不上。
   - 改用 Node 生成 UTF-8 JSON 文件 + `--data-binary @file` 后正常。

3. **客户端把错误帧当广播跳过**
   - 权限失败时服务端返回 `<Request>Ack`（如 `NoteSyncAck`），早期客户端只等 `NoteSyncEnd`，
     会跳过错误帧；叠加 WebSocket Ping 不断重置单帧超时，表现为“同步中”永久卡住。
   - 已在 `flowix-fns` 修复：请求作用域的错误立即失败，超时改为 wall-clock 截止。

## 前端源码与仓库

| 内容 | 地址 / 位置 |
|---|---|
| 前端源码（上游） | `https://github.com/HughArch/fast-note-sync-webgui` |
| WSL 工作副本 | `/code/outer/fast-note-sync-webgui` |
| 服务端仓库 | `https://github.com/HughArch/fast-note-sync-service`（你的 fork） |
| 构建产物 | 本仓库 `frontend/`（被 `//go:embed` 打包） |

## 服务端现状（本仓库）

后端**不需要修改**即可签发 Flowix 所需 token：

- `POST /api/token` 接受任意 `clientType`（备注名）和 `client`（scope 的 `c:` 维度）
- `protocol` 支持 `rest,ws`，`function` 支持 `*`
- `vaults` 支持逗号分隔白名单

前端构建产物位于 `frontend/`（由 Go `embed` 打包进二进制），**源码不在本仓库**：

```
/code/outer/fast-note-sync-service/
├── frontend/                 # 构建产物：assets/*.js、*.html（会被 embed）
└── docs/                     # 本文档所在位置

/code/outer/fast-note-sync-webgui/   # 前端源码（React + Vite + TS）
└── src/components/user/token-manager.tsx
```

### 创建令牌表单的当前字段

| 字段 | 含义 | 现状 |
|---|---|---|
| 备注名称 | 写入 `auth_token.client_type`，仅作标识 | 自由输入，placeholder `e.g. Obsidian-Mobile, MyServer` |
| 客户端维度 | 写入 scope 的 `c:` | 自由输入，placeholder `e.g. ObsidianPlugin, *` |
| 协议 | `p:` 维度 | 复选框 rest / ws / mcp |
| 有效期 | `expiredDays` | 数字输入 |
| 功能限制 | `f:` 维度 | 输入框 |
| Vault 限制 | `vaults` 白名单 | 输入框 |

问题：没有 Flowix 预设，用户看到 placeholder 容易把客户端维度填成 `ObsidianPlugin`，再配 Flowix 客户端就会 315。

## 待办事项

### P0 — 让 Web 端能正确生成 Flowix 令牌

- [ ] **客户端维度改为预设 + 自定义**
  - 提供 `flowix` / `obsidianPlugin` / `*` 三个预设，保留自定义输入
  - 选中 `flowix` 时自动勾选 `rest` + `ws`
- [ ] **备注名称默认值**
  - 选择 `flowix` 预设时，备注名称默认填 `Flowix`
- [ ] **提示文案**
  - 在客户端维度旁说明：“Flowix 客户端需要使用 `flowix`（或 `*`）”
  - Vault 限制旁说明：“留空表示不限制；Flowix 每个笔记本绑定一个 vault”
- [ ] **构建并替换产物**
  - 在 `/code/outer/fast-note-sync-webgui` 执行构建
  - 将 `dist/` 复制到 `/code/outer/fast-note-sync-service/frontend/`（包含 `.br` / `.gz`）
  - 重新构建 Go 服务端以更新 embed

前端侧的具体文件、行号和实现建议见：
`/code/outer/fast-note-sync-webgui/docs/FLOWIX-INTEGRATION.md`

### P1 — 降低误配概率

- [ ] 令牌列表展示 scope 中的 `c:` 维度，而不是只展示备注名称
- [ ] 创建/编辑时校验：备注名称与客户端维度不同（避免把备注名误当成权限维度）
- [ ] 前端对 `vaults` 输入做非空/逗号格式校验
- [ ] README 增加“接入第三方客户端（Flowix）”一节，列出所需 scope

### P2 — 可选增强

- [ ] 服务端增加一键签发接口，如 `POST /api/token/flowix`，固定
      `protocol=rest,ws`、`client=flowix`、`function=*`，只要求传 `vaults` 与备注名
- [ ] 前端增加“复制 Flowix 配置”按钮，输出 `{ api, apiToken, vault }` JSON
- [ ] 令牌轮换时保留原 scope，避免轮换后类型变化

## 实施步骤（P0）

1. 修改 `/code/outer/fast-note-sync-webgui/src/components/user/token-manager.tsx`
   - 新增客户端维度预设（按钮组或 Select + 自定义）
   - 选择 `flowix` 时联动协议复选框与备注名
2. 修改对应 i18n 文案（`src/i18n/locales/*.ts`，至少 `zh-CN` 与 `en`）
3. 构建：
   ```bash
   cd /code/outer/fast-note-sync-webgui
   pnpm install
   pnpm build
   ```
4. 复制产物（二选一）：
   - 自动（推荐）：在 webgui 根目录建 `.env`，写入
     `DIST_PATH_MAC=/code/outer/fast-note-sync-service/frontend`
     （`scripts/copy-dist.ts` 只区分 win / 非 win，WSL 使用 `mac` 这个 key），
     然后执行 `pnpm dist mac`。
   - 手动：清空 `fast-note-sync-service/frontend/` 后把 `dist/` 内容复制过去。
   注意 `.br` / `.gz` 压缩文件也要一并复制（`vite-plugin-compression` 会在构建时生成）。
5. 重建服务端并部署：
   ```bash
   cd /code/outer/fast-note-sync-service
   go build ./...
   ```
6. 验证：Web 端创建令牌时选择 `flowix`，生成的 scope 应为 `p:rest,ws c:flowix f:*`。

## 验证清单

- [ ] Web 端创建令牌选择 `flowix`，返回的 `scope` 为 `p:rest,ws c:flowix f:*`
- [ ] `vaults` 填中文 vault 名后，响应中的 `vaults` 与输入一致（UTF-8 未损坏）
- [ ] Flowix 客户端绑定该令牌与对应 vault，`NoteSync` 返回 `code=1`
- [ ] 用 `obsidianPlugin` 令牌配 Flowix 客户端时，客户端显示明确的 315 提示（而不是卡住）

## 相关代码位置

| 内容 | 位置 |
|---|---|
| 令牌创建表单 | `/code/outer/fast-note-sync-webgui/src/components/user/token-manager.tsx` |
| 令牌 API 封装 | `/code/outer/fast-note-sync-webgui/src/components/api-handle/token-handle.ts` |
| 令牌签发后端 | `internal/service/token_service.go` |
| Scope 解析与校验 | `pkg/app/permission.go` |
| Vault 白名单校验 | `pkg/util/vault_access.go` |
| WS 权限拦截 | `internal/routers/websocket_router/interceptor.go` |
| Flowix 客户端 | `flowix/app/flowix-fns` |
