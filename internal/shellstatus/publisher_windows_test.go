//go:build windows

package shellstatus

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"easyshare/internal/cfapi"
	"easyshare/internal/shellroot"
	"golang.org/x/sys/windows"
)

func TestShellStatusFlagsWaitForVerification(t *testing.T) {
	if TransferFlags(PhaseVerifying)&0x4 == 0 || TransferFlags(PhaseSynced) != 0 {
		t.Fatal("确认阶段不能提前清除活动状态")
	}
	if TransferFlags(PhaseFailed)&0x10 == 0 || TransferFlags(PhaseConflict)&0x10 == 0 || TransferFlags(PhaseUnknown) == 0 {
		t.Fatal("失败/未知状态不能伪装正常同步")
	}
}

func TestShellStatusScope(t *testing.T) {
	root := `C:\test\root`
	for _, path := range []string{root, root + `\file`, root + `\folder\file`} {
		if !within(root, path) {
			t.Fatalf("拒绝合法范围: %s", path)
		}
	}
	for _, path := range []string{`C:\test\root-other\file`, root + `\..\file`, `D:\test\root\file`, `relative\file`} {
		if within(root, path) {
			t.Fatalf("允许越界范围: %s", path)
		}
	}
}

func TestShellStatusPropertyLayout(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("当前只验证64位Shell布局")
	}
	if unsafe.Sizeof(propertyKey{}) != 20 || unsafe.Sizeof(propertyVariant{}) != 24 || unsafe.Offsetof(propertyVariant{}.Data) != 16 {
		t.Fatal("Shell PROPERTYKEY/PROPVARIANT布局不符")
	}
	if keyTransferStatus.ID != 103 || keyTransferProgress.ID != 4 {
		t.Fatal("同步属性PID不符")
	}
}

func TestShellStatusVolatileRealRoundTrip(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_MULTITHREADED); err != nil {
		t.Fatal(err)
	}
	defer windows.CoUninitialize()
	path := registeredStatusFixture(t)
	for _, phase := range []Phase{PhaseUploading, PhaseVerifying, PhaseFailed, PhaseSynced} {
		if err := Apply(Update{Path: path, Phase: phase, Total: 9, Completed: 9}); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(5 * time.Second)
		var got uint32
		var readErr error
		for {
			got, readErr = ReadTransferFlags(path)
			want := TransferFlags(phase)
			const controlledMask = uint32(0x4 | 0x10 | 0x20 | 0x80)
			matched := got&controlledMask == want&controlledMask
			if phase == PhaseFailed || phase == PhaseConflict {
				matched = got&0x10 != 0 && got&0x20 == 0
			}
			if phase == PhaseSynced {
				matched = got&(0x10|0x20) == 0
			}
			if readErr == nil && matched {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("volatile状态未回读: phase=%s got=%x err=%v", phase, got, readErr)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != "unchanged" {
		t.Fatal("Shell属性发布改了文件正文")
	}
}

func TestShellStatusPublisherThreadAndScope(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "state.txt")
	if err := os.WriteFile(path, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	publisher := NewPublisher(context.Background(), root, nil)
	publisher.Publish(Update{Path: filepath.Join(root, "..", "forbidden.txt"), Phase: PhaseFailed})
	publisher.Publish(Update{Path: path, Phase: PhaseUploading, Total: 9, Completed: 9})
	deadline := time.Now().Add(3 * time.Second)
	for {
		publisher.mu.Lock()
		_, published := publisher.last[path]
		publisher.mu.Unlock()
		if published {
			break
		}
		if time.Now().After(deadline) {
			publisher.Close()
			n, err := publisher.Failures()
			t.Fatalf("线程未发布: failures=%d err=%v", n, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	publisher.Close()
	if n, err := publisher.Failures(); n != 0 {
		t.Fatalf("COM属性发布错误: %d %v", n, err)
	}
}

// 微型真实CFAPI提供方，普通临时文件不具有云文件活动属性的回读契约。
func registeredStatusFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	sid, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	identity := fmt.Sprintf("shellstatus-%d", time.Now().UnixNano())
	id := "EasyShareStatusTest!" + sid.User.Sid.String() + "!" + identity
	if err := shellroot.Register(root, id, "状态测试", "%SystemRoot%\\system32\\imageres.dll,-1040", "1.0", []byte(identity)); err != nil {
		t.Fatal(err)
	}
	provider, err := windows.GenerateGUID()
	if err != nil {
		t.Fatal(err)
	}
	m, err := cfapi.MountSpace(cfapi.MountOptions{RootPath: root, Provider: cfapi.ProviderInfo{Name: "EasyShareStatusTest", Version: "1.0", ID: provider}, SyncRootIdentity: []byte(identity), List: func(context.Context) ([]cfapi.CloudEntry, error) {
		return []cfapi.CloudEntry{{Path: "state.txt", Size: 9, Identity: cfapi.EncodeIdentity(99), Version: 1}}, nil
	}, Open: func(context.Context, string, int64, int64) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("unchanged")), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = m.Close()
		if err := shellroot.UnregisterIsolated(id); err != nil {
			t.Error(err)
		}
	})
	return filepath.Join(root, "state.txt")
}
