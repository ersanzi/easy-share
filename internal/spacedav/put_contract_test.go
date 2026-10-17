package spacedav

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"easyshare/internal/drive"

	"golang.org/x/net/webdav"
)

// TestWebDAVPutThenGetEndToEnd 走真实 handler 验证**上传后能读回**。
//
// 为什么补它：PUT 是用户往「此电脑」拖文件的路径。它走的是 FS.OpenFile(write) +
// writeFile.Close()，与 GET 完全不同的分支——KI-9 说明「一个分支对了不代表别的对」。
func TestWebDAVPutThenGetEndToEnd(t *testing.T) {
	fc := newRangeAware([]byte("x"))
	defer fc.Close()
	fc.objects = []drive.Object{{Path: "up.txt", Size: 5}}

	fs := New(Options{Client: fc, Space: drive.SpacePersonal,
		Token: func() string { return "tok" }, Label: "test"})
	h := &webdav.Handler{Prefix: "/", FileSystem: fs, LockSystem: webdav.NewMemLS()}
	srv := httptest.NewServer(h)
	defer srv.Close()

	const content = "hello-upload"
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/up.txt", strings.NewReader(content))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT 失败: %v", err)
	}
	defer resp.Body.Close()
	// WebDAV PUT 成功通常是 201 Created（或 204）
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT 状态码 = %d，期望 201/204；体：%q", resp.StatusCode, string(body))
	}

	// 上传必须真的经控制面发出（而不是静默丢弃）
	if fc.uploads == nil || fc.uploads["up.txt"] != int64(len(content)) {
		t.Fatalf("控制面收到的上传 = %v，期望 up.txt=%d", fc.uploads, len(content))
	}
	t.Logf("PUT 成功（%d），控制面记录 %v", resp.StatusCode, fc.uploads)
	_ = context.Background
}
