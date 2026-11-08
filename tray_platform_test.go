package main

import (
	"go/build"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDarwinTrayDoesNotImportSystray(t *testing.T) {
	t.Parallel()

	context := build.Default
	context.GOOS = "darwin"
	context.GOARCH = "arm64"
	context.CgoEnabled = true

	pkg, err := context.ImportDir(testPackageDir(t), build.IgnoreVendor)
	if err != nil {
		t.Fatalf("读取 Darwin 构建文件: %v", err)
	}
	for _, importPath := range pkg.Imports {
		if importPath == "github.com/getlantern/systray" {
			t.Fatal("Darwin 构建不能导入 getlantern/systray：它会与 Wails 重复定义 AppDelegate")
		}
	}
}

func TestDarwinTrayBridgeDoesNotOwnApplicationDelegate(t *testing.T) {
	t.Parallel()

	sourcePath := filepath.Join(testPackageDir(t), "tray_native_darwin.m")
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("读取 macOS 托盘 bridge: %v", err)
	}

	// Wails 必须是 NSApplicationDelegate 和 AppKit 事件循环的唯一所有者。
	forbidden := []string{
		"@interface AppDelegate",
		"@implementation AppDelegate",
		"setDelegate:",
		"[NSApp run]",
	}
	for _, token := range forbidden {
		if strings.Contains(string(source), token) {
			t.Fatalf("macOS 托盘 bridge 包含禁止的应用生命周期操作 %q", token)
		}
	}
}

func TestWindowsHoverKeepsExternalDOMFileDropEnabled(t *testing.T) {
	source, err := os.ReadFile(filepath.Join(testPackageDir(t), "tray_hover_windows.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	if !strings.Contains(text, "AllowExternalDrag(true)") || strings.Contains(text, "AllowExternalDrag(false)") {
		t.Fatal("悬浮窗的 DOM 文件拖放通道不能禁用 WebView2 外部 drop")
	}
	if !strings.Contains(text, "MessageWithAdditionalObjectsCallback = p.handleDroppedObjects") {
		t.Fatal("必须通过附加 File 对象取真实路径，不能只依赖 WM_DROPFILES")
	}
}

func testPackageDir(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("无法定位测试文件")
	}
	return filepath.Dir(filename)
}
