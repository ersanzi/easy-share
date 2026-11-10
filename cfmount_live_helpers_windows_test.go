//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"easyshare/internal/account"
	"easyshare/internal/cfapi"
	"golang.org/x/sys/windows"
)

// 所有活栈落点在当前检出目录的 .rd-test；不要求机器专属目录名，也不使用生产云根。
func nativeLiveWorkspace(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal("无法确定测试检出目录")
	}
	workspace := filepath.Join(wd, ".rd-test")
	for parent := wd; ; parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			t.Fatal("测试工作区父目录不是普通目录")
		}
		path, err := windows.UTF16PtrFromString(parent)
		if err != nil {
			t.Fatal("测试工作区路径无效")
		}
		attrs, err := windows.GetFileAttributes(path)
		if err != nil || attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			t.Fatal("测试工作区不能经过重解析目录")
		}
		if filepath.Dir(parent) == parent {
			break
		}
	}
	if err := os.MkdirAll(workspace, 0700); err != nil {
		t.Fatal("无法建立隔离测试工作区")
	}
	path, err := windows.UTF16PtrFromString(workspace)
	if err != nil {
		t.Fatal("隔离测试路径无效")
	}
	attrs, err := windows.GetFileAttributes(path)
	if err != nil || attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		t.Fatal("隔离测试工作区不能是重解析目录")
	}
	if _, registered, err := cfapi.QuerySyncRootStandardInfo(workspace); err != nil || registered {
		t.Fatal("隔离测试工作区不能位于已注册的云同步根中")
	}
	return workspace
}

// 可由安全运行器注入当前测试会话，避免反复登录触发服务器并发会话限制；不从AppData取令牌。
func nativeLiveSession(ctx context.Context, base string) (*account.Session, error) {
	if strings.TrimSpace(base) == "" {
		return nil, fmt.Errorf("缺少 EASYSHARE_LIVE_BASE")
	}
	client := account.New(base)
	if token := os.Getenv("EASYSHARE_LIVE_TOKEN"); token != "" {
		return client.SessionInfo(ctx, token)
	}
	user, pass := os.Getenv("EASYSHARE_LIVE_USER_A"), os.Getenv("EASYSHARE_LIVE_PASS_A")
	if user == "" || pass == "" {
		return nil, fmt.Errorf("需要显式测试会话或测试账号环境变量")
	}
	return client.Login(ctx, user, pass)
}

func nativeLiveGUIRoot(workspace, root string) bool {
	root = filepath.Clean(root)
	parent := filepath.Dir(root)
	return filepath.IsAbs(root) && filepath.Base(root) == "root" &&
		strings.EqualFold(filepath.Dir(parent), filepath.Clean(workspace)) &&
		strings.HasPrefix(filepath.Base(parent), "live-cache-gui-") &&
		len(filepath.Base(parent)) > len("live-cache-gui-")
}

func TestNativeLiveGUIRootRejectsProductionAndNestedPaths(t *testing.T) {
	workspace := `C:\checkout\.rd-test`
	if !nativeLiveGUIRoot(workspace, `C:\checkout\.rd-test\live-cache-gui-123\root`) {
		t.Fatal("隔离测试根被拒绝")
	}
	for _, root := range []string{
		`C:\checkout\.rd-test-other\live-cache-gui-123\root`,
		`C:\checkout\.rd-test\live-cache-gui-\root`,
		`C:\checkout\.rd-test\live-cache-gui-123\nested\root`,
		`C:\user-data\cloud-drive\root`,
		`live-cache-gui-123\root`,
	} {
		if nativeLiveGUIRoot(workspace, root) {
			t.Fatalf("不安全测试根被接受: %q", root)
		}
	}
}
