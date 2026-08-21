package cloud

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"easyshare/internal/cloud/objectstore"
	"easyshare/internal/cloud/objectstore/memory"
)

func TestPreviewInfoClassifiesAndReadsText(t *testing.T) {
	store := memory.New()
	service := NewService(store, "bucket")
	_, err := store.PutObject(context.Background(), objectstore.PutObjectInput{
		ObjectRef:   objectstore.ObjectRef{Bucket: "bucket", Key: "notes/readme.md"},
		Body:        strings.NewReader("hello\nworld"),
		Size:        11,
		ContentType: "application/octet-stream",
	})
	if err != nil {
		t.Fatal(err)
	}

	preview, err := service.PreviewInfo(context.Background(), "notes/readme.md")
	if err != nil {
		t.Fatal(err)
	}
	// ContentType 归一化会优先参考系统 mime 表（Windows 上 .md 可能是 text/markdown），
	// 断言限定"文本类 + 内容可读"，不耦合具体子类型。
	if preview.Kind != PreviewText || preview.Text != "hello\nworld" || !strings.HasPrefix(preview.ContentType, "text/") {
		t.Fatalf("unexpected preview: %+v", preview)
	}
}

func TestPreviewInfoTruncatesLargeText(t *testing.T) {
	store := memory.New()
	service := NewService(store, "bucket")
	body := strings.Repeat("a", int(maxTextPreviewBytes)+16)
	_, err := store.PutObject(context.Background(), objectstore.PutObjectInput{
		ObjectRef:   objectstore.ObjectRef{Bucket: "bucket", Key: "large.txt"},
		Body:        strings.NewReader(body),
		Size:        int64(len(body)),
		ContentType: "text/plain; charset=utf-8",
	})
	if err != nil {
		t.Fatal(err)
	}

	preview, err := service.PreviewInfo(context.Background(), "large.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Truncated || len(preview.Text) != int(maxTextPreviewBytes) {
		t.Fatalf("expected truncated preview, got length=%d truncated=%v", len(preview.Text), preview.Truncated)
	}
}

func TestPreviewInfoTruncatesAtUTF8Boundary(t *testing.T) {
	store := memory.New()
	service := NewService(store, "bucket")
	body := strings.Repeat("a", int(maxTextPreviewBytes)-1) + "你" + "tail"
	_, err := store.PutObject(context.Background(), objectstore.PutObjectInput{
		ObjectRef:   objectstore.ObjectRef{Bucket: "bucket", Key: "utf8.txt"},
		Body:        strings.NewReader(body),
		Size:        int64(len(body)),
		ContentType: "text/plain; charset=utf-8",
	})
	if err != nil {
		t.Fatal(err)
	}

	preview, err := service.PreviewInfo(context.Background(), "utf8.txt")
	if err != nil {
		t.Fatal(err)
	}
	if preview.Kind != PreviewText || !preview.Truncated || !utf8.ValidString(preview.Text) {
		t.Fatalf("expected valid truncated UTF-8 preview, got %+v", preview)
	}
	if strings.Contains(preview.Text, "你") {
		t.Fatal("expected incomplete trailing rune to be removed")
	}
}

func TestPreviewInfoPreservesWhitespaceInObjectKey(t *testing.T) {
	store := memory.New()
	service := NewService(store, "bucket")
	key := " report.txt "
	_, err := store.PutObject(context.Background(), objectstore.PutObjectInput{
		ObjectRef:   objectstore.ObjectRef{Bucket: "bucket", Key: key},
		Body:        strings.NewReader("content"),
		Size:        int64(len("content")),
		ContentType: "text/plain",
	})
	if err != nil {
		t.Fatal(err)
	}

	preview, err := service.PreviewInfo(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Key != key || preview.Text != "content" {
		t.Fatalf("preview = %+v, want original object key and content", preview)
	}
}
func TestPreviewInfoRejectsNonUTF8Text(t *testing.T) {
	store := memory.New()
	service := NewService(store, "bucket")
	body := string([]byte{'o', 'k', 0xff})
	_, err := store.PutObject(context.Background(), objectstore.PutObjectInput{
		ObjectRef:   objectstore.ObjectRef{Bucket: "bucket", Key: "legacy.txt"},
		Body:        strings.NewReader(body),
		Size:        int64(len(body)),
		ContentType: "text/plain",
	})
	if err != nil {
		t.Fatal(err)
	}

	preview, err := service.PreviewInfo(context.Background(), "legacy.txt")
	if err != nil {
		t.Fatal(err)
	}
	if preview.Kind != PreviewUnsupported || preview.Text != "" {
		t.Fatalf("expected unsupported non-UTF-8 text, got %+v", preview)
	}
}

func TestDetectPreviewKindRejectsActiveOrUnsupportedContent(t *testing.T) {
	tests := []struct {
		contentType string
		key         string
		want        PreviewKind
	}{
		{"image/png", "photo.png", PreviewImage},
		{"application/pdf", "guide.pdf", PreviewPDF},
		{"", "config.json", PreviewText},
		{"image/svg+xml", "unsafe.svg", PreviewUnsupported},
		{"application/zip", "archive.zip", PreviewUnsupported},
	}
	for _, test := range tests {
		if got := DetectPreviewKind(test.contentType, test.key); got != test.want {
			t.Errorf("DetectPreviewKind(%q, %q) = %q, want %q", test.contentType, test.key, got, test.want)
		}
	}
}
