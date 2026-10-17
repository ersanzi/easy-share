package spacedav

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"easyshare/internal/drive"

	"golang.org/x/net/webdav"
)

// TestProductionWiringCanSeek 验证**生产装配方式**能通过 Seek 契约。
//
// 与其它用例的区别：那些用假客户端（自定义的 rangeAwareClient）。
// 这条用的是生产代码里那个构造方式——`drive.New(base)` 返回的 *drive.Client，
// 只是把它的 baseURL 指向一个假的控制面——从而验证
//
//	生产装配（spacemount.go 的 client := drive.New(base) + spacedav.New）
//	  → 真实 *drive.Client
//	  → **满足 openRangeer**（KI-9 修复的前提）
//
// 假客户端全绿但生产类型不满足接口，是 KI-9 那类「静默退化」的典型形态；
// 本条把生产类型本身钉住。
func TestProductionWiringCanSeek(t *testing.T) {
	// 最小假控制面：只回预签名（GET 下载）与列举
	var objectURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/easyshare/drive/objects", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"code":200,"data":[{"path":"f.txt","size":5,"lastModified":"2026-01-01T00:00:00Z","fileId":1}]}`)
	})
	mux.HandleFunc("/easyshare/drive/presign-get", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"code":200,"data":{"url":"`+objectURL+`","path":"f.txt"}}`)
	})
	ctrl := httptest.NewServer(mux)
	defer ctrl.Close()

	// 假对象存储：支持 Range（ServeContent）
	obj := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "f.txt", time.Time{}, bytes.NewReader([]byte("hello")))
	}))
	defer obj.Close()
	objectURL = obj.URL

	// **生产装配方式**
	client := drive.New(ctrl.URL)
	fs := New(Options{Client: client, Space: drive.SpacePersonal,
		Token: func() string { return "tok" }, Label: "test"})

	h := &webdav.Handler{Prefix: "/", FileSystem: fs, LockSystem: webdav.NewMemLS()}
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/f.txt")
	if err != nil {
		t.Fatalf("GET 失败: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("生产装配下 GET 状态码 = %d，期望 200（KI-9 复发？）", resp.StatusCode)
	}
	if string(body) != "hello" {
		t.Fatalf("内容 = %q，期望 %q", body, "hello")
	}
	_ = context.Background
}
