package drive

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWebDAVReadWrite 验证回环 WebDAV 服务无需认证即可读写文件。
// 服务只监听 127.0.0.1，本机进程即可访问，因此不提供认证。
func TestWebDAVReadWrite(t *testing.T) {
	root := t.TempDir()
	service := NewService(root)
	if err := service.Start(0); err != nil {
		t.Fatal(err)
	}
	defer service.Stop(context.Background())

	url := "http://" + service.Addr() + "/note.txt"
	request, _ := http.NewRequest(http.MethodPut, url, strings.NewReader("hello"))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode >= 300 {
		t.Fatalf("PUT status = %d", response.StatusCode)
	}
	if data, err := os.ReadFile(filepath.Join(root, "note.txt")); err != nil || string(data) != "hello" {
		t.Fatalf("file = %q, error = %v", data, err)
	}
}
