package drive

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// uploadFixture 起一对测试服务器：控制面签发预签名，对象存储收 PUT。
func uploadFixture(t *testing.T) (*Client, *string, func()) {
	t.Helper()
	received := new(string)
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		*received = string(raw)
		w.WriteHeader(http.StatusOK)
	}))
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":200,"data":{"url":"` + store.URL + `/k","path":"a.txt"}}`))
	}))
	client := New(control.URL)
	return client, received, func() {
		control.Close()
		store.Close()
	}
}

func TestUploadFileSendsContentAndReportsProgress(t *testing.T) {
	client, received, closeFn := uploadFixture(t)
	defer closeFn()

	content := strings.Repeat("abcdefghij", 5000) // 50KB，确保多次 Read
	local := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(local, []byte(content), 0o644); err != nil {
		t.Fatalf("写测试文件失败：%v", err)
	}

	var lastSent, lastTotal int64
	calls := 0
	err := client.UploadFile(context.Background(), "tok", SpacePersonal, "sample.txt", local, func(sent, total int64) {
		calls++
		lastSent, lastTotal = sent, total
	})
	if err != nil {
		t.Fatalf("UploadFile 出错：%v", err)
	}
	if *received != content {
		t.Errorf("上传内容与源文件不一致（收到 %d 字节，期望 %d）", len(*received), len(content))
	}
	if calls == 0 {
		t.Error("未回调进度")
	}
	if lastTotal != int64(len(content)) {
		t.Errorf("总字节数错误：%d", lastTotal)
	}
	if lastSent != lastTotal {
		t.Errorf("最终进度未到 100%%：sent=%d total=%d", lastSent, lastTotal)
	}
}

func TestUploadFileAcceptsNilProgress(t *testing.T) {
	client, received, closeFn := uploadFixture(t)
	defer closeFn()

	local := filepath.Join(t.TempDir(), "x.txt")
	if err := os.WriteFile(local, []byte("hi"), 0o644); err != nil {
		t.Fatalf("写测试文件失败：%v", err)
	}
	if err := client.UploadFile(context.Background(), "tok", SpacePersonal, "x.txt", local, nil); err != nil {
		t.Fatalf("UploadFile 出错：%v", err)
	}
	if *received != "hi" {
		t.Errorf("内容错误：%q", *received)
	}
}

func TestUploadFileRejectsDirectory(t *testing.T) {
	client, _, closeFn := uploadFixture(t)
	defer closeFn()

	err := client.UploadFile(context.Background(), "tok", SpacePersonal, "d", t.TempDir(), nil)
	if err == nil {
		t.Fatal("上传目录应当报错")
	}
	if !strings.Contains(err.Error(), "目录") {
		t.Errorf("错误信息不明确：%v", err)
	}
}

func TestUploadFileMissingLocalFile(t *testing.T) {
	client, _, closeFn := uploadFixture(t)
	defer closeFn()

	err := client.UploadFile(context.Background(), "tok", SpacePersonal, "a.txt",
		filepath.Join(t.TempDir(), "nope.txt"), nil)
	if err == nil {
		t.Fatal("文件不存在应当报错")
	}
}

// 空文件也要能上传（size 0，进度回调不应触发但不得报错）。
func TestUploadFileEmptyFile(t *testing.T) {
	client, received, closeFn := uploadFixture(t)
	defer closeFn()

	local := filepath.Join(t.TempDir(), "empty.txt")
	if err := os.WriteFile(local, nil, 0o644); err != nil {
		t.Fatalf("写测试文件失败：%v", err)
	}
	if err := client.UploadFile(context.Background(), "tok", SpacePersonal, "empty.txt", local, func(sent, total int64) {
		t.Errorf("空文件不应回调进度：sent=%d total=%d", sent, total)
	}); err != nil {
		t.Fatalf("UploadFile 出错：%v", err)
	}
	if *received != "" {
		t.Errorf("内容应为空：%q", *received)
	}
}
