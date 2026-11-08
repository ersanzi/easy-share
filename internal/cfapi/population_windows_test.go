//go:build windows

package cfapi

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// 对照微软 cfapi.h，而非用本包常量彼此比较，防止 SDK 数值错位再次漏检。
// https://learn.microsoft.com/windows/win32/api/cfapi/ne-cfapi-cf_population_policy_primary
// https://learn.microsoft.com/windows/win32/api/cfapi/ne-cfapi-cf_register_flags
func TestPopulationSDKValues(t *testing.T) {
	for _, c := range []struct {
		name      string
		got, want uint32
	}{
		{"PARTIAL", PopulationPolicyPartial, 0},
		{"FULL", PopulationPolicyFull, 2},
		{"ALWAYS_FULL", PopulationPolicyAlwaysFull, 3},
		{"NONE", RegisterFlagNone, 0},
		{"UPDATE", RegisterFlagUpdate, 1},
		{"DISABLE_ON_DEMAND_POPULATION_ON_ROOT", RegisterFlagDisableOnDemandPopulationOnRoot, 2},
	} {
		if c.got != c.want {
			t.Errorf("%s=%d, SDK=%d", c.name, c.got, c.want)
		}
	}
	sr, err := NewSyncRoot(testProvider, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sr.Policies.Population.Primary != 3 || sr.Policies.Hydration.Primary != 2 {
		t.Fatalf("主动完整铺树默认策略错误: %+v", sr.Policies)
	}
	// 显式策略仍由调用方控制，不应被新的默认覆盖。
	explicit := SyncPolicies{Population: PopulationPolicy{Primary: PopulationPolicyFull}}
	sr, err = NewSyncRoot(testProvider, nil, nil, &explicit)
	if err != nil || sr.Policies.Population.Primary != 2 {
		t.Fatalf("显式 FULL 策略被改写: %v %v", sr, err)
	}
}

// 子进程是硬超时边界：即便内核枚举或 Close 卡住，也不会拖死 go test。
// 仅使用新建临时 CFAPI 根，绝不访问用户挂载根或 Shell namespace。
func TestPopulationStandardEnumeration(t *testing.T) {
	if os.Getenv("EASYSHARE_POPULATION_CHILD") != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPopulationStandardEnumeration$", "-test.v", "-test.timeout=15s")
		cmd.Env = append(os.Environ(), "EASYSHARE_POPULATION_CHILD=1")
		output, err := cmd.CombinedOutput()
		t.Logf("真实 CFAPI 子进程:\n%s", output)
		if ctx.Err() != nil {
			t.Fatalf("真实标准枚举硬超时: %v", ctx.Err())
		}
		if err != nil {
			t.Fatalf("真实 CFAPI 回归失败: %v", err)
		}
		if !bytes.Contains(output, []byte("POPULATION_REAL_OK")) {
			t.Fatal("子进程未完成真实 CFAPI 验证（不能把 skip 当通过）")
		}
		return
	}
	if !Available() {
		t.Fatal("真实回归要求本机 CfApi 可用")
	}
	root := t.TempDir()
	content := bytes.Repeat([]byte("cloud-only-data-"), 8192)
	entries := []CloudEntry{
		{Path: "docs", IsDir: true, Identity: EncodeIdentity(301)},
		{Path: "docs/nested", IsDir: true, Identity: EncodeIdentity(302)},
		{Path: "docs/read.bin", Size: int64(len(content)), Identity: EncodeIdentity(303)},
		{Path: "unread.bin", Size: int64(len(content)), Identity: EncodeIdentity(304)},
		{Path: "docs/nested/unread.bin", Size: int64(len(content)), Identity: EncodeIdentity(305)},
	}
	var mu sync.Mutex
	var opened []string
	m, err := mountSpaceForTest(t, MountOptions{
		RootPath: root, Provider: testProvider, ProtectedPaths: []string{},
		List: func(context.Context) ([]CloudEntry, error) { return entries, nil },
		Open: func(_ context.Context, p string, offset, length int64) (io.ReadCloser, error) {
			mu.Lock()
			opened = append(opened, p)
			mu.Unlock()
			end := offset + length
			if end > int64(len(content)) {
				end = int64(len(content))
			}
			return io.NopCloser(bytes.NewReader(content[offset:end])), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := m.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()
	// 可写保护路径确实保持 NoMarkInSync，禁止为绕过枚举问题恢复 MARK_IN_SYNC。
	ph := NewPlaceholders([]PlaceholderSpec{{RelativeName: "file", NoMarkInSync: true}, {RelativeName: "dir", IsDir: true, NoMarkInSync: true}})
	for _, info := range ph.Infos {
		if info.Flags&PlaceholderCreateFlagMarkInSync != 0 {
			t.Fatal("NoMarkInSync 被破坏")
		}
	}
	for _, c := range []struct {
		rel  string
		want []string
	}{
		{"", []string{"docs", "unread.bin"}},
		{"docs", []string{"nested", "read.bin"}},
		{"docs/nested", []string{"unread.bin"}},
	} {
		dir := filepath.Join(root, filepath.FromSlash(c.rel))
		started := time.Now()
		names := standardEnumerationWithin(t, dir)
		sort.Strings(names)
		sort.Strings(c.want)
		if !reflect.DeepEqual(names, c.want) {
			t.Fatalf("标准枚举 %s=%v, want %v", c.rel, names, c.want)
		}
		t.Logf("FindFirstFile(%s\\*) 标准枚举耗时 %v", c.rel, time.Since(started))
	}
	mu.Lock()
	before := len(opened)
	mu.Unlock()
	if before != 0 {
		t.Fatalf("仅枚举即下载: %d", before)
	}
	for _, rel := range []string{"docs/read.bin", "unread.bin", "docs/nested/unread.bin"} {
		if n := allocatedSize(t, filepath.Join(root, filepath.FromSlash(rel))); n != 0 {
			t.Fatalf("枚举后 %s 分配=%d", rel, n)
		}
	}
	got, err := os.ReadFile(filepath.Join(root, "docs", "read.bin"))
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("NoMarkInSync 按需水合失败: bytes=%d err=%v", len(got), err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(opened) == 0 {
		t.Fatal("没有真实 FETCH_DATA")
	}
	for _, p := range opened {
		if p != "docs/read.bin" {
			t.Fatalf("未读文件被下载: %v", opened)
		}
	}
	for _, rel := range []string{"unread.bin", "docs/nested/unread.bin"} {
		if n := allocatedSize(t, filepath.Join(root, filepath.FromSlash(rel))); n != 0 {
			t.Fatalf("未读 %s 分配=%d", rel, n)
		}
	}
	t.Log("POPULATION_REAL_OK: 标准根/子目录枚举、NoMarkInSync 按需水合、未读零下载")
}

// windows.FindFirstFile 调用标准 FindFirstFileW，无 ON_DISK_ONLY 或其他枚举旁路。
func standardEnumerationWithin(t *testing.T, dir string) []string {
	t.Helper()
	type result struct {
		names []string
		err   error
	}
	done := make(chan result, 1)
	go func() {
		pattern, err := windows.UTF16PtrFromString(filepath.Join(dir, "*"))
		if err != nil {
			done <- result{err: err}
			return
		}
		var data windows.Win32finddata
		h, err := windows.FindFirstFile(pattern, &data)
		if err != nil {
			done <- result{err: err}
			return
		}
		defer windows.FindClose(h)
		var names []string
		for {
			name := windows.UTF16ToString(data.FileName[:])
			if name != "." && name != ".." {
				names = append(names, name)
			}
			err = windows.FindNextFile(h, &data)
			if err == windows.ERROR_NO_MORE_FILES {
				err = nil
				break
			}
			if err != nil {
				break
			}
		}
		done <- result{names, err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatal(r.err)
		}
		return r.names
	case <-time.After(2 * time.Second):
		t.Fatalf("标准 FindFirstFile/FindNextFile 枚举超过 2s: %s", dir)
		return nil
	}
}

// 注销失败只向上报错；不允许删除或清空根，更不能碰未上传的本地内容。
func TestMountCloseWithoutSessionPreservesRoot(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "pending.txt")
	if err := os.WriteFile(file, []byte("local-only"), 0600); err != nil {
		t.Fatal(err)
	}
	// NUL 确定性触发注销路径转换失败，不影响任何真实已登记根。
	m := &Mount{rootPath: root + "\x00"}
	if err := m.Close(); err != nil {
		t.Fatal("无会话 Close 不应访问或注销根", err)
	}
	if b, err := os.ReadFile(file); err != nil || string(b) != "local-only" {
		t.Fatalf("失败 Close 损坏本地根: %q %v", b, err)
	}
}
