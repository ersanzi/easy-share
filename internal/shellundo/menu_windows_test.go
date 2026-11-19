//go:build windows

package shellundo

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

type memoryMenuStore struct {
	mu       sync.Mutex
	values   map[string]map[string]string
	removed  []string
	failPath string
}

func newMemoryMenuStore() *memoryMenuStore {
	return &memoryMenuStore{values: make(map[string]map[string]string)}
}
func (store *memoryMenuStore) put(path string, values map[string]string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if path == store.failPath {
		return errors.New("injected registry write failure")
	}
	if store.values[path] == nil {
		store.values[path] = make(map[string]string)
	}
	for name, value := range values {
		store.values[path][name] = value
	}
	return nil
}
func (store *memoryMenuStore) get(path, name string) (string, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	values, ok := store.values[path]
	if !ok {
		return "", registry.ErrNotExist
	}
	value, ok := values[name]
	if !ok {
		return "", registry.ErrNotExist
	}
	return value, nil
}
func (store *memoryMenuStore) remove(path string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.removed = append(store.removed, path)
	delete(store.values, path)
	delete(store.values, path+`\command`)
	return nil
}

func TestMenuRegistrationScopedToRootAndCurrentExecutable(t *testing.T) {
	store := newMemoryMenuStore()
	cfg := menuConfig{Root: `C:\用户 云盘\current`, Executable: `D:\Apps\Easy Share\easyshare.exe`, Session: "session-one"}
	if err := installMenus(store, cfg); err != nil {
		t.Fatal(err)
	}
	if len(store.values) != 4 {
		t.Fatalf("只能创建 file/Directory 两个自身 verb及 command: %v", store.values)
	}
	for _, path := range menuPaths {
		values := store.values[path]
		if values["MUIVerb"] != "删除（可撤销）" || values["AppliesTo"] != `System.ItemPathDisplay:~="C:\用户 云盘\current\"` {
			t.Fatalf("菜单范围: %v", values)
		}
		if values["MultiSelectModel"] != "Single" {
			t.Fatal("%1 菜单多选会漏项，必须显式单项")
		}
		if store.values[path+`\command`][""] != `"D:\Apps\Easy Share\easyshare.exe" --easyshare-undo-delete "%1"` {
			t.Fatal("命令未按本机 exe 引用或正确加引号")
		}
		if strings.Contains(path, "AllFilesystemObjects") || strings.HasPrefix(path, `Directory\Background`) {
			t.Fatal("不得添加全局/背景删除")
		}
	}
	got, err := readMenuConfig(store, cfg.Executable)
	if err != nil || got != cfg {
		t.Fatalf("读取当前配置: %+v/%v", got, err)
	}
	if _, err := readMenuConfig(store, `D:\other\easyshare.exe`); err == nil {
		t.Fatal("其它 exe 不得使用此菜单")
	}
	store.values[menuPaths[1]]["EasyShareCloudRoot"] = `C:\different`
	if _, err := readMenuConfig(store, cfg.Executable); err == nil {
		t.Fatal("文件与目录注册根不一致应拒绝")
	}
}

func TestMenuUnregisterDoesNotTouchAnotherVerbOrNewSession(t *testing.T) {
	store := newMemoryMenuStore()
	cfg := menuConfig{Root: `C:\cloud`, Executable: `C:\apps\easyshare.exe`, Session: "old"}
	if err := installMenus(store, cfg); err != nil {
		t.Fatal(err)
	}
	other := `*\shell\OtherVendor`
	store.values[other] = map[string]string{"MUIVerb": "Keep"}
	store.values[menuPaths[1]]["EasyShareSession"] = "new"
	if err := removeOwnedMenus(store, cfg); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(store.removed, []string{menuPaths[0]}) || store.values[other] == nil || store.values[menuPaths[1]] == nil {
		t.Fatalf("越权卸载: %+v", store)
	}
}

func TestMenuWriteFailureRollsBackOwnSession(t *testing.T) {
	store := newMemoryMenuStore()
	store.failPath = menuPaths[1] + `\command`
	cfg := menuConfig{Root: `C:\cloud`, Executable: `C:\apps\easyshare.exe`, Session: "failure"}
	if err := installMenus(store, cfg); err == nil {
		t.Fatal("注册错误被吞")
	}
	if len(store.values) != 0 {
		t.Fatalf("部分菜单未撤销: %v", store.values)
	}
}

func isolatedHost(t *testing.T, callback func([]string) error) (*menuHost, string) {
	t.Helper()
	root := canonicalTestDirectory(t)
	if err := os.WriteFile(filepath.Join(root, "selected.txt"), []byte("not deleted by IPC tests"), 0600); err != nil {
		t.Fatal(err)
	}
	exe, err := currentExecutable()
	if err != nil {
		t.Fatal(err)
	}
	exe = canonicalTestPath(t, exe)
	identity, err := currentIdentity()
	if err != nil {
		t.Fatal(err)
	}
	host, err := startHost(root, exe, identity, callback, newMemoryMenuStore(), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := host.Close(); err != nil {
			t.Errorf("关闭 isolated host: %v", err)
		}
	})
	return host, filepath.Join(root, "selected.txt")
}

