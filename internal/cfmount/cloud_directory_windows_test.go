//go:build windows

package cfmount

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"easyshare/internal/cfapi"
	"easyshare/internal/drive"
	"golang.org/x/sys/windows"
)

// 使用内存控制面和真实 CFAPI 临时根，不登录、不注册 Shell namespace。
type cloudDirectoryClient struct{ *wbFake }

func (c *cloudDirectoryClient) Objects(ctx context.Context, token, space string) ([]drive.Object, error) {
	rows, err := c.wbFake.Objects(ctx, token, space)
	for i := range rows {
		rows[i].LastModified = time.Unix(1767225600, 0)
		// 确保列表顺序变化不改变身份。
		switch rows[i].Path {
		case "root.txt":
			rows[i].FileId = 101
		case "docs/deep/seed.txt":
			rows[i].FileId = 102
		case "docs/deep/remote.txt":
			rows[i].FileId = 103
		default:
			rows[i].FileId = 104
		}
	}
	return rows, err
}

func TestProtectedCloudDirectoryHydrationAndWriteback(t *testing.T) {
	if !cfapi.Available() {
		t.Skip("本机无 CFAPI")
	}
	base, err := os.MkdirTemp("", "es-isolated-cloud-")
	if err != nil {
		t.Fatal(err)
	}
	// 只有注销成功才允许递归清理，失败时留下绝对路径便于恢复。
	cleanable := true
	t.Cleanup(func() {
		if cleanable {
			if err := os.RemoveAll(base); err != nil {
				t.Errorf("清理 %s: %v", base, err)
			}
		} else {
			t.Errorf("注销未确认，保留临时根: %s", base)
		}
	})
	root := filepath.Join(base, "root")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	f := &cloudDirectoryClient{&wbFake{
		data:     map[string][]byte{"root.txt": []byte("root cloud bytes"), "docs/deep/seed.txt": []byte("deep cloud bytes")},
		versions: map[string]int64{"root.txt": 1, "docs/deep/seed.txt": 1},
	}}
	cfg := Config{RootPath: root, Space: drive.SpacePersonal, Client: f, Token: func() string { return "fake" }, Writable: true}
	engine, err := NewWritebackEngine(WritebackConfig{RootPath: root, StateDir: filepath.Join(base, "state"), Space: cfg.Space, Namespace: "isolated-cloud-test", Client: f, Token: cfg.Token, StableFor: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	remote := List(cfg)
	initial := func(ctx context.Context) ([]cfapi.CloudEntry, error) {
		result, err := engine.Tick(ctx)
		if err != nil {
			return nil, err
		}
		if !cfapi.ProtectPaths(ctx, result.ProtectedPaths) {
			t.Fatal("缺少保护上下文")
		}
		return remote(ctx)
	}
	m, err := cfapi.MountSpace(cfapi.MountOptions{RootPath: root, Provider: cfapi.ProviderInfo{Name: "EasyShareIsolatedCloudTest", Version: "test", ID: windows.GUID{Data1: 0xa9ca12b3, Data2: 0xf199, Data3: 0x401a, Data4: [8]byte{0x85, 0x73, 1, 2, 3, 4, 5, 6}}}, SyncRootIdentity: []byte("isolated-cloud-directory-test"), List: initial, Open: Open(cfg)})
	if err != nil {
		t.Fatal(err)
	}
	cleanable = false
	closed := false
	defer func() {
		if _, exists, err := cfapi.QuerySyncRootStandardInfo(root); err == nil && exists {
			if err := cfapi.UnregisterSyncRoot(root); err != nil {
				t.Errorf("explicit test unregister: %v", err)
			}
		}
	}()
	defer func() {
		if !closed {
			if err := m.Close(); err != nil {
				t.Errorf("Close: %v", err)
			} else {
				cleanable = true
			}
		}
	}()
	// 保护模式不带 MARK_IN_SYNC 的根/深层占位符必须仍能真实 FETCH_DATA。
	for _, name := range []string{"root.txt", "docs/deep/seed.txt"} {
		p := filepath.Join(root, filepath.FromSlash(name))
		before, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if ok, err := writebackReadable(p, before); err != nil || ok {
			t.Fatalf("未水合文件不应可扫描 %s: %v %v", name, ok, err)
		}
		b, err := os.ReadFile(p)
		u, _ := windows.UTF16PtrFromString(p)
		a, _ := windows.GetFileAttributes(u)
		t.Logf("水合后 %s attrs=%x", name, a)
		if err != nil || string(b) != string(f.data[name]) {
			t.Fatalf("保护模式水合 %s: %q %v", name, b, err)
		}
	}
	for _, name := range []string{"docs", "docs/deep"} {
		p := filepath.Join(root, filepath.FromSlash(name))
		info, err := os.Lstat(p)
		if err != nil {
			t.Fatal(err)
		}
		safe, cloud, err := writebackScanEntry(p, info)
		if err != nil || !safe || !cloud {
			t.Fatalf("Cloud目录不可扫描 %s: safe=%v cloud=%v err=%v mode=%v", name, safe, cloud, err, info.Mode())
		}
	}
	reconcile := func() WritebackResult {
		t.Helper()
		result, err := engine.Reconcile(context.Background(), func(_ context.Context, protected []string) error { return m.SyncProtected(remote, protected) })
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Issues) != 0 {
			t.Fatalf("扫描/写回异常: %+v", result.Issues)
		}
		for _, p := range result.ProtectedPaths {
			if p == "docs" || p == "docs/deep" {
				t.Fatalf("Cloud目录被整树保护: %v", result.ProtectedPaths)
			}
		}
		return result
	}
	reconcile()
	time.Sleep(5 * time.Millisecond)
	reconcile()
	if engine.state.Records["docs/deep/seed.txt"] == nil {
		t.Fatal("深层水合文件未建立基线")
	}
	// 已有 Cloud 目录中的普通新增文件和水合文件编辑均必须被扫描到。
	for name, content := range map[string]string{"docs/deep/new.txt": "new local", "docs/deep/seed.txt": "edited local bytes"} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	reconcile()
	time.Sleep(5 * time.Millisecond)
	reconcile()
	if string(f.data["docs/deep/new.txt"]) != "new local" || string(f.data["docs/deep/seed.txt"]) != "edited local bytes" || f.versions["docs/deep/seed.txt"] != 2 {
		t.Fatalf("深层新增/编辑未写回: %+v", f.versions)
	}
	// 普通本地目录仍整树保护，不能把远端内容塞入用户自建目录。
	if err := os.Mkdir(filepath.Join(root, "local-only"), 0700); err != nil {
		t.Fatal(err)
	}
	f.data["local-only/remote.txt"] = []byte("must not project")
	f.versions["local-only/remote.txt"] = 1
	result := reconcile()
	foundLocalProtection := false
	for _, p := range result.ProtectedPaths {
		foundLocalProtection = foundLocalProtection || p == "local-only"
	}
	if !foundLocalProtection {
		t.Fatal("普通本地目录丢失整树保护")
	}
	if _, err := os.Stat(filepath.Join(root, "local-only", "remote.txt")); !os.IsNotExist(err) {
		t.Fatalf("普通本地目录被远端投影侵入: %v", err)
	}
	// 远端新增兄弟项不能被祖先 Cloud 目录保护挡住；已有本地字节绝不能更新。
	// 即使远端版本变化或缺项，保护同步也不得更新/误删现有本地文件。
	f.data["docs/deep/seed.txt"] = []byte("remote changed independently")
	f.versions["docs/deep/seed.txt"]++
	delete(f.data, "root.txt")
	delete(f.versions, "root.txt")
	f.data["docs/deep/remote.txt"] = []byte("new remote bytes")
	f.versions["docs/deep/remote.txt"] = 1
	result, err = engine.Reconcile(context.Background(), func(_ context.Context, protected []string) error { return m.SyncProtected(remote, protected) })
	if err != nil || len(result.Issues) != 2 {
		t.Fatalf("应报告已知的远端改版/缺失冲突，同时继续保护树同步: %+v %v", result, err)
	}
	for _, issue := range result.Issues {
		if issue.Kind != "conflict" || (issue.Path != "docs/deep/seed.txt" && issue.Path != "root.txt") {
			t.Fatalf("非预期冲突: %+v", issue)
		}
	}
	b, err := os.ReadFile(filepath.Join(root, "docs", "deep", "remote.txt"))
	if err != nil || string(b) != "new remote bytes" {
		t.Fatalf("远端新增子项未投影/水合: %q %v", b, err)
	}
	b, err = os.ReadFile(filepath.Join(root, "docs", "deep", "seed.txt"))
	if err != nil || string(b) != "edited local bytes" {
		t.Fatalf("树同步改写本地文件: %q %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(root, "root.txt")); err != nil || string(b) != "root cloud bytes" {
		t.Fatalf("远端缺项误删本地: %q %v", b, err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	cleanable = true
	// 正常 Close 保留注册；只有隔离测试清理显式注销。
	if err := cfapi.UnregisterSyncRoot(root); err != nil {
		t.Fatal(err)
	}
	sr, err := cfapi.NewSyncRoot(cfapi.ProviderInfo{Name: "EasyShareIsolatedCloudTest", Version: "test"}, []byte("cleanup-proof"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfapi.RegisterSyncRoot(root, &sr.Registration, &sr.Policies, cfapi.RegisterFlagNone); err != nil {
		t.Fatal(err)
	}
	cleanable = false
	if err := cfapi.UnregisterSyncRoot(root); err != nil {
		t.Fatal(err)
	}
	cleanable = true
}

func TestWritebackDirectoryReparseClassification(t *testing.T) {
	info, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		attrs, tag uint32
		want       bool
	}{
		{"ordinary", windows.FILE_ATTRIBUTE_DIRECTORY, 0, true},
		{"cloud", windows.FILE_ATTRIBUTE_DIRECTORY | windows.FILE_ATTRIBUTE_REPARSE_POINT, 0x9000001a, true},
		{"cloud variant", windows.FILE_ATTRIBUTE_DIRECTORY | windows.FILE_ATTRIBUTE_REPARSE_POINT, 0x9000f01a, true},
		{"junction", windows.FILE_ATTRIBUTE_DIRECTORY | windows.FILE_ATTRIBUTE_REPARSE_POINT, 0xa0000003, false},
		{"symlink", windows.FILE_ATTRIBUTE_DIRECTORY | windows.FILE_ATTRIBUTE_REPARSE_POINT, 0xa000000c, false},
		{"unknown", windows.FILE_ATTRIBUTE_DIRECTORY | windows.FILE_ATTRIBUTE_REPARSE_POINT, 0x90000042, false},
		{"unpopulated", windows.FILE_ATTRIBUTE_DIRECTORY | windows.FILE_ATTRIBUTE_REPARSE_POINT | 0x40000, 0x9000001a, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := writebackScanAttributes(tc.attrs, tc.tag, info); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}
