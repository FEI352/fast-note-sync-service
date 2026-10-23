package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/haierkeys/fast-note-sync-service/internal/config"
	"github.com/haierkeys/fast-note-sync-service/pkg/util"
	"go.uber.org/zap"
)

// AttachmentObject describes a stored attachment that can be offloaded to the CDN.
// AttachmentObject 描述一个可分流到 CDN 的附件。
type AttachmentObject struct {
	UID      int64  // User ID // 用户 ID
	SavePath string // Local physical path // 本地物理路径
	Path     string // Vault path (used for extension / content type) // 库内路径（用于判断后缀和类型）
	ETag     string // Content hash (or path hash) of the current version // 当前版本的内容哈希（或路径哈希）
	Mtime    int64  // Modification time of the current version // 当前版本修改时间
	Size     int64  // File size, 0 means unknown // 文件大小，0 表示未知
}

// AttachmentCDNService mirrors attachments to S3-compatible storage and resolves CDN URLs.
// AttachmentCDNService 将附件镜像到兼容 S3 的对象存储，并解析 CDN 预览地址。
type AttachmentCDNService interface {
	// Enabled reports whether CDN offload is enabled // 是否启用
	Enabled() bool
	// ResolveURL returns the CDN URL when the object is already in the bucket;
	// otherwise it schedules a background upload and returns ok=false (caller serves locally).
	// ResolveURL 对象已在存储桶中时返回 CDN 地址；否则安排后台上传并返回 ok=false（调用方本地返回）。
	ResolveURL(ctx context.Context, obj AttachmentObject) (string, bool)
	// Enqueue schedules a background upload // 安排后台上传
	Enqueue(obj AttachmentObject)
	// RedirectCacheSeconds client cache seconds for the 302 response // 302 响应缓存秒数
	RedirectCacheSeconds() int
	// Stop stops the upload workers // 停止上传协程
	Stop(ctx context.Context) error
}

// cdnObjectStore is the minimal object storage contract used by the CDN service.
// cdnObjectStore 为 CDN 服务使用的最小对象存储接口。
type cdnObjectStore interface {
	Exists(ctx context.Context, key string) (bool, error)
	Put(ctx context.Context, key string, localPath string, contentType string) error
}

const (
	cdnStatePending = iota + 1
	cdnStateUploaded
	cdnStateFailed
)

type cdnEntry struct {
	state int
	at    time.Time
}

type attachmentCDNService struct {
	cfg           *config.AttachmentCDNConfig
	store         cdnObjectStore
	logger        *zap.Logger
	secret        []byte
	cdnBase       string
	allowedExts   map[string]bool
	retryInterval time.Duration

	states sync.Map // object key -> *cdnEntry
	mu     sync.Mutex
	queue  chan AttachmentObject
	wg     sync.WaitGroup
	stopCh chan struct{}
	once   sync.Once
}

// NewAttachmentCDNService creates the CDN service. A disabled service is returned when
// the feature is off or misconfigured, so callers never need nil checks.
// NewAttachmentCDNService 创建 CDN 服务。未启用或配置错误时返回禁用状态的服务，调用方无需判空。
func NewAttachmentCDNService(cfg *config.AttachmentCDNConfig, fallbackSecret string, logger *zap.Logger) AttachmentCDNService {
	if logger == nil {
		logger = zap.NewNop()
	}
	if cfg == nil || !cfg.IsEnable {
		return &attachmentCDNService{logger: logger}
	}
	if err := validateAttachmentCDNConfig(cfg); err != nil {
		logger.Error("attachment cdn disabled: invalid config", zap.Error(err))
		return &attachmentCDNService{logger: logger}
	}
	store, err := newS3CDNObjectStore(cfg)
	if err != nil {
		logger.Error("attachment cdn disabled: create s3 client failed", zap.Error(err))
		return &attachmentCDNService{logger: logger}
	}
	return newAttachmentCDNServiceWithStore(cfg, store, fallbackSecret, logger)
}

func validateAttachmentCDNConfig(cfg *config.AttachmentCDNConfig) error {
	var missing []string
	if strings.TrimSpace(cfg.Endpoint) == "" {
		missing = append(missing, "endpoint")
	}
	if strings.TrimSpace(cfg.BucketName) == "" {
		missing = append(missing, "bucket-name")
	}
	if strings.TrimSpace(cfg.AccessKeyID) == "" {
		missing = append(missing, "access-key-id")
	}
	if strings.TrimSpace(cfg.AccessKeySecret) == "" {
		missing = append(missing, "access-key-secret")
	}
	if strings.TrimSpace(cfg.CDNDomain) == "" {
		missing = append(missing, "cdn-domain")
	}
	if len(missing) > 0 {
		return fmt.Errorf("attachment-cdn missing: %s", strings.Join(missing, ", "))
	}
	return nil
}

