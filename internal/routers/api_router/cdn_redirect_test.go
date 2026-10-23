package api_router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/haierkeys/fast-note-sync-service/internal/app"
	"github.com/haierkeys/fast-note-sync-service/internal/service"
)

type fakeCDN struct {
	url string
	ok  bool
	got service.AttachmentObject
}

func (f *fakeCDN) Enabled() bool { return true }
func (f *fakeCDN) ResolveURL(_ context.Context, obj service.AttachmentObject) (string, bool) {
	f.got = obj
	return f.url, f.ok
}
func (f *fakeCDN) Enqueue(service.AttachmentObject) {}
func (f *fakeCDN) RedirectCacheSeconds() int       { return 60 }
func (f *fakeCDN) Stop(context.Context) error      { return nil }

func TestTryAttachmentCDNRedirect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cdn := &fakeCDN{url: "https://img.example.com/fns/1/ab/abc.png", ok: true}
	a := &app.App{Services: &app.Services{AttachmentCDN: cdn}}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/file?path=a.png", nil)

	if !TryAttachmentCDNRedirect(c, a, 1, "storage/f_1/file.dat", "a.png", "h", 9) {
		t.Fatal("expected redirect")
	}
	if w.Code != http.StatusFound || w.Header().Get("Location") != cdn.url {
		t.Fatalf("code=%d location=%q", w.Code, w.Header().Get("Location"))
	}
	if w.Header().Get("Cache-Control") != "private, max-age=60" {
		t.Fatalf("cache-control=%q", w.Header().Get("Cache-Control"))
	}
	if cdn.got.UID != 1 || cdn.got.ETag != "h" || cdn.got.Mtime != 9 {
		t.Fatalf("unexpected object %+v", cdn.got)
	}

	// Not yet uploaded: fall through to local serving // 尚未上传：继续本地返回
	cdn.ok = false
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = httptest.NewRequest(http.MethodGet, "/api/file?path=a.png", nil)
	if TryAttachmentCDNRedirect(c2, a, 1, "p", "a.png", "h", 9) {
		t.Fatal("should not redirect before upload")
	}

	// Disabled / nil app: never redirect // 未启用：从不跳转
	if TryAttachmentCDNRedirect(c2, &app.App{}, 1, "p", "a.png", "h", 9) {
		t.Fatal("nil services should not redirect")
	}
}
