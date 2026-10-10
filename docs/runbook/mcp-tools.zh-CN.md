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

`note_search` 与 `vault_search` 的非空 `auto/path/content` 查询使用持久化 SQL 三字符索引，不依赖 Bleve 是否启用。索引按小写 Unicode 字符生成，保留空格和标点；先通过索引筛选候选，再用原有字面子串规则精确验证，标签、时间、大小过滤和跨库排序分页保持一致。

已有笔记在首次查询时逐批补建，因此首次查询仍需读取未建索引的正文；后续查询只读取索引候选及未就绪记录。新建、修改正文时同步维护索引，未就绪记录始终进入候选扫描。索引与笔记保存在同一用户数据库中，额外空间随每篇笔记不同的三字符组合数量增长；每次正文修改也会增加索引写入成本。

少于三个 Unicode 字符的查询、空查询、`fuzzy`、文件和历史搜索仍使用扫描。当前候选筛选使用查询的首个三字符片段，常见片段可能产生较多候选；数据库仍检查笔记元数据与索引就绪状态，以保证未建索引和失败写入不会漏搜。全局排序仍收集全部匹配结果，内存随结果数量增长。补建和正文写入使用 64 个按用户分组的锁，同一组内会等待当前候选页处理完成。并发修改数据时，多页查询不提供快照一致性。

正文在服务端的独立文件中；使用 `rg` 仍需遍历正文并关联数据库里的用户、Vault 和删除状态，因此当前没有增加外部 `rg` 依赖。

自动化测试覆盖工具发现、旧别名调用、参数校验、超过 100 条候选的分页、跨库合并排序、标签/片段、文件 MIME、历史搜索和权限拒绝。真实公网 MCP Inspector 与在线 Agent 授权接入需要部署后验证。
