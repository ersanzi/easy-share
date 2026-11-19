//go:build windows

package cfapi

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"easyshare/internal/shellroot"
	"easyshare/internal/shellstatus"
	"golang.org/x/sys/windows"
)

// 同时使用Shell登记和CFAPI占位文件，不以普通文件的默认0状态冒称回读成功。
func TestRegisteredShellCloudTransferFlags(t *testing.T) {
	if !Available() {
		t.Skip("CFAPI unavailable")
	}
	root := directoryRestoreTempRoot(t)
	sid, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	identity := fmt.Sprintf("registered-shell-%d", time.Now().UnixNano())
	id := "EasyShareTest!" + sid.User.Sid.String() + "!" + identity
	if err := shellroot.Register(root, id, "状态验收", "%SystemRoot%\\system32\\imageres.dll,-1040", "1.0", []byte(identity)); err != nil {
		t.Fatal(err)
	}
	guid, err := windows.GenerateGUID()
	if err != nil {
		t.Fatal(err)
	}
	content := bytes.Repeat([]byte("isolated-cloud-data"), 8192)
	m, err := MountSpace(MountOptions{RootPath: root, Provider: ProviderInfo{Name: "EasyShareTest", Version: "1.0", ID: guid}, SyncRootIdentity: []byte(identity), List: func(context.Context) ([]CloudEntry, error) {
		return []CloudEntry{{Path: "state.bin", Size: int64(len(content)), Identity: EncodeIdentity(99), Version: 1}}, nil
	}, Open: func(ctx context.Context, path string, offset, length int64) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(content[offset : offset+length])), nil
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
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_MULTITHREADED); err != nil {
		t.Fatal(err)
	}
	defer windows.CoUninitialize()
	path := filepath.Join(root, "state.bin")
	for _, phase := range []shellstatus.Phase{shellstatus.PhaseUploading, shellstatus.PhaseVerifying, shellstatus.PhaseFailed, shellstatus.PhaseSynced} {
		if err := shellstatus.Apply(shellstatus.Update{Path: path, Phase: phase, Total: 9, Completed: 9}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(200 * time.Millisecond)
		flags, err := shellstatus.ReadTransferFlags(path)
		t.Logf("phase=%s nativeFlags=%x error=%v", phase, flags, err)
		if err != nil {
			t.Fatal(err)
		}
		if phase == shellstatus.PhaseUploading && flags&4 == 0 {
			t.Fatal("真实传输标志未回读")
		}
		if phase == shellstatus.PhaseVerifying && flags&(4|0x20) == 0 {
			t.Fatal("确认期间被刷为静止")
		}
		if phase == shellstatus.PhaseFailed && flags&0x10 == 0 {
			t.Fatal("真实失败标志未回读")
		}
	}
	state, err := m.QueryPlaceholderStatus("state.bin")
	if err != nil || state.OnDisk != 0 {
		t.Fatalf("属性操作召回正文: %+v %v", state, err)
	}
}
