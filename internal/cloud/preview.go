package cloud

import (
	"context"
	"fmt"
	"io"
	"mime"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"easyshare/internal/cloud/objectstore"
)

const maxTextPreviewBytes int64 = 1 << 20

// PreviewKind 描述前端可采用的预览器类型，而不是暴露存储实现细节。
type PreviewKind string

const (
	PreviewUnsupported PreviewKind = "unsupported"
	PreviewImage       PreviewKind = "image"
	PreviewPDF         PreviewKind = "pdf"
	PreviewText        PreviewKind = "text"
)

// Preview 是 Core 返回给桌面端的预览能力描述。
type Preview struct {
	Key         string      `json:"key"`
	Name        string      `json:"name"`
	Kind        PreviewKind `json:"kind"`
	ContentType string      `json:"contentType"`
	Size        int64       `json:"size"`
	ContentURL  string      `json:"contentUrl,omitempty"`
	Text        string      `json:"text,omitempty"`
	Truncated   bool        `json:"truncated,omitempty"`
}

// PreviewInfo 返回文件预览能力；文本内容限量内联，图片和 PDF 由内容端点流式提供。
func (s *Service) PreviewInfo(ctx context.Context, key string) (Preview, error) {
	if strings.TrimSpace(key) == "" {
		return Preview{}, fmt.Errorf("preview: key must not be empty")
	}
	info, err := s.store.HeadObject(ctx, objectstore.ObjectRef{Bucket: s.bucket, Key: key})
	if err != nil {
		return Preview{}, fmt.Errorf("preview head %s: %w", key, err)
	}
	contentType := normalizedContentType(info.ContentType, key)
	preview := Preview{Key: key, Name: filepath.Base(key), Kind: DetectPreviewKind(contentType, key), ContentType: contentType, Size: info.Size}
	if preview.Kind != PreviewText {
		return preview, nil
	}

	object, err := s.store.GetObject(ctx, objectstore.GetObjectInput{ObjectRef: objectstore.ObjectRef{Bucket: s.bucket, Key: key}})
	if err != nil {
		return Preview{}, fmt.Errorf("preview read %s: %w", key, err)
	}
	defer object.Body.Close()
	if err := FillTextPreview(&preview, object.Body); err != nil {
		return Preview{}, fmt.Errorf("preview read %s: %w", key, err)
	}
	return preview, nil
}

// FillTextPreview 把对象内容限量读入预览的内联文本字段。
// 超过上限时截断并回退到 UTF-8 字符边界；内容不是有效 UTF-8 时预览类型降级为不支持。
func FillTextPreview(preview *Preview, body io.Reader) error {
	data, err := io.ReadAll(io.LimitReader(body, maxTextPreviewBytes+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > maxTextPreviewBytes {
		data = data[:maxTextPreviewBytes]
		preview.Truncated = true
		// 截断点可能落在多字节字符中间，最多回退一个 UTF-8 字符。
		for trim := 0; trim < utf8.UTFMax && !utf8.Valid(data); trim++ {
			data = data[:len(data)-1]
		}
	}
	if !utf8.Valid(data) {
		preview.Kind = PreviewUnsupported
		return nil
	}
	preview.Text = string(data)
	return nil
}

// OpenPreview 打开可流式预览的图片或 PDF，并拒绝潜在主动内容（例如 SVG）。
func (s *Service) OpenPreview(ctx context.Context, key string) (Preview, io.ReadCloser, error) {
	preview, err := s.PreviewInfo(ctx, key)
	if err != nil {
		return Preview{}, nil, err
	}
	if preview.Kind != PreviewImage && preview.Kind != PreviewPDF {
		return preview, nil, fmt.Errorf("preview type %s cannot be streamed", preview.Kind)
	}
	object, err := s.store.GetObject(ctx, objectstore.GetObjectInput{ObjectRef: objectstore.ObjectRef{Bucket: s.bucket, Key: key}})
	if err != nil {
		return Preview{}, nil, fmt.Errorf("preview open %s: %w", key, err)
	}
	return preview, object.Body, nil
}

// DetectPreviewKind 将 MIME/扩展名归一化为稳定的产品能力。
func DetectPreviewKind(contentType, key string) PreviewKind {
	contentType = normalizedContentType(contentType, key)
	switch contentType {
	case "application/pdf":
		return PreviewPDF
	case "image/jpeg", "image/png", "image/gif", "image/webp", "image/bmp", "image/x-icon", "image/vnd.microsoft.icon":
		return PreviewImage
	}
	if strings.HasPrefix(contentType, "text/") {
		return PreviewText
	}
	switch contentType {
	case "application/json", "application/xml", "application/yaml", "application/x-yaml", "application/toml", "application/javascript":
		return PreviewText
	default:
		return PreviewUnsupported
	}
}

// ContentTypeForKey 按对象键的扩展名推断 MIME 类型。
// 控制面列举对象不返回 contentType（S3 ListObjectsV2 本就没有该字段），故由客户端推断。
func ContentTypeForKey(key string) string {
	return normalizedContentType("", key)
}

func normalizedContentType(contentType, key string) string {
	contentType = strings.TrimSpace(strings.ToLower(contentType))
	if parsed, _, err := mime.ParseMediaType(contentType); err == nil && parsed != "" {
		contentType = parsed
	}
	if contentType == "" || contentType == "application/octet-stream" {
		extension := strings.ToLower(filepath.Ext(key))
		if detected := mime.TypeByExtension(extension); detected != "" {
			if parsed, _, err := mime.ParseMediaType(detected); err == nil {
				contentType = parsed
			}
		}
		if contentType == "" || contentType == "application/octet-stream" {
			contentType = fallbackContentType(extension)
		}
	}
	if contentType == "" {
		return "application/octet-stream"
	}
	return contentType
}

func fallbackContentType(extension string) string {
	switch extension {
	case ".pdf":
		return "application/pdf"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".ico":
		return "image/x-icon"
	case ".json":
		return "application/json"
	case ".xml":
		return "application/xml"
	case ".yaml", ".yml":
		return "application/yaml"
	case ".toml":
		return "application/toml"
	case ".txt", ".md", ".markdown", ".log", ".csv", ".ini", ".conf", ".cfg", ".go", ".rs", ".py", ".java", ".js", ".ts", ".css", ".html", ".htm", ".sh", ".ps1":
		return "text/plain"
	default:
		return "application/octet-stream"
	}
}
