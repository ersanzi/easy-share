//go:build windows

package cfmount

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"easyshare/internal/cfapi"
	"easyshare/internal/drive"

	"golang.org/x/sys/windows"
)

// fakeClient 是控制面客户端的假实现：内存里几个文件，支持 Objects/Open。
//
// 用它而不是起真实 HTTP 服务：本用例要验证的是**适配层 + CfApi 挂载**，
// 不是 HTTP 客户端（后者已有自己的测试）。
type fakeClient struct {
	files   map[string][]byte
	objects []drive.Object

	listCalls int
	openCalls []string
}

func (f *fakeClient) Objects(ctx context.Context, token, space string) ([]drive.Object, error) {
	f.listCalls++
	if token == "" {
		return nil, fmt.Errorf("未登录")
	}
	return f.objects, nil
}

func (f *fakeClient) Open(ctx context.Context, token, space, path string) (io.ReadCloser, int64, error) {
	f.openCalls = append(f.openCalls, path)
	b, ok := f.files[path]
	if !ok {
		return nil, 0, fmt.Errorf("云端没有 %s", path)
	}
	return io.NopCloser(strings.NewReader(string(b))), int64(len(b)), nil
}

// testGUID 是本包测试用的固定提供方 GUID。
var testGUID = windows.GUID{
	Data1: 0x2B3C4D5E, Data2: 0x6F70, Data3: 0x4B8C,
	Data4: [8]byte{0x9D, 0x0E, 0x1F, 0x2A, 0x3B, 0x4C, 0x5D, 0x6E},
}

// TestMountThroughAdapter 是全链路验收：真实 cfapi + 本适配层 + 假控制面。
//
// 走的路径与生产完全一致，只是把 HTTP 客户端换成内存实现：
//
//	drive.Object 列表 → Entries（推导目录）→ cfapi.MountSpace
//	    → 建占位符树 → 读文件 → FETCH_DATA → Open（适配层）→ 内容
//
// 断言三件事：树结构对、全部零字节、读到的内容对。
func TestMountThroughAdapter(t *testing.T) {
	if !cfapi.Available() {
		t.Skip("本机无 CfApi")
	}
	root := t.TempDir()

	mod := time.Unix(1767225600, 0)
	fc := &fakeClient{
		files: map[string][]byte{
			"readme.txt":     []byte("top level"),
			"docs/guide.md":  []byte("guide body"),
			"docs/a/deep.md": []byte("deep body"),
		},
		objects: []drive.Object{
			{Path: "readme.txt", Size: 9, LastModified: mod, FileId: 201},
			{Path: "docs/guide.md", Size: 10, LastModified: mod, FileId: 202},
			{Path: "docs/a/deep.md", Size: 9, LastModified: mod, FileId: 203},
		},
	}

	cfg := Config{
		RootPath:     root,
		Space:        drive.SpacePersonal,
		Client:       fc,
		Token:        func() string { return "fake-token" },
		ProviderName: "EasyShareTest",
		ProviderID:   testGUID,
	}

	m, err := cfapi.MountSpace(cfapi.MountOptions{
		RootPath:         root,
		Provider:         cfapi.ProviderInfo{Name: cfg.ProviderName, Version: "0.1.0", ID: cfg.ProviderID},
		List:             List(cfg),
		Open:             Open(cfg),
		SyncRootIdentity: []byte("easyshare-cfmount-test"),
	})
	if err != nil {
		t.Fatalf("MountSpace 失败: %v", err)
	}
	defer func() {
		if err := m.Close(); err != nil {
			t.Errorf("Close 失败: %v", err)
		}
	}()

	// ① 清单拉过一次（挂载时）
	if fc.listCalls != 1 {
		t.Errorf("Objects 调用 %d 次，期望 1", fc.listCalls)
	}

	// ② 目录被推导出来了（控制面没给目录对象）
	for _, d := range []string{"docs", "docs/a"} {
		fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(d)))
		if err != nil {
			t.Fatalf("目录 %s 没建成（推导失败）: %v", d, err)
		}
		if !fi.IsDir() {
			t.Errorf("%s 不是目录", d)
		}
	}

	// ③ 建树阶段一次都没拉内容
	if len(fc.openCalls) != 0 {
		t.Fatalf("建树阶段就拉取了内容：%v", fc.openCalls)
	}

	// ④ 读一个深层文件 → 触发按需拉取
	got, err := os.ReadFile(filepath.Join(root, "docs", "a", "deep.md"))
	if err != nil {
		t.Fatalf("读取 docs/a/deep.md 失败: %v", err)
	}
	if string(got) != "deep body" {
		t.Errorf("内容 = %q，期望 %q", got, "deep body")
	}
	if len(fc.openCalls) != 1 || fc.openCalls[0] != "docs/a/deep.md" {
		t.Errorf("拉取记录 = %v，期望只有 docs/a/deep.md", fc.openCalls)
	}

	// ⑤ 只拉了被读的那个：另一个文件仍零字节
	other := filepath.Join(root, "readme.txt")
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("readme.txt 应仍在: %v", err)
	}
	t.Logf("全链路通过：清单 1 次、按需拉取 %v", fc.openCalls)
}

