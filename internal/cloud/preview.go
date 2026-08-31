// Package cloud 保留桌面端网盘视图所需的类型与预览辅助：File/Preview 是 Wails 绑定
// 透传给前端的数据结构，ContentTypeForKey/DetectPreviewKind/FillTextPreview 支撑
// 控制面对象列表的预览能力推断。Core 直连 S3 的 Service 与 /api/cloud/* 路由已随
// KI-5 清理删除（P2 起云盘走控制面预签名 URL，见 internal/drive）。
package cloud

import (
	"io"
	"mime"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const maxTextPreviewBytes int64 = 1 << 20

// File 是网盘文件条目（控制面对象经 driveObjectToFile 转换后的形态，前端契约）。
type File struct {
	Key          string      `json:"key"`
	Name         string      `json:"name"`
	Size         int64       `json:"size"`
	ContentType  string      `json:"contentType"`
	LastModified time.Time   `json:"lastModified"`
	PreviewKind  PreviewKind `json:"previewKind"`
}

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
