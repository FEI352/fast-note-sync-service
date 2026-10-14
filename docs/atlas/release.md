---
kind: part
parent: README.md
code: [.github/workflows/, pkg/fileurl/service_release.go, internal/task/task_check_version.go, internal/routers/api_router/handler_admin_control.go, docker/, internal/app/version.go]
verified: 2026-10-14 @ 4cf8ffac
---
# 发布与升级

> **30 秒版**：上游停更以后，fork 有自己的发行线。推一个 `v3.6.4` 这样的 tag，GitHub Actions 就测试、构建六个平台的程序包、发布 GHCR 容器镜像，最后发 Release。部署好的服务端检查更新、一键升级时，只认这个 fork 的 Release。

## 边界

负责：服务端的版本号、Release 和容器镜像的构建发布、服务端的版本检查和后台一键升级。

不负责：插件的发布（插件没有改动，直接用上游发布的版本，插件里的版本检查也看上游）；后台没有单独的发布，它随服务端发布；数据库结构升级（见[笔记库与存储](storage.md)）。

## 怎么运转

1. **打 tag。** 在要发布的提交上打 `v*` tag 并推送，触发 [fork-release.yml](../../.github/workflows/fork-release.yml)。
2. **构建。** 工作流先跑 `go test ./...`，再为 linux（amd64、arm64、armv7）、darwin（amd64、arm64）、windows（amd64）构建静态程序，打包成 `fast-note-sync-service-<tag>-<系统>-<架构>.tar.gz`。版本号从 tag 注入，不读 [version.go](../../internal/app/version.go) 里写的值。
3. **容器镜像和 Release。** 构建 amd64 和 arm64 的容器镜像，推到 `ghcr.io/liu-zhengdong/fast-note-sync-service`；容器镜像成功后才发 Release。Release 包和容器镜像来自同一个提交。
4. **检查更新。** 服务端定时查 fork 的 Release 列表（[task_check_version.go](../../internal/task/task_check_version.go)），地址写死在 [service_release.go](../../pkg/fileurl/service_release.go)。
5. **一键升级。** 管理员在后台点升级，服务端按 tag 和当前平台下载对应的包，替换程序后重启（[handler_admin_control.go](../../internal/routers/api_router/handler_admin_control.go) 的 `Upgrade`）。GitHub 连不上时升级直接失败，不会退回去下载上游的程序。

新装有两种方式，都只从本仓库下载：一键安装脚本（[quest_install.sh](../../scripts/quest_install.sh)，从 master 取脚本、从 Release 取程序包，没有 CNB 镜像），或 Docker（`ghcr.io/liu-zhengdong/fast-note-sync-service`）。部署和切换容器镜像的步骤见运行手册 [fork-release](../runbook/fork-release.zh-CN.md)。

## 为什么这样设计

- **升级只认 fork**：fork 的功能上游没有，从上游升级等于把功能降级掉。
- **Go 的 module 路径不改**，仍是 `github.com/haierkeys/fast-note-sync-service`。它只是编译用的标识，和从哪里下载升级无关；保留它，合并上游代码时不用全局改 import。

## 坑

- **上游留下的工作流还在**：`release.yml`、`alpha-release.yml`、`mirror-to-cnb.yml`。它们都带 `github.actor == 'haierkeys'` 判断，在本仓库里只会显示为"已跳过"，不会真的发布。→ 在 Actions 页面看到它们被跳过是正常的。
- **第一次推送的 GHCR 包默认是私有的**。→ 部署机器拉镜像失败。→ 在 GitHub Packages 里把它设成公开，或者部署机器用带 `read:packages` 的凭据登录。
- **后台一键升级只换容器里的程序，不换容器镜像**。→ 之后重建容器，又回到容器镜像里的旧程序。→ 重建容器时用对应版本的新容器镜像。
- **两个服务端同时写同一个数据库会损坏数据**。→ 切换容器镜像时先停旧的、备份数据库，再起新的。
- **版本号接着上游往下编**：上游最后一个 tag 是 `3.6.1`（不带 `v`），本仓库从 `v3.6.2` 开始。不再跟上游，所以不用考虑撞号。
- **装了上游版本的服务端，一键升级只看上游**。→ 升不到本仓库的版本。→ 先用一键安装脚本或本仓库的容器镜像手动换一次，之后一键升级就走本仓库。
