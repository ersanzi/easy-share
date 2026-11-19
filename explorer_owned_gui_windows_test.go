//go:build windows

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"easyshare/internal/explorerview"
	"easyshare/internal/namespace"
	"golang.org/x/sys/windows"
)

func TestExplorerOwnedNavigationGUI(t *testing.T) {
	if os.Getenv("EASYSHARE_EXPLORER_GUI") != "1" {
		t.Skip("真实导航鼠标验收需显式启用")
	}
	wd, _ := os.Getwd()
	local := filepath.Join(wd, ".rd-test", "explorer-gui")
	root := filepath.Join(local, "root")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "界面测试.txt"), []byte("isolated-ui-fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	guid, err := windows.GenerateGUID()
	if err != nil {
		t.Fatal(err)
	}
	entry := namespace.SpaceEntries("", []namespace.SpaceMount{{Kind: "personal", TargetPath: root, UserID: "fixture-only", DisplayName: "导航测试"}})[0]
	entry.CLSID = guid.String()
	if err := namespace.Register([]namespace.Entry{entry}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = namespace.Unregister([]namespace.Entry{entry}) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watcher := explorerview.Start(ctx, explorerview.Options{Root: root, StateFile: filepath.Join(local, "view.json"), Logf: t.Logf})
	t.Cleanup(func() { watcher.Close() })
	data, _ := json.Marshal(map[string]any{"root": root, "run": local, "clsid": entry.CLSID, "label": entry.Name})
	manifest := filepath.Join(local, "manifest.json")
	if err := os.WriteFile(manifest, data, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("powershell.exe", "-NoProfile", "-STA", "-ExecutionPolicy", "Bypass", "-File", filepath.Join(wd, "tools", "gui", "explorer-owned-actions.ps1"), "-Manifest", manifest)
	output, err := cmd.CombinedOutput()
	_ = os.WriteFile(filepath.Join(local, "gui.log"), output, 0600)
	if err != nil || !strings.Contains(string(output), "EXPLORER_GUI_PASS") {
		t.Fatalf("真实导航视图验收失败: %v %s", err, output)
	}
	t.Log("EXPLORER_GUI_PASS: 隔离根磁贴双击/同级导航点击/详细列表；服务器闭环另验收")
}