func newAttachmentCDNServiceWithStore(cfg *config.AttachmentCDNConfig, store cdnObjectStore, fallbackSecret string, logger *zap.Logger) *attachmentCDNService {
	secret := cfg.KeySecret
	if secret == "" {
		secret = fallbackSecret
	}
	if secret == "" {
		secret = "fast-note-sync-attachment-cdn"
	}

	exts := make(map[string]bool, len(cfg.AllowedExts))
	for _, e := range cfg.AllowedExts {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" {
			continue
		}
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		exts[e] = true
	}

	retry := 5 * time.Minute
	if d, err := util.ParseDuration(cfg.RetryInterval); err == nil && d > 0 {
		retry = d
	}

	workers := cfg.UploadConcurrency
	if workers <= 0 {
		workers = 1
	}
	queueSize := cfg.UploadQueueSize
	if queueSize <= 0 {
		queueSize = 10000
	}

	s := &attachmentCDNService{
		cfg:           cfg,
		store:         store,
		logger:        logger,
		secret:        []byte(secret),
		cdnBase:       normalizeCDNBase(cfg.CDNDomain),
		allowedExts:   exts,
		retryInterval: retry,
		queue:         make(chan AttachmentObject, queueSize),
		stopCh:        make(chan struct{}),
	}
	for i := 0; i < workers; i++ {
		s.wg.Add(1)
		go s.worker()
	}
	logger.Info("attachment cdn enabled",
		zap.String("endpoint", cfg.Endpoint),
		zap.String("bucket", cfg.BucketName),
		zap.String("cdnDomain", s.cdnBase),
		zap.Int("workers", workers))
	return s
}

func normalizeCDNBase(domain string) string {
	domain = strings.TrimRight(strings.TrimSpace(domain), "/")
	if domain != "" && !strings.Contains(domain, "://") {
		domain = "https://" + domain
	}
	return domain
}

func (s *attachmentCDNService) Enabled() bool { return s.store != nil }

func (s *attachmentCDNService) RedirectCacheSeconds() int {
	if s.cfg == nil || s.cfg.RedirectCacheSeconds < 0 {
		return 0
	}
	return s.cfg.RedirectCacheSeconds
}

// eligible reports whether the attachment should be offloaded // 判断附件是否需要分流
func (s *attachmentCDNService) eligible(obj AttachmentObject) bool {
	if !s.Enabled() || obj.SavePath == "" {
		return false
	}
	if len(s.allowedExts) > 0 && !s.allowedExts[strings.ToLower(filepath.Ext(obj.Path))] {
		return false
	}
	if s.cfg.MinSize > 0 && obj.Size > 0 && obj.Size < s.cfg.MinSize {
		return false
	}
	return true
}

// ObjectKey builds a deterministic but unguessable key that changes with the content version,
// so CDN caches can be kept forever.
// ObjectKey 生成确定但不可猜测的对象名，内容版本变化则对象名变化，CDN 可永久缓存。
func (s *attachmentCDNService) ObjectKey(obj AttachmentObject) string {
	mac := hmac.New(sha256.New, s.secret)
	fmt.Fprintf(mac, "%d\n%s\n%s\n%d", obj.UID, filepath.ToSlash(filepath.Clean(obj.SavePath)), obj.ETag, obj.Mtime)
	sum := hex.EncodeToString(mac.Sum(nil))[:40]
	ext := strings.ToLower(filepath.Ext(obj.Path))
	if len(ext) > 16 || strings.ContainsAny(ext, "/\\?#% ") {
		ext = ""
	}
	return path.Join(strings.Trim(s.cfg.PathPrefix, "/"), strconv.FormatInt(obj.UID, 10), sum[:2], sum+ext)
}

