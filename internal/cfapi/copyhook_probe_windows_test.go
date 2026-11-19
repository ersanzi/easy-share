//go:build windows

package cfapi

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"easyshare/internal/winui"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// 仅为新建隔离根验证官方Shell CopyHook时序，不登记正式产品CLSID。
type undoCopyProbe struct {
	table     *[5]uintptr
	refs      atomic.Int32
	root, run string
	content   map[string][]byte
	mu        sync.Mutex
	calls     []map[string]any
}

type undoCopyFactory struct {
	table *[5]uintptr
	refs  atomic.Int32
	hook  *undoCopyProbe
}

var (
	undoIIDUnknown = windows.GUID{Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
	undoIIDFactory = windows.GUID{Data1: 1, Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
	undoIIDHook    = windows.GUID{Data1: 0x7bf992a9, Data2: 0xaf7a, Data3: 0x4dba, Data4: [8]byte{0xb2, 0xe5, 0x4d, 0x08, 0x0b, 0x1e, 0xcb, 0xc6}}
)

func undoPtr[T any](value uintptr) *T {
	return (*T)(*(*unsafe.Pointer)(unsafe.Pointer(&value)))
}

func undoHookQuery(this, iid, result uintptr) uintptr {
	hook := undoPtr[undoCopyProbe](this)
	*undoPtr[uintptr](result) = 0
	if *undoPtr[windows.GUID](iid) != undoIIDUnknown && *undoPtr[windows.GUID](iid) != undoIIDHook {
		return 0x80004002
	}
	*undoPtr[uintptr](result) = this
	hook.refs.Add(1)
	return 0
}
func undoHookAdd(this uintptr) uintptr     { return uintptr(undoPtr[undoCopyProbe](this).refs.Add(1)) }
func undoHookRelease(this uintptr) uintptr { return uintptr(undoPtr[undoCopyProbe](this).refs.Add(-1)) }
func undoFactoryQuery(this, iid, result uintptr) uintptr {
	factory := undoPtr[undoCopyFactory](this)
	*undoPtr[uintptr](result) = 0
	if *undoPtr[windows.GUID](iid) != undoIIDUnknown && *undoPtr[windows.GUID](iid) != undoIIDFactory {
		return 0x80004002
	}
	*undoPtr[uintptr](result) = this
	factory.refs.Add(1)
	return 0
}
func undoFactoryAdd(this uintptr) uintptr { return uintptr(undoPtr[undoCopyFactory](this).refs.Add(1)) }
func undoFactoryRelease(this uintptr) uintptr {
	return uintptr(undoPtr[undoCopyFactory](this).refs.Add(-1))
}
func undoFactoryCreate(this, outer, iid, result uintptr) uintptr {
	if outer != 0 {
		return 0x80040110
	}
	factory := undoPtr[undoCopyFactory](this)
	return undoHookQuery(uintptr(unsafe.Pointer(factory.hook)), iid, result)
}
func undoFactoryLock(this, lock uintptr) uintptr { return 0 }

func undoCopyCallback(this, hwnd, operation, flags, source, attrs, destination, destAttrs, result uintptr) (hr uintptr) {
	*undoPtr[uint32](result) = 6 // IDYES
	defer func() {
		if recover() != nil {
			*undoPtr[uint32](result) = 2
			hr = 0x80004005
		}
	}()
	hook := undoPtr[undoCopyProbe](this)
	path := utf16ToString(undoPtr[uint16](source))
	rel, inRoot := RelativeOperationPath(hook.root, path)
	call := map[string]any{"operation": operation, "flags": flags, "source": path, "relative": rel, "attrs": attrs, "at": time.Now()}
	if destination != 0 {
		call["destination"] = utf16ToString(undoPtr[uint16](destination))
	}
	// 只在用户选择删除本轮fixture时验证回收准备，复制/移动等操作不改动。
	if operation == 3 && inRoot && os.Getenv("EASYSHARE_UNDO_PROBE_COPYHOOK_HYDRATE") == "1" {
		for p, expected := range hook.content {
			if p != rel && !(attrs&windows.FILE_ATTRIBUTE_DIRECTORY != 0 && strings.HasPrefix(p, rel+"/")) {
				continue
			}
			actual, err := os.ReadFile(filepath.Join(hook.root, filepath.FromSlash(p)))
			if err != nil || string(actual) != string(expected) {
				call["prepareError"] = fmt.Sprintf("%s: %v", p, err)
				*undoPtr[uint32](result) = 2
				break
			}
		}
	}
	hook.mu.Lock()
	hook.calls = append(hook.calls, call)
	b, _ := json.MarshalIndent(hook.calls, "", "  ")
	_ = os.WriteFile(filepath.Join(hook.run, "copyhook-events.json"), b, 0600)
	hook.mu.Unlock()
	return 0
}

func startUndoCopyHookProbe(t *testing.T, root, shellID, run string, content map[string][]byte) func() {
	t.Helper()
	clsid, err := windows.GenerateGUID()
	if err != nil {
		t.Fatal(err)
	}
	classKey := `Software\Classes\CLSID\` + clsid.String()
	key, exists, err := registry.CreateKey(registry.CURRENT_USER, classKey+`\LocalServer32`, registry.SET_VALUE)
	if err != nil || exists {
		t.Fatalf("隔离COM登记失败:%v", err)
	}
	// 当前进程注册Running class；该入口不允许启动产品或另一个测试进程。
	if err := key.SetStringValue("", `"`+os.Args[0]+`" -test.run=^$`); err != nil {
		key.Close()
		t.Fatal(err)
	}
	key.Close()
	rootKey, err := registry.OpenKey(registry.LOCAL_MACHINE, `Software\Microsoft\Windows\CurrentVersion\Explorer\SyncRootManager\`+shellID, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := rootKey.GetStringValue("CopyHook"); err == nil {
		rootKey.Close()
		t.Fatal("隔离根已存在CopyHook，禁止覆盖")
	}
	if err := rootKey.SetStringValue("CopyHook", clsid.String()); err != nil {
		rootKey.Close()
		t.Fatal(err)
	}
	hook := &undoCopyProbe{root: root, run: run, content: content}
	hook.refs.Store(1)
	hook.table = &[5]uintptr{syscall.NewCallback(undoHookQuery), syscall.NewCallback(undoHookAdd), syscall.NewCallback(undoHookRelease), syscall.NewCallback(undoCopyCallback)}
	factory := &undoCopyFactory{hook: hook}
	factory.refs.Store(1)
	factory.table = &[5]uintptr{syscall.NewCallback(undoFactoryQuery), syscall.NewCallback(undoFactoryAdd), syscall.NewCallback(undoFactoryRelease), syscall.NewCallback(undoFactoryCreate), syscall.NewCallback(undoFactoryLock)}
	ready := make(chan error, 1)
	done := make(chan struct{})
	var threadID uint32
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(done)
		threadID = windows.GetCurrentThreadId()
		ole := windows.NewLazySystemDLL("ole32.dll")
		hr, _, _ := ole.NewProc("CoInitializeEx").Call(0, 2)
		if int32(hr) < 0 {
			ready <- fmt.Errorf("COM STA初始化:0x%08x", uint32(hr))
			return
		}
		defer ole.NewProc("CoUninitialize").Call()
		var cookie uint32
		hr, _, _ = ole.NewProc("CoRegisterClassObject").Call(uintptr(unsafe.Pointer(&clsid)), uintptr(unsafe.Pointer(factory)), 4, 1, uintptr(unsafe.Pointer(&cookie)))
		if int32(hr) < 0 {
			ready <- fmt.Errorf("COM本地类注册:0x%08x", uint32(hr))
			return
		}
		defer ole.NewProc("CoRevokeClassObject").Call(uintptr(cookie))
		ready <- nil
		winui.RunMessageLoop()
		runtime.KeepAlive(factory)
	}()
	if err := <-ready; err != nil {
		t.Fatal(err)
	}
	return func() {
		post := windows.NewLazySystemDLL("user32.dll").NewProc("PostThreadMessageW")
		post.Call(uintptr(threadID), 0x12, 0, 0)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("隔离COM退出超时")
		}
		if err := rootKey.DeleteValue("CopyHook"); err != nil {
			t.Errorf("撤掉隔离CopyHook:%v", err)
		}
		rootKey.Close()
		if err := registry.DeleteKey(registry.CURRENT_USER, classKey+`\LocalServer32`); err != nil {
			t.Errorf("清理隔离COM子键:%v", err)
		}
		if err := registry.DeleteKey(registry.CURRENT_USER, classKey); err != nil {
			t.Errorf("清理隔离COM键:%v", err)
		}
	}
}
