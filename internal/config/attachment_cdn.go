package config

// AttachmentCDNConfig Attachment CDN offload configuration.
// Attachments synced to the server are mirrored to an S3-compatible object storage,
// and attachment preview requests are redirected (302) to the CDN URL, so the
// attachment bytes no longer go through the server's own bandwidth.
// AttachmentCDNConfig 附件 CDN 分流配置。
// 同步到服务器的附件会镜像到兼容 S3 的对象存储，附件预览请求通过 302 跳转到 CDN 地址，
// 附件数据不再占用服务器自身带宽。
type AttachmentCDNConfig struct {
	// IsEnable whether to enable attachment CDN offload // 是否启用附件 CDN 分流
	IsEnable bool `yaml:"is-enable" default:"false"`

	// Endpoint S3-compatible API endpoint, e.g. https://s3.bitiful.net // 兼容 S3 的 API 地址，如 https://s3.bitiful.net
	Endpoint string `yaml:"endpoint"`
	// Region region, e.g. cn-east-1 // 区域，如 cn-east-1
	Region string `yaml:"region" default:"us-east-1"`
	// BucketName bucket name // 存储桶名称
	BucketName string `yaml:"bucket-name"`
	// AccessKeyID access key // 访问密钥 ID
	AccessKeyID string `yaml:"access-key-id"`
	// AccessKeySecret secret key // 访问密钥 Secret
	AccessKeySecret string `yaml:"access-key-secret"`
	// UsePathStyle use path-style addressing (endpoint/bucket/key) // 是否使用路径风格访问（endpoint/bucket/key）
	UsePathStyle *bool `yaml:"use-path-style" default:"true"`

	// CDNDomain public CDN (or bucket) domain used to build preview URLs, e.g. https://img.example.com
	// CDNDomain 用于拼接预览地址的 CDN（或存储桶公开）域名，如 https://img.example.com
	CDNDomain string `yaml:"cdn-domain"`
	// PathPrefix object key prefix in the bucket // 对象在存储桶中的路径前缀
	PathPrefix string `yaml:"path-prefix" default:"fns-attachments"`
	// KeySecret secret used to generate unguessable object keys; falls back to security.auth-token-key when empty
	// KeySecret 生成不可猜测对象名的密钥；为空时使用 security.auth-token-key
	KeySecret string `yaml:"key-secret"`

	// AllowedExts file extensions to offload; empty means all files // 需要分流的文件后缀；为空表示全部文件
	AllowedExts []string `yaml:"allowed-exts"`
	// MinSize files smaller than this size (bytes) are served by the server directly // 小于该大小（字节）的文件仍由服务器直接返回
	MinSize int64 `yaml:"min-size" default:"0"`

	// UploadConcurrency number of concurrent upload workers // 后台上传并发数
	UploadConcurrency int `yaml:"upload-concurrency" default:"1"`
	// UploadQueueSize max pending upload tasks // 上传队列长度
	UploadQueueSize int `yaml:"upload-queue-size" default:"10000"`
	// RetryInterval wait time before retrying a failed upload // 上传失败后重试的等待时间
	RetryInterval string `yaml:"retry-interval" default:"5m"`
	// RedirectCacheSeconds client cache time (seconds) of the 302 response // 302 跳转响应的客户端缓存秒数
	RedirectCacheSeconds int `yaml:"redirect-cache-seconds" default:"60"`
}

// PathStyle returns whether path-style addressing is enabled // 是否使用路径风格访问
func (c *AttachmentCDNConfig) PathStyle() bool {
	return c.UsePathStyle == nil || *c.UsePathStyle
}
