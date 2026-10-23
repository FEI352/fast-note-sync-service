package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/haierkeys/fast-note-sync-service/internal/config"
	"go.uber.org/zap"
)

type fakeCDNStore struct {
	mu      sync.Mutex
	objects map[string]string // key -> content type
	puts    int
	failPut bool
	putCh   chan string
}

func newFakeCDNStore() *fakeCDNStore {
	return &fakeCDNStore{objects: map[string]string{}, putCh: make(chan string, 16)}
}

func (f *fakeCDNStore) Exists(_ context.Context, key string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.objects[key]
	return ok, nil
}

func (f *fakeCDNStore) Put(_ context.Context, key, localPath, contentType string) error {
	if _, err := os.Stat(localPath); err != nil {
		return err
	}
	f.mu.Lock()
	f.puts++
	if f.failPut {
		f.mu.Unlock()
		f.putCh <- ""
		return os.ErrPermission
	}
	f.objects[key] = contentType
	f.mu.Unlock()
	f.putCh <- key
	return nil
}

func newTestCDN(t *testing.T, store *fakeCDNStore, mutate func(*config.AttachmentCDNConfig)) *attachmentCDNService {
	t.Helper()
	cfg := &config.AttachmentCDNConfig{
		IsEnable:          true,
		CDNDomain:         "img.example.com/",
		PathPrefix:        "/fns/",
		KeySecret:         "test-secret",
		UploadConcurrency: 1,
		UploadQueueSize:   10,
		RetryInterval:     "1h",
	}
	if mutate != nil {
		mutate(cfg)
	}
	s := newAttachmentCDNServiceWithStore(cfg, store, "", zap.NewNop())
	t.Cleanup(func() { _ = s.Stop(context.Background()) })
	return s
}

func writeTempAttachment(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "file.dat")
	if err := os.WriteFile(p, []byte("png-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func waitPut(t *testing.T, store *fakeCDNStore) string {
	t.Helper()
	select {
	case k := <-store.putCh:
		return k
	case <-time.After(3 * time.Second):
		t.Fatal("upload not triggered")
		return ""
	}
}

func TestAttachmentCDN_ObjectKeyStableAndVersioned(t *testing.T) {
	s := newTestCDN(t, newFakeCDNStore(), nil)
	obj := AttachmentObject{UID: 7, SavePath: "storage/vault/u_7/file/f_1/file.dat", Path: "img/A.PNG", ETag: "123", Mtime: 100}

	k1 := s.ObjectKey(obj)
	if k1 != s.ObjectKey(obj) {
		t.Fatal("object key should be deterministic")
	}
	if !strings.HasPrefix(k1, "fns/7/") || !strings.HasSuffix(k1, ".png") {
		t.Fatalf("unexpected key %q", k1)
	}
	obj2 := obj
	obj2.ETag = "456"
	if s.ObjectKey(obj2) == k1 {
		t.Fatal("object key must change when content changes")
	}
	if strings.Contains(k1, "img") || strings.Contains(k1, "A.") {
		t.Fatal("object key must not leak the vault path")
	}
}

func TestAttachmentCDN_ResolveUploadsThenRedirects(t *testing.T) {
	store := newFakeCDNStore()
	s := newTestCDN(t, store, nil)
	obj := AttachmentObject{UID: 1, SavePath: writeTempAttachment(t), Path: "a.png", ETag: "h", Mtime: 1}

	if _, ok := s.ResolveURL(context.Background(), obj); ok {
		t.Fatal("first request should fall back to local serving")
	}
	key := waitPut(t, store)
	if store.objects[key] != "image/png" {
		t.Fatalf("content type = %q", store.objects[key])
	}

	var u string
	var ok bool
	for i := 0; i < 100 && !ok; i++ {
		if u, ok = s.ResolveURL(context.Background(), obj); !ok {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !ok {
		t.Fatal("expected redirect after upload")
	}
	if u != "https://img.example.com/"+key {
		t.Fatalf("unexpected url %q", u)
	}
}

func TestAttachmentCDN_EnqueueDedupAndExistingObject(t *testing.T) {
	store := newFakeCDNStore()
	s := newTestCDN(t, store, nil)
	obj := AttachmentObject{UID: 1, SavePath: writeTempAttachment(t), Path: "a.jpg", ETag: "h", Mtime: 1}

	s.Enqueue(obj)
	s.Enqueue(obj)
	waitPut(t, store)
	time.Sleep(50 * time.Millisecond)
	if store.puts != 1 {
		t.Fatalf("puts = %d, want 1", store.puts)
	}

	// After restart (fresh state) an existing object is detected via HEAD, no re-upload
	// 重启后（状态清空）通过 HEAD 识别已存在的对象，不重复上传
	s2 := newTestCDN(t, store, nil)
	if _, ok := s2.ResolveURL(context.Background(), obj); !ok {
		t.Fatal("existing object should redirect immediately")
	}
	if store.puts != 1 {
		t.Fatalf("puts = %d, want 1", store.puts)
	}
}

func TestAttachmentCDN_FiltersAndFailure(t *testing.T) {
	store := newFakeCDNStore()
	s := newTestCDN(t, store, func(c *config.AttachmentCDNConfig) {
		c.AllowedExts = []string{"png", ".JPG"}
		c.MinSize = 10
	})
	p := writeTempAttachment(t)

	if _, ok := s.ResolveURL(context.Background(), AttachmentObject{UID: 1, SavePath: p, Path: "a.md", ETag: "h"}); ok {
		t.Fatal("disallowed ext should not redirect")
	}
	s.Enqueue(AttachmentObject{UID: 1, SavePath: p, Path: "small.png", ETag: "h", Size: 5})
	s.Enqueue(AttachmentObject{UID: 1, SavePath: p, Path: "a.md", ETag: "h", Size: 50})
	time.Sleep(50 * time.Millisecond)
	if store.puts != 0 {
		t.Fatalf("puts = %d, want 0", store.puts)
	}

	store.failPut = true
	obj := AttachmentObject{UID: 1, SavePath: p, Path: "b.JPG", ETag: "h", Size: 50}
	s.Enqueue(obj)
	waitPut(t, store)
	time.Sleep(20 * time.Millisecond)
	// Failed upload is not retried before retry-interval // 失败后在重试间隔内不重试
	if _, ok := s.ResolveURL(context.Background(), obj); ok {
		t.Fatal("failed upload should not redirect")
	}
	time.Sleep(50 * time.Millisecond)
	if store.puts != 1 {
		t.Fatalf("puts = %d, want 1", store.puts)
	}
}

func TestAttachmentCDN_DisabledOrInvalidConfig(t *testing.T) {
	if NewAttachmentCDNService(nil, "", nil).Enabled() {
		t.Fatal("nil config should be disabled")
	}
	if NewAttachmentCDNService(&config.AttachmentCDNConfig{IsEnable: true}, "", nil).Enabled() {
		t.Fatal("incomplete config should be disabled")
	}
	s := NewAttachmentCDNService(&config.AttachmentCDNConfig{}, "", nil)
	if _, ok := s.ResolveURL(context.Background(), AttachmentObject{SavePath: "x", Path: "a.png"}); ok {
		t.Fatal("disabled service must not redirect")
	}
	s.Enqueue(AttachmentObject{SavePath: "x", Path: "a.png"})
	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}
