package cloud

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Service 随 KI-5 删除后，这里只测仍在服役的纯辅助函数：
// FillTextPreview 的限量/UTF-8 边界行为与 DetectPreviewKind 的归类。

func TestFillTextPreviewReadsText(t *testing.T) {
	preview := Preview{Kind: PreviewText}
	if err := FillTextPreview(&preview, strings.NewReader("hello\nworld")); err != nil {
		t.Fatal(err)
	}
	if preview.Text != "hello\nworld" || preview.Truncated {
		t.Fatalf("unexpected preview: %+v", preview)
	}
}

func TestFillTextPreviewTruncatesLargeText(t *testing.T) {
	preview := Preview{Kind: PreviewText}
	if err := FillTextPreview(&preview, strings.NewReader(strings.Repeat("a", int(maxTextPreviewBytes)+16))); err != nil {
		t.Fatal(err)
	}
	if !preview.Truncated || len(preview.Text) != int(maxTextPreviewBytes) {
		t.Fatalf("expected truncated preview, got length=%d truncated=%v", len(preview.Text), preview.Truncated)
	}
}

func TestFillTextPreviewTruncatesAtUTF8Boundary(t *testing.T) {
	body := strings.Repeat("a", int(maxTextPreviewBytes)-1) + "你" + "tail"
	preview := Preview{Kind: PreviewText}
	if err := FillTextPreview(&preview, strings.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	if !preview.Truncated || !utf8.ValidString(preview.Text) {
		t.Fatalf("expected valid truncated UTF-8 preview, got %+v", preview)
	}
	if strings.Contains(preview.Text, "你") {
		t.Fatal("expected incomplete trailing rune to be removed")
	}
}

func TestFillTextPreviewRejectsNonUTF8Text(t *testing.T) {
	preview := Preview{Kind: PreviewText}
	if err := FillTextPreview(&preview, strings.NewReader(string([]byte{'o', 'k', 0xff}))); err != nil {
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
