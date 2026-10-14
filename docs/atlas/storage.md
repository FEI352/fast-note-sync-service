---
kind: part
parent: README.md
code: [internal/dao/, internal/domain/, internal/service/, internal/model/, internal/query/, internal/upgrade/, internal/task/task_note_history.go, pkg/keyedmutex/, pkg/writequeue/]
verified: 2026-10-14 @ 4cf8ffac
---
# 笔记库与存储

> **30 秒版**：服务端怎么保存一个用户的全部笔记库：笔记、文件夹、附件、配置、历史版本、回收站，以及搜索用的索引。数据库只存元数据，笔记正文、历史版本和附件都存成文件。每个用户有自己独立的一份数据库。

## 边界

负责：数据的分层和存放位置；写入的并发控制；历史版本和软删除；全文搜索索引；数据库结构升级。

不负责：谁有权读写（见[账号与授权](auth.md)）；数据怎么到达设备（见[同步](sync.md)）；数据怎么备份到外部（见[备份、镜像与 Git](backup-git.md)）。

## 怎么运转

**分层。** 代码按领域分层：入口（WebSocket、REST、MCP 三种路由）只调服务层；服务层（`internal/service`）放业务规则；领域层（`internal/domain`）定义数据结构和仓储接口；数据访问层（`internal/dao`）实现这些接口。`internal/model` 和 `internal/query` 由 `make gen` 从本地 SQLite 库的表结构反向生成，不手改。三种入口共用同一个服务层，所以 AI 通过 MCP 写的笔记、后台里改的笔记、设备同步上来的笔记，走的是同一套规则。

**两级数据库。** 主库存用户、令牌这类全局数据。每个用户另有一份用户库，存他的笔记库和里面的所有内容（[dao.go](../../internal/dao/dao.go) 的 `GetOrCreateDB`）：

| 数据库类型 | 用户库是什么 |
|---|---|
| SQLite（默认） | `storage/database/` 下每个用户一组独立文件 |
| MySQL | 每个用户一个 `user_<uid>` 数据库 |
| PostgreSQL | 每个用户一个 `user_<uid>` schema |

用户库可以单独配置成和主库不同的类型。

**正文存成文件。** 笔记、配置、附件、历史版本的内容都在 `storage/vault/u_<uid>/` 下，按对象 ID 分目录（[dao_helper.go](../../internal/dao/dao_helper.go)）；数据库里只留路径、内容哈希、时间这些字段。早期版本把正文存在数据库里，读取时会顺手迁移到文件。

**写入的并发。** 同一篇笔记的写入用按 key 的互斥锁串行执行（[keyedmutex](../../pkg/keyedmutex/)），保证每次写入都真正执行。SQLite 不擅长并发写，服务端用每个用户一条写队列（[writequeue](../../pkg/writequeue/)）排队。

**历史版本。** 每次写入笔记后发一条消息给历史任务（[task_note_history.go](../../internal/task/task_note_history.go)）。任务等一段时间（默认 10 秒，`history-save-delay`）再生成历史版本；这段时间里的连续编辑只留一份。同步合并时要找的基准版本就从这里按哈希找。

**删除和回收站。** 删除是软删除：记录标为已删除，进回收站，可以恢复。保留期（默认 90 天，`soft-delete-retention-time`）过后由清理任务真正删掉。

**搜索索引。** 有两套。Bleve 全文索引用在笔记列表按正文搜索时，可以用 `fts-bleve-enabled` 关闭，关闭后按正文搜索改走数据库查询。三字索引用在 MCP 的字面搜索（见 [AI 接入](ai-access.md)）。两套都只是加速，正文才是准的。

**结构升级。** 数据库表结构由 gorm 自动迁移；需要搬数据的升级写在 `internal/upgrade/`，按版本号只执行一次（例如重命名笔记时迁移历史版本的归属）。

## 坑

- **笔记重命名时要把历史版本一起迁过去**。→ 没迁的话，重命名后历史版本全丢。→ 修过一次（提交 1ee4a6c2），老数据由 `upgrade_note_history_rename.go` 补迁。动重命名逻辑时检查历史归属。
- **刚写入的笔记，历史版本要过几秒才有**。→ 这期间的合并可能覆盖另一台设备的改动，见[同步](sync.md)的坑。
- **正文文件写失败时，数据库里的新哈希可能对应旧正文**。→ 搜索索引把这种记录标成"脏"，直到下一次写入成功修好。→ 排查"搜到的内容和看到的不一致"时，先比对文件和数据库里的哈希。
- **`internal/model` 和 `internal/query` 是生成的**。→ 手改会在下次 `make gen` 时被覆盖。
  > 待确认：改表结构的完整步骤（先改哪里、再怎么生成）。
