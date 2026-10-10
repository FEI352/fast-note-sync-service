# 在线 MCP 工具与搜索

Fast Note Sync Service 提供 Streamable HTTP `/api/mcp` 和兼容 SSE `/api/mcp/sse`。在线 Agent 需要能访问部署的公网 HTTPS 地址；本地开发改动本身不会创建公网服务。认证支持现有 API Token 与 OAuth。OAuth 部署配置见 [运行手册](mcp-oauth-stytch.zh-CN.md)。

## 工具发现与兼容

`tools/list` 展示 14 个工具：

| 类型 | 工具 |
| --- | --- |
| 笔记读取 | `note_search`, `note_get`, `note_links`, `note_history_search` |
| 笔记修改 | `note_write`, `note_delete`, `note_rename`, `note_search_replace` |
| 附件 | `file_search`, `file_read`, `file_write` |
| 生命周期 | `trash_action` |
| Vault | `vault_manage`, `vault_search` |

旧工具仍可通过 `tools/call` 调用，但不再出现在发现响应中，定义的 `_meta` 标记 `deprecated: true` 与 `seeAlso`。`note_write` 复用 `note_create_or_update`，`note_search_replace` 复用 `note_replace`。旧 append/prepend/frontmatter 工具保留兼容；新客户端可读取、修改后写入。

## 搜索参数与返回

`note_search` 搜索单个 Vault；`vault_search` 搜索所有 Token 可访问 Vault 的笔记，可用 `vaults: ["Work", "Personal"]` 限定。每条结果含 `vault`、`id`、`path`、`version`、`size`、`ctime`、`mtime`，可选 `snippet`。结果合并后统一排序、分页。

- `query`：大小写不敏感的字面子串，可省略以列出全部。
- `mode`：`auto` 匹配路径或正文；`path` 只匹配路径；`content` 只匹配正文；`fuzzy` 匹配路径内按顺序出现的查询字符。
- `includeSnippet`：返回最多 200 个 Unicode 字符的正文片段，默认关闭。
- `tags`：所有指定标签都必须存在，匹配 YAML tags 或行内标签，大小写不敏感。
- `dateFrom`、`dateTo`：包含边界的修改时间范围，使用带时区的 RFC3339，例如 `2026-10-01T00:00:00+08:00`。
- `minSize`、`maxSize`：包含边界的字节数过滤。
- `sortBy`：`mtime`（默认）、`ctime`、`path`、`size`；`sortOrder`：`desc`（默认）或 `asc`。相同值按 Vault、路径、ID 升序打破平局。
- `page`：从 1 开始；`pageSize`：1–100，默认 20。

输出统一为 `{results, total, page, pageSize, totalPages, hasMore}`。超过末页返回空数组并保留总数。非法参数或无权访问指定 Vault 返回错误。

```json
{"name":"vault_search","arguments":{"query":"项目计划","mode":"auto","tags":["work"],"includeSnippet":true,"page":1,"pageSize":20}}
```

`file_search` 使用 `query` 匹配路径，支持上述分页、排序、时间和大小过滤以及 `mimePrefix`（如 `image/`）。MIME 按扩展名推断，未知扩展名为 `application/octet-stream`，不会读取二进制文件正文。

`note_history_search` 支持同样的笔记搜索参数和可选精确 `path`；`keyword` 是 `query` 的兼容别名，两者不能同时提供。它搜索当前和回收站笔记可关联的历史版本，时间过滤作用于版本创建时间。结果额外含 `noteId`，`id` 为历史版本 ID。已失去当前/回收站笔记关联的孤立历史暂不支持枚举。

旧 `note_list` / `file_list` 保持 `vault`、`count`、`notes` / `files` 返回字段，新增 `total`、`page`、`pageSize`、`totalPages`、`hasMore`；默认每页 100，可显式翻页。

## 组合操作

`note_links` 默认一次返回 `backlinks` 和 `outlinks`，每项包含原有 `vault/path/count/links`。可选 `direction: in|out|both`，兼容 `backlinks|outlinks`。

`trash_action` 要求 `type: note|file|folder` 与 `action: delete|restore|purge|rename`。note/file 支持全部动作；delete/restore/purge 要求非空 `path`，rename 要求 `oldPath/newPath`。purge 永久删除指定条目。folder 当前仅支持递归 delete，其他动作明确报不支持。旧 recycle_clear 工具仍保留原有全库清空调用方式。

`vault_manage` 支持 `action: list|get|create|rename|delete`。get/rename/delete 要求正整数 `id`；create/rename 使用 `name`，兼容旧 `vault` 参数。create 不接受已有正整数 ID。

所有组合工具复用现有权限、Vault 白名单与同步通知逻辑。省略 Vault 时选择可访问的客户端默认 Vault，或第一个可访问 Vault。跨库搜索在读取笔记前过滤白名单。

## 当前性能与验证范围

增强搜索逐页收集候选记录并执行过滤，正文、标签和片段按需读取，不依赖 Bleve 是否启用。这保证过滤总数及跨库分页完整，但大库可能产生较多数据库请求与内存占用；尚未做索引加速或并发跨库查询。并发修改数据时，多页扫描不提供快照一致性。

自动化测试覆盖工具发现、旧别名调用、参数校验、超过 100 条候选的分页、跨库合并排序、标签/片段、文件 MIME、历史搜索和权限拒绝。真实公网 MCP Inspector 与在线 Agent 授权接入需要部署后验证。