func (s *attachmentCDNService) objectURL(key string) string {
	parts := strings.Split(key, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return s.cdnBase + "/" + strings.Join(parts, "/")
}

func (s *attachmentCDNService) ResolveURL(ctx context.Context, obj AttachmentObject) (string, bool) {
	if !s.eligible(obj) {
		return "", false
	}
	key := s.ObjectKey(obj)

	if v, ok := s.states.Load(key); ok {
		e := v.(*cdnEntry)
		switch e.state {
		case cdnStateUploaded:
			return s.objectURL(key), true
		case cdnStatePending:
			return "", false
		case cdnStateFailed:
			if time.Since(e.at) < s.retryInterval {
				return "", false
			}
		}
	}

	// Unknown (e.g. after restart): ask the bucket once // 状态未知（如重启后）：向存储桶确认一次
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	exists, err := s.store.Exists(checkCtx, key)
	if err != nil {
		s.logger.Warn("attachment cdn head object failed", zap.String("key", key), zap.Error(err))
	}
	if exists {
		s.states.Store(key, &cdnEntry{state: cdnStateUploaded, at: time.Now()})
		return s.objectURL(key), true
	}
	s.enqueue(obj, key)
	return "", false
}

func (s *attachmentCDNService) Enqueue(obj AttachmentObject) {
	if !s.eligible(obj) {
		return
	}
	s.enqueue(obj, s.ObjectKey(obj))
}

func (s *attachmentCDNService) enqueue(obj AttachmentObject, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.states.Load(key); ok {
		e := v.(*cdnEntry)
		if e.state == cdnStateUploaded || e.state == cdnStatePending {
			return
		}
		if e.state == cdnStateFailed && time.Since(e.at) < s.retryInterval {
			return
		}
	}
	select {
	case <-s.stopCh:
		return
	default:
	}
	select {
	case s.queue <- obj:
		s.states.Store(key, &cdnEntry{state: cdnStatePending, at: time.Now()})
	default:
		s.logger.Warn("attachment cdn upload queue full, skip", zap.String("path", obj.Path))
	}
}

func (s *attachmentCDNService) worker() {
	defer s.wg.Done()
	for {
		select {
		case <-s.stopCh:
			return
		case obj := <-s.queue:
			s.upload(obj)
		}
	}
}

func (s *attachmentCDNService) upload(obj AttachmentObject) {
	key := s.ObjectKey(obj)
	start := time.Now()

	err := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if exists, _ := s.store.Exists(ctx, key); exists {
			return nil
		}
		contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(obj.Path)))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		return s.store.Put(ctx, key, obj.SavePath, contentType)
	}()

	if err != nil {
		s.states.Store(key, &cdnEntry{state: cdnStateFailed, at: time.Now()})
		s.logger.Warn("attachment cdn upload failed",
			zap.Int64("uid", obj.UID), zap.String("path", obj.Path), zap.String("key", key), zap.Error(err))
		return
	}
	s.states.Store(key, &cdnEntry{state: cdnStateUploaded, at: time.Now()})
	s.logger.Info("attachment cdn uploaded",
		zap.Int64("uid", obj.UID), zap.String("path", obj.Path), zap.String("key", key),
		zap.Duration("cost", time.Since(start)))
}

func (s *attachmentCDNService) Stop(ctx context.Context) error {
	if s.stopCh == nil {
		return nil
	}
	s.once.Do(func() { close(s.stopCh) })
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ---------------------------------------------------------------------------
// S3-compatible object store // 兼容 S3 的对象存储
// ---------------------------------------------------------------------------

type s3CDNObjectStore struct {
	client *s3.Client
	bucket string
}

func newS3CDNObjectStore(cfg *config.AttachmentCDNConfig) (*s3CDNObjectStore, error) {
	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.AccessKeySecret, "")),
		awsconfig.WithRegion(region),
	)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/")
	if !strings.Contains(endpoint, "://") {
		endpoint = "https://" + endpoint
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = cfg.PathStyle()
		// Many S3-compatible services do not support the newer default checksums
		// 许多兼容 S3 的服务不支持新版 SDK 默认的校验和
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	})
	return &s3CDNObjectStore{client: client, bucket: cfg.BucketName}, nil
}

func (s *s3CDNObjectStore) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err == nil {
		return true, nil
	}
	var re interface{ HTTPStatusCode() int }
	if errors.As(err, &re) && re.HTTPStatusCode() == http.StatusNotFound {
		return false, nil
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) && (apiErr.ErrorCode() == "NotFound" || apiErr.ErrorCode() == "NoSuchKey") {
		return false, nil
	}
	return false, err
}

func (s *s3CDNObjectStore) Put(ctx context.Context, key string, localPath string, contentType string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          f,
		ContentLength: aws.Int64(st.Size()),
		ContentType:   aws.String(contentType),
		CacheControl:  aws.String("public, max-age=31536000, immutable"),
	})
	return err
}
