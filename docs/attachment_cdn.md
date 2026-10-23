# 附件 CDN 分流（Attachment CDN Offload）

服务器带宽较小时（如 5Mbps），插件开启「附件云预览」后浏览图片、视频会很卡，因为每个附件都要从服务器传下来。
本功能把附件镜像到 **兼容 S3 的对象存储**（如缤纷云 S4、Cloudflare R2、MinIO 等），预览时由服务器返回一个很小的 **302 跳转**，
附件数据改由 CDN 传输。**插件无需任何修改**。

## 工作流程

1. 附件通过插件同步到服务器（WebSocket / REST / MCP）后，服务器在后台把文件上传到存储桶。
2. 插件请求 `GET /api/file?...&token=...`（或 `/attachments/...` 静态入口）：
   - 服务器照常校验 token、查找文件；
   - 若该版本已在存储桶中 → `302 Location: https://<cdn-domain>/<path-prefix>/<uid>/xx/<hash>.<ext>`；
   - 若尚未上传 → 仍由服务器返回文件，并加入后台上传队列，下次访问即走 CDN。
3. 服务重启后，首次访问会用 `HEAD` 向存储桶确认对象是否存在，不会重复上传。

对象名由 `HMAC-SHA256(key-secret, uid + 存储路径 + 内容哈希 + 修改时间)` 生成：

- 不包含笔记路径，外人无法猜出附件地址；
- 附件内容变化后对象名随之变化，对象设置了 `Cache-Control: public, max-age=31536000, immutable`，CDN 可放心长期缓存；
- 重命名、删除附件不会出错（旧对象不再被引用）。

## 缤纷云 S4 配置示例

1. 在缤纷云控制台创建存储桶，并在「CDN」中添加加速域名，加速源选择该存储桶（需备案域名）。
   - 存储桶为公开读，或在 CDN 中开启私有桶回源均可。
2. 创建 AccessKey。
3. 修改 `config.yaml`：

```yaml
attachment-cdn:
  is-enable: true
  endpoint: "https://s3.bitiful.net"
  region: "cn-east-1"
  bucket-name: "my-notes"
  access-key-id: "xxx"
  access-key-secret: "xxx"
  use-path-style: true
  cdn-domain: "https://img.example.com"
  path-prefix: "fns-attachments"
```

4. 重启服务。插件端保持「附件云预览」开启即可。

> 已在服务器上的旧附件不会主动批量上传：第一次被预览时会自动补传；或者在插件中重新全量同步一次。

## 注意事项

- 上传到对象存储会占用服务器 **上行** 带宽，但每个附件只上传一次；默认 `upload-concurrency: 1`，避免影响同步。
- 302 跳转响应默认缓存 60 秒（`redirect-cache-seconds`）。
- 本功能未实现防盗链；对象名不可猜测，但拿到链接的人可以直接访问。
- 修改 `key-secret`（或在其为空时修改 `security.auth-token-key`）后，所有附件会重新生成对象名并重新上传。
- 附件永久删除后，存储桶中的对象不会自动删除。