func TestPipeForwardsToOwnedRunningHostAndRejectsForeignRoot(t *testing.T) {
	var calls atomic.Int32
	var gotMu sync.Mutex
	var got []string
	host, path := isolatedHost(t, func(paths []string) error { calls.Add(1); gotMu.Lock(); got = paths; gotMu.Unlock(); return nil })
	if err := forwardTo(host.cfg, host.identity, []string{path}, time.Now().Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	gotMu.Lock()
	actual := append([]string(nil), got...)
	gotMu.Unlock()
	if calls.Load() != 1 || !reflect.DeepEqual(actual, []string{path}) {
		t.Fatalf("转发次数/路径 %d/%q", calls.Load(), actual)
	}
	outside := filepath.Join(canonicalTestDirectory(t), "outside.txt")
	if err := os.WriteFile(outside, []byte("must remain"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := forwardTo(host.cfg, host.identity, []string{outside}, time.Now().Add(time.Second)); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("云根外未拒绝: %v", err)
	}
	bad := deleteRequest{Version: protocolVersion, Root: host.cfg.Root + "-other", Session: host.cfg.Session, Paths: []string{path}, Deadline: time.Now().Add(time.Second).UnixMilli()}
	if err := host.dispatch(bad); err == nil {
		t.Fatal("host 未独立校验 root")
	}
	bad.Root = host.cfg.Root
	bad.Session = "stale"
	if err := host.dispatch(bad); err == nil {
		t.Fatal("host 未拒绝过期会话")
	}
	if calls.Load() != 1 {
		t.Fatal("未授权请求进入 callback")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("IPC测试不应删除任何文件: %v", err)
	}
}

func TestPipePropagatesCallbackErrorWithoutRetry(t *testing.T) {
	var calls atomic.Int32
	host, path := isolatedHost(t, func([]string) error { calls.Add(1); return fmt.Errorf("hydrate sentinel") })
	err := forwardTo(host.cfg, host.identity, []string{path}, time.Now().Add(3*time.Second))
	if err == nil || !strings.Contains(err.Error(), "hydrate sentinel") || calls.Load() != 1 {
		t.Fatalf("callback error/retry: %v/%d", err, calls.Load())
	}
}

func TestPipeTimeoutAndCloseDoNotLeaveMenuLauncherWaiting(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	completed := make(chan struct{})
	host, path := isolatedHost(t, func([]string) error { close(entered); <-release; close(completed); return nil })
	defer func() {
		close(release)
		select {
		case <-entered:
			select {
			case <-completed:
			case <-time.After(2 * time.Second):
				t.Error("fixture callback 清理超时")
			}
		default: // 鉴权失败时 callback 从未进入，不等待不存在的完成通知。
		}
	}()
	started := time.Now()
	err := forwardTo(host.cfg, host.identity, []string{path}, time.Now().Add(300*time.Millisecond))
	launcherElapsed := time.Since(started)
	if err == nil || launcherElapsed > 2*time.Second {
		t.Fatalf("launcher 无有界等待: %v/%s", err, launcherElapsed)
	}
	t.Logf("300ms deadline 的菜单等待实际耗时: %s", launcherElapsed)
	select {
	case <-entered:
	default:
		t.Fatal("请求未进入宿主")
	}
	request := deleteRequest{Version: protocolVersion, Root: host.cfg.Root, Session: host.cfg.Session, Paths: []string{path}, Deadline: time.Now().Add(time.Second).UnixMilli()}
	if err := host.dispatch(request); err == nil || !strings.Contains(err.Error(), "正在处理") {
		t.Fatalf("超时不能重复开始删除: %v", err)
	}
	started = time.Now()
	if err := host.Close(); err != nil {
		t.Fatal(err)
	}
	closeElapsed := time.Since(started)
	if closeElapsed > 2*time.Second {
		t.Fatal("Close 不能等不可中断的 hydration/native UI")
	}
	t.Logf("阻塞 callback 尚未完成时 Close 实际耗时: %s", closeElapsed)
}

func TestPipeDuplicateHostAndWrongExecutableRejected(t *testing.T) {
	host, path := isolatedHost(t, func([]string) error { return nil })
	if duplicate, err := startHost(host.cfg.Root, host.cfg.Executable, host.identity, func([]string) error { return nil }, newMemoryMenuStore(), false); err == nil {
		_ = duplicate.Close()
		t.Fatal("不得启动第二套 host")
	}
	handle, err := openPipe(pipeName(host.identity, host.cfg.Root, host.cfg.Executable), time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestHandle(t, handle)
	if err := verifyPipePeer(handle, true, host.identity, host.cfg.Executable+".other"); err == nil {
		t.Fatal("未验证对端程序")
	}
	// 对端鉴权测试本身并未提交删除；随后正常完成一次请求/响应/ACK，避免制造无关断连。
	stream := &pipeStream{handle: handle, deadline: time.Now().Add(time.Second)}
	request := deleteRequest{Version: protocolVersion, Root: host.cfg.Root, Session: host.cfg.Session, Paths: []string{path}, Deadline: stream.deadline.UnixMilli()}
	if err := writeFrame(stream, request); err != nil {
		t.Fatal(err)
	}
	var response deleteResponse
	if err := readFrame(stream, &response); err != nil || response.Version != protocolVersion || response.Error != "" {
		t.Fatalf("鉴权后完成响应: %+v / %v", response, err)
	}
	if err := writeFrame(stream, deleteAck{Version: protocolVersion, Session: host.cfg.Session}); err != nil {
		t.Fatal(err)
	}
}

func closeTestHandle(t *testing.T, handle windows.Handle) {
	t.Helper()
	if err := windows.CloseHandle(handle); err != nil {
		t.Error(err)
	}
}
