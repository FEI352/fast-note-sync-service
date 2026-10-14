---
kind: part
parent: README.md
code: [internal/service/backup_service.go, internal/service/git_sync_service.go, internal/service/storage_service.go, internal/task/, pkg/storage/]
verified: 2026-10-14 @ 4cf8ffac
---
# 备份、镜像与 Git

> **30 秒版**：服务端的后台任务把笔记库复制到别处：定时打包成 ZIP 归档、单向镜像到远程存储、或者推送到一个 Git 仓库。目的是即使服务器出事，笔记也还在别的地方有一份。三种都是单向的：只从笔记库往外写，不从外面读回来。

## 边界

负责：备份和 Git 推送的配置、调度、执行、历史记录和过期清理；对接各种存储协议。

不负责：笔记库本身怎么存（见[笔记库与存储](storage.md)）；设备之间的同步（见[同步](sync.md)）。

## 怎么运转

**存储目标。** 一个用户可以配置多个存储目标：本地目录、S3、阿里云 OSS、Cloudflare R2、MinIO、WebDAV，每种一个适配器（[pkg/storage/](../../pkg/storage/)）。

**备份。** 每条备份配置指定笔记库、存储目标和类型（[backup_service.go](../../internal/service/backup_service.go)）：

| 类型 | 做什么 |
|---|---|
| 全量 | 把整个笔记库打成一个 ZIP |
| 增量 | 只打包上次备份以后改过的内容 |
| 镜像 | 把笔记库的文件逐个同步到远程存储，保持目录结构 |

备份任务每分钟检查一次，到时间的配置就执行（[task_backup.go](../../internal/task/task_backup.go)）。超过保留天数的旧归档自动删除。

**Git 推送。** 每条 Git 配置对应一个笔记库和一个远程仓库，工作区在 `storage/git_workspace/<uid>/<配置 ID>/`（[git_sync_service.go](../../internal/service/git_sync_service.go)，用 go-git 实现，不依赖系统里的 git 命令）。笔记写入时，笔记服务通知 Git 服务；Git 服务按以下步骤执行：

1. 检查或初始化本地工作区；
2. 从远端拉取最新提交；
3. 把笔记库里改过的笔记和配置导出到工作区；
4. 提交并推送。

没有变化时不写历史记录。任务结束后释放内存，并定期对工作区做垃圾回收。

## 坑

- **Git 远端被别处改过时，拉取曾经报 "object not found"**。→ 推送失败，笔记没有备份上去。→ 上游修过一次。再出现时，可以用后台的"清理工作区"删掉本地工作区，下一次推送会重新克隆。
- **在 Git 远端直接改笔记，改动不会回到笔记库**。→ 而且下一次导出可能把同一路径覆盖掉。→ Git 只当备份看，要改笔记在 Obsidian 或后台里改。
