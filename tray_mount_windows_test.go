//go:build windows

package main

import (
	"context"
	"testing"

	"easyshare/internal/account"
	"easyshare/internal/cfapi"
	"easyshare/internal/drive"
)

func TestHoverDropStatusQueuesWithoutCallingWebView(t *testing.T) {
	popup := &hoverPopup{}
	done := make(chan struct{})
	go func() {
		popup.SetDropStatus("正在上传…", "1 项 → 网盘", "busy")
		close(done)
	}()
	<-done
	popup.dropStatusMu.Lock()
	got := popup.dropStatus
	popup.dropStatusMu.Unlock()
	if got != [3]string{"正在上传…", "1 项 → 网盘", "busy"} {
		t.Fatalf("工作线程的状态未入队：%v", got)
	}
	// 尚无 WebView2 时窗口线程消费应安全跳过，不能反过来让工作线程访问 COM。
	popup.setDropStatusOnOwnThread()
}

func TestHoverSpacesRequireActualMount(t *testing.T) {
	spaces := []account.Space{{SpaceType: account.SpacePersonal, QuotaBytes: 1024}, {SpaceType: account.SpaceShared, QuotaBytes: 1024, Permission: account.PermWrite}}
	before := projectHoverSpaces(spaces, false)
	if len(before) != 2 || before[0].Unavailable == "" || before[1].Unavailable == "" {
		t.Fatalf("unmounted spaces advertised: %+v", before)
	}
	after := projectHoverSpaces(spaces, true)
	if after[0].Unavailable != "" || after[1].Unavailable == "" {
		t.Fatalf("mount state not reflected: %+v", after)
	}
	if got := projectHoverSpaces([]account.Space{{SpaceType: account.SpacePersonal, QuotaBytes: account.QuotaUnset}}, true); len(got) != 0 {
		t.Fatal("unallocated space shown")
	}
}

func TestNativeDropRejectsWrongMountGeneration(t *testing.T) {
	a := newSpaceTestApp()
	first := &account.Session{Token: "one"}
	second := &account.Session{Token: "two"}
	a.accountSession = first
	a.cfapi.personal = &cfapi.Mount{}
	a.cfapi.session = first
	a.cfapi.lifetime = newNativeMountLifetime()
	defer a.cfapi.lifetime.cancel()
	if err := a.nativeDropAvailable(drive.SpacePersonal); err != nil {
		t.Fatal(err)
	}
	a.accountSession = second
	if err := a.nativeDropAvailable(drive.SpacePersonal); err == nil {
		t.Fatal("stale mount accepted new account upload")
	}
	a.accountSession = first
	a.cfapi.lifetime.cancel()
	if err := a.nativeDropAvailable(drive.SpacePersonal); err == nil {
		t.Fatal("canceled mount accepted")
	}
	if err := a.nativeDropAvailable(drive.SpaceShared); err == nil {
		t.Fatal("unsupported shared mount advertised")
	}
}

func TestExplicitCacheActionRejectsPathBeforeCallback(t *testing.T) {
	a := newSpaceTestApp()
	a.cfapi.personal = &cfapi.Mount{}
	a.cfapi.lifetime = newNativeMountLifetime()
	defer a.cfapi.lifetime.cancel()
	calls := 0
	a.cfapi.cacheApply = func(context.Context, *cfapi.Mount, string, bool) error { calls++; return nil }
	for _, key := range []string{"", ".", "..", "../outside", "a/../b", `a\b`, "file:stream", "C:/outside"} {
		if err := a.CloudFreeUpSpace(key); err == nil {
			t.Fatalf("unsafe path allowed: %q", key)
		}
	}
	if calls != 0 {
		t.Fatal("unsafe path reached cache callback")
	}
	if err := a.CloudFreeUpSpace("doc.txt"); err != nil || calls != 1 {
		t.Fatalf("safe path: %v calls=%d", err, calls)
	}
}

func TestInvalidDropSelectionPreservesPriorSpace(t *testing.T) {
	a := newSpaceTestApp()
	a.SetDropSpace(drive.SpacePersonal)
	a.SetDropSpace("unexpected")
	if a.targetSpace() != drive.SpacePersonal {
		t.Fatal("invalid switch changed upload destination")
	}
}

func TestPersistentDirectoryMatchesFrontendFolderView(t *testing.T) {
	file := driveObjectToFile(drive.Object{Path: "empty", Directory: true, FileId: 17, VersionId: 1})
	if file.Key != "empty/" || file.Name != "empty" || file.FileId != 17 {
		t.Fatalf("persistent directory misrepresented: %+v", file)
	}
}
