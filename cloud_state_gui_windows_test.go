//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"easyshare/internal/cfapi"
	"easyshare/internal/explorerview"
	"easyshare/internal/namespace"
	"easyshare/internal/shellroot"
	"easyshare/internal/shellstatus"
	"golang.org/x/sys/windows"
)

// 真实云占位元数据/原生状态列；云端内容用隔离fixture，真实服务器另有独立GUI闭环。
func TestCloudStateExplorerIsolatedGUI(t *testing.T) {
	if os.Getenv("EASYSHARE_EXPLORER_GUI") != "1" {
		t.Skip("真实云状态GUI需要显式启用")
	}
	wd, _ := os.Getwd()
	run, err := os.MkdirTemp(filepath.Join(wd, ".rd-test"), "cloud-state-gui-")
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(run, "root")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sid, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	identity := fmt.Sprintf("cloud-state-%d", time.Now().UnixNano())
	id := "EasyShareStateTest!" + sid.User.Sid.String() + "!" + identity
	if err := shellroot.Register(root, id, "状态验收", "%SystemRoot%\\system32\\imageres.dll,-1040", "1.0", []byte(identity)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := shellroot.UnregisterIsolated(id); err != nil {
			t.Error(err)
		}
	})
	if err := shellroot.SuppressGeneratedNavigation(id, root); err != nil {
		t.Fatal(err)
	}
	provider, err := windows.GenerateGUID()
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("only-isolated-cloud-"), 4096)
	names := []string{"仅云端.txt", "本机可用.txt", "同步中.txt", "失败.txt"}
	mount, err := cfapi.MountSpace(cfapi.MountOptions{RootPath: root, Provider: cfapi.ProviderInfo{Name: "EasyShareStateTest", Version: "1.0", ID: provider}, SyncRootIdentity: []byte(identity), List: func(context.Context) ([]cfapi.CloudEntry, error) {
		out := []cfapi.CloudEntry{}
		for i, n := range names {
			out = append(out, cfapi.CloudEntry{Path: n, Size: int64(len(payload)), Identity: cfapi.EncodeIdentity(int64(i + 1)), Version: 1})
		}
		return out, nil
	}, Open: func(ctx context.Context, p string, offset, length int64) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(payload[offset : offset+length])), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mount.Close() })
	if _, err := os.ReadFile(filepath.Join(root, names[1])); err != nil {
		t.Fatal(err)
	}
	// 同步/失败对应真实本地未提交修改；仅云端INSYNC文件不能冒充上传中的脏文件。
	modified := bytes.Clone(payload)
	modified[0] ^= 0x20
	for _, name := range names[2:] {
		if err := os.WriteFile(filepath.Join(root, name), modified, 0600); err != nil {
			t.Fatal(err)
		}
		state, err := mount.QueryPlaceholderStatus(name)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("真实未提交文件 %s inSync=%d modified=%d full=%v", name, state.InSyncState, state.Modified, state.FullContent)
	}
	guid, err := windows.GenerateGUID()
	if err != nil {
		t.Fatal(err)
	}
	entry := namespace.SpaceEntries("", []namespace.SpaceMount{{Kind: "personal", TargetPath: root, UserID: "fixture", DisplayName: "状态验收"}})[0]
	entry.CLSID = guid.String()
	if err := namespace.Register([]namespace.Entry{entry}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = namespace.Unregister([]namespace.Entry{entry}) })
	watcher := explorerview.Start(ctx, explorerview.Options{Root: root, StateFile: filepath.Join(run, "view.json"), Logf: t.Logf})
	t.Cleanup(func() { watcher.Close() })
	publisher := shellstatus.NewPublisher(ctx, root, t.Logf)
	t.Cleanup(func() { publisher.Close() })
	publisher.Publish(shellstatus.Update{Path: filepath.Join(root, names[2]), Phase: shellstatus.PhaseUploading, Total: int64(len(payload)), Completed: int64(len(payload)) / 2})
	publisher.Publish(shellstatus.Update{Path: filepath.Join(root, names[3]), Phase: shellstatus.PhaseFailed})
	data, _ := json.Marshal(map[string]any{"root": root, "run": run, "clsid": entry.CLSID, "label": entry.Name, "requireStatus": true})
	manifest := filepath.Join(run, "manifest.json")
	_ = os.WriteFile(manifest, data, 0600)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-STA", "-ExecutionPolicy", "Bypass", "-File", filepath.Join(wd, "tools", "gui", "explorer-owned-actions.ps1"), "-Manifest", manifest)
	output, err := cmd.CombinedOutput()
	_ = os.WriteFile(filepath.Join(run, "gui.log"), output, 0600)
	if err != nil || !strings.Contains(string(output), "EXPLORER_GUI_PASS") {
		t.Fatalf("cloud-state-gui: %s err=%v output=%s", run, err, output)
	}
	t.Logf("CLOUD_STATE_GUI_PASS: %s", run)
}
