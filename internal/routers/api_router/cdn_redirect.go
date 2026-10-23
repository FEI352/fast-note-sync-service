package api_router

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/haierkeys/fast-note-sync-service/internal/app"
	"github.com/haierkeys/fast-note-sync-service/internal/service"
)

// TryAttachmentCDNRedirect redirects an attachment request to the CDN when the attachment
// has already been mirrored to the bucket. It returns true if a redirect response was written.
// When the attachment is not in the bucket yet, a background upload is scheduled and false is
// returned, so the caller keeps serving the file from local disk.
// TryAttachmentCDNRedirect 附件已镜像到存储桶时，将请求 302 跳转到 CDN，写入跳转响应后返回 true。
// 附件尚未在存储桶中时会安排后台上传并返回 false，调用方继续从本地磁盘返回文件。
func TryAttachmentCDNRedirect(c *gin.Context, a *app.App, uid int64, savePath, fileName, etag string, mtime int64) bool {
	if a == nil || a.Services == nil || a.AttachmentCDN == nil || !a.AttachmentCDN.Enabled() {
		return false
	}
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		return false
	}

	cdnURL, ok := a.AttachmentCDN.ResolveURL(c.Request.Context(), service.AttachmentObject{
		UID:      uid,
		SavePath: savePath,
		Path:     fileName,
		ETag:     etag,
		Mtime:    mtime,
	})
	if !ok {
		return false
	}

	if sec := a.AttachmentCDN.RedirectCacheSeconds(); sec > 0 {
		c.Header("Cache-Control", "private, max-age="+strconv.Itoa(sec))
	} else {
		c.Header("Cache-Control", "no-store")
	}
	c.Redirect(http.StatusFound, cdnURL)
	c.Abort()
	return true
}
