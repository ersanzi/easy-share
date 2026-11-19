//go:build windows

package shellstatus

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestShellStatusExistingIsolatedProvider(t *testing.T) {
	root := os.Getenv("EASYSHARE_SHELL_STATUS_FIXTURE")
	if root == "" {
		t.Skip("需要专用已登记隔离provider根")
	}
	if !filepath.IsAbs(root) || filepath.Base(root) != "root" {
		t.Fatal("隔离根不可信")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_MULTITHREADED); err != nil {
		t.Fatal(err)
	}
	defer windows.CoUninitialize()
	path := filepath.Join(root, "界面测试.txt")
	for _, phase := range []Phase{PhaseUploading, PhaseVerifying, PhaseFailed, PhaseSynced} {
		if err := Apply(Update{Path: path, Phase: phase, Total: 18, Completed: 18}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		value, err := ReadTransferFlags(path)
		// 系统会叠加NEEDSUPLOAD/INCOMPLETE等原生占位位；只核对提供方活动位。
		const activityMask = uint32(0x4 | 0x10 | 0x20 | 0x80)
		if err != nil || value&activityMask != TransferFlags(phase)&activityMask {
			t.Fatalf("registered cloud provider phase=%s got=%x err=%v", phase, value, err)
		}
	}
}