// rangeClient 是**支持预签名**的假客户端：把它接的 httptest 服务器当作对象存储。
//
// 为什么单独一个类型而不是给 fakeClient 加方法：`Open` 是靠**类型断言**
// （cfg.Client.(Presigner)）分流的。给 fakeClient 加方法会让**所有**用例都切到
// Range 路径，既有用例就不再覆盖整份读取的回退分支。两个类型并存，两条路径都有覆盖。
type rangeClient struct {
	objects []drive.Object
	files   map[string][]byte
	srv     *httptest.Server
	// ranged 记录收到的 Range 头，用来证明走的是区间请求
	ranged []string
	// fullReads 记录不带 Range 的整份请求次数
	fullReads int
}

func newRangeClient(objects []drive.Object, files map[string][]byte) *rangeClient {
	c := &rangeClient{objects: objects, files: files}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 用路径区分对象：/obj/<url-encoded path>
		key := strings.TrimPrefix(r.URL.Path, "/obj/")
		b, ok := c.files[key]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if rh := r.Header.Get("Range"); rh != "" {
			c.ranged = append(c.ranged, key+" "+rh)
		} else {
			c.fullReads++
		}
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(b))
	}))
	return c
}

func (c *rangeClient) Close() { c.srv.Close() }

func (c *rangeClient) Objects(ctx context.Context, token, space string) ([]drive.Object, error) {
	return c.objects, nil
}

func (c *rangeClient) Open(ctx context.Context, token, space, path string) (io.ReadCloser, int64, error) {
	resp, err := http.Get(c.srv.URL + "/obj/" + path)
	if err != nil {
		return nil, 0, err
	}
	return resp.Body, int64(len(c.files[path])), nil
}

// PresignGet 让本类型满足 cfmount.Presigner —— 这就是「生产路径」的标志。
func (c *rangeClient) PresignGet(ctx context.Context, token, space, path string) (string, error) {
	return c.srv.URL + "/obj/" + path, nil
}

// TestMountThroughAdapterRange 与 TestMountThroughAdapter 同类，但客户端
// **实现了 Presigner**，因此走的是**生产会走的 Range 路径**。
//
// 断言重点：建树阶段零请求、读文件时发出的是**带 Range 的请求**（而不是整份）。
func TestMountThroughAdapterRange(t *testing.T) {
	if !cfapi.Available() {
		t.Skip("本机无 CfApi")
	}
	root := t.TempDir()

	mod := time.Unix(1767225600, 0)
	content := []byte("range-path content")
	rc := newRangeClient(
		[]drive.Object{
			{Path: "deep.txt", Size: int64(len(content)), LastModified: mod, FileId: 501},
		},
		map[string][]byte{"deep.txt": content},
	)
	defer rc.Close()

	cfg := Config{
		RootPath:     root,
		Space:        drive.SpacePersonal,
		Client:       rc,
		Token:        func() string { return "tok" },
		ProviderName: "EasyShareRangeTest",
		ProviderID:   testGUID,
	}

	m, err := cfapi.MountSpace(cfapi.MountOptions{
		RootPath:         root,
		Provider:         cfapi.ProviderInfo{Name: cfg.ProviderName, Version: "0.1.0", ID: cfg.ProviderID},
		List:             List(cfg),
		Open:             Open(cfg),
		SyncRootIdentity: []byte("easyshare-range-e2e"),
	})
	if err != nil {
		t.Fatalf("MountSpace: %v", err)
	}
	defer func() { _ = m.Close() }()

	// 建树阶段不该有任何请求
	if len(rc.ranged) != 0 || rc.fullReads != 0 {
		t.Fatalf("建树阶段就发了请求：ranged=%v fullReads=%d", rc.ranged, rc.fullReads)
	}

	got, err := os.ReadFile(filepath.Join(root, "deep.txt"))
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("内容 = %q，期望 %q", got, content)
	}
	// 关键断言：读文件应当走 Range，而不是整份下载
	if len(rc.ranged) == 0 {
		t.Errorf("读取没有走 Range 路径（fullReads=%d）——生产路径未被覆盖", rc.fullReads)
	}
	t.Logf("Range 路径实测：%v（整份读取次数 %d）", rc.ranged, rc.fullReads)
}
