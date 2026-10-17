package spacedav

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"easyshare/internal/drive"

	"golang.org/x/net/webdav"
)

// TestWebDAVDirectoryListingEndToEnd 走真实 handler 验证**列目录**（PROPFIND）可用。
//
// 为什么补它：KI-9 的教训是「单测 FS 层方法 ≠ HTTP 层可用」。
// 目录列举在生产里走的是 PROPFIND，若它也不可用，
// 用户在资源管理器里会看到**空目录**——比报错更迷惑。
func TestWebDAVDirectoryListingEndToEnd(t *testing.T) {
	fc := newRangeAware([]byte("hello"))
	defer fc.Close()
	fc.objects = []drive.Object{
		{Path: "a.txt", Size: 5},
		{Path: "docs/b.md", Size: 3},
	}

	fs := New(Options{Client: fc, Space: drive.SpacePersonal,
		Token: func() string { return "tok" }, Label: "test"})
	h := &webdav.Handler{Prefix: "/", FileSystem: fs, LockSystem: webdav.NewMemLS()}
	srv := httptest.NewServer(h)
	defer srv.Close()

	req, _ := http.NewRequest("PROPFIND", srv.URL+"/", nil)
	req.Header.Set("Depth", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PROPFIND 失败: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 207 {
		head := body
		if len(head) > 200 {
			head = head[:200]
		}
		t.Fatalf("状态码 = %d，期望 207（Multi-Status）；体前段：%q", resp.StatusCode, string(head))
	}
	for _, name := range []string{"a.txt", "docs"} {
		if !strings.Contains(string(body), name) {
			t.Errorf("列举结果里没有 %s", name)
		}
	}
	t.Logf("PROPFIND 207，响应 %d 字节", len(body))
}
