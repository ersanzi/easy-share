//go:build windows

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"easyshare/internal/winui"
)

// 只在专用进程环境中启用；创建一只测试宠物，不启动App/Core、不改生产入口。
func TestDesktopPetIsolatedGUI(t *testing.T) {
	if os.Getenv("EASYSHARE_PET_GUI") != "1" {
		t.Skip("真实宠物鼠标验收须显式启用隔离环境")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(wd, ".rd-test", "pet-slide-gui")
	if !strings.EqualFold(filepath.Clean(os.Getenv("LOCALAPPDATA")), filepath.Clean(filepath.Join(local, "user"))) {
		t.Fatal("验收必须使用本任务独立LOCALAPPDATA，不共享用户WebView/位置配置")
	}
	if err := os.MkdirAll(local, 0700); err != nil {
		t.Fatal(err)
	}
	popup, err := newHoverPopup(96, false, func() {}, func(string) {}, func([]string, string) {}, func() {}, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		popup.Destroy()
		select {
		case <-popup.done:
		case <-time.After(5 * time.Second):
			t.Error("隔离宠物退出未完成")
		}
	})
	popup.SetUser(true, "宠物测试", "fixture")
	popup.SetSpaces([]hoverSpace{{Kind: "personal", Label: "网盘", Quota: "隔离界面验收，不传输用户文件"}})
	time.Sleep(700 * time.Millisecond)
	manifest := map[string]any{"pid": os.Getpid(), "pet": popup.pet.hwnd, "panel": popup.hwnd, "root": local, "localAppData": os.Getenv("LOCALAPPDATA"), "webviewData": filepath.Join(local, "user", "EasyShare", "webview2-widget"), "screenshotScope": "owned pet/panel rect, outside native region transparent", "serverTested": false, "productionSwitched": false}
	data, _ := json.Marshal(manifest)
	path := filepath.Join(local, "manifest.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("powershell.exe", "-NoProfile", "-STA", "-ExecutionPolicy", "Bypass", "-File", filepath.Join(wd, "tools", "gui", "pet-isolated-actions.ps1"), "-Manifest", path)
	output, err := command.CombinedOutput()
	if err := os.WriteFile(filepath.Join(local, "gui.log"), output, 0600); err != nil {
		t.Fatal(err)
	}
	if err != nil || !strings.Contains(string(output), "PET_GUI_PASS") {
		t.Fatalf("真实宠物GUI失败: %v %s", err, output)
	}
	if !winui.GetWindowRect(popup.pet.hwnd).Contains(winui.GetWindowRect(popup.pet.hwnd).Left+1, winui.GetWindowRect(popup.pet.hwnd).Top+1) {
		t.Fatal("宿主位置不可信")
	}
	popup.Destroy()
	select {
	case <-popup.done:
	case <-time.After(5 * time.Second):
		t.Fatal("隔离宠物退出未完成")
	}
	t.Log("PET_GUI_PASS: 实际悬停、跨面板、离开收起、双击固定、固定后拖动及退出；未冒称服务器上传")
}
