//go:build windows

package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"easyshare/internal/account"
	"easyshare/internal/cfapi"
	"easyshare/internal/drive"
	"easyshare/internal/namespace"
)

// 实际服务器A→B→A账号绑定；测试进程专用状态，正式客户端须由父运行器正常退出。
func TestLiveAccountNicknameBinding(t *testing.T) {
	if os.Getenv("EASYSHARE_ACCOUNT_BINDING") != "1" {
		t.Skip("账号绑定活栈需显式专用环境")
	}
	base := os.Getenv("EASYSHARE_LIVE_BASE")
	nameA := os.Getenv("EASYSHARE_ACCOUNT_A")
	nameB := os.Getenv("EASYSHARE_ACCOUNT_B")
	passA := os.Getenv("EASYSHARE_ACCOUNT_PASS_A")
	passB := os.Getenv("EASYSHARE_ACCOUNT_PASS_B")
	if base == "" || nameA == "" || nameB == "" || passA == "" || passB == "" {
		t.Fatal("缺少显式专用账号环境")
	}
	work, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(work, ".rd-test", "account-binding", "data")
	if err := os.MkdirAll(data, 0700); err != nil {
		t.Fatal(err)
	}
	a := NewApp()
	a.configPath = filepath.Join(data, "config.json")
	a.config.PlatformBaseURL = base
	a.logger = log.New(io.Discard, "", 0)
	t.Cleanup(func() { a.unmountAllSpaces() })
	login := func(name, password string) *account.Session {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		s, err := account.New(base).Login(ctx, name, password)
		if err != nil {
			t.Fatal("专用账号登录失败（隐藏凭据）")
		}
		a.accountMu.Lock()
		a.accountSession = s
		a.accountRevision++
		a.accountMu.Unlock()
		a.mountCfapiSpaces()
		if a.CfapiMountPath() == "" {
			t.Fatal("专用账号真实空间未挂载")
		}
		return s
	}
	gui := func(root, label string) {
		t.Helper()
		run, err := os.MkdirTemp(filepath.Join(work, ".rd-test", "account-binding"), "gui-")
		if err != nil {
			t.Fatal(err)
		}
		manifest := filepath.Join(run, "manifest.json")
		data, _ := json.Marshal(map[string]any{"root": root, "run": run, "clsid": namespace.PersonalCLSID(), "label": label + " 的网盘", "accountBindingTest": true})
		if err := os.WriteFile(manifest, data, 0600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("powershell.exe", "-NoProfile", "-STA", "-ExecutionPolicy", "Bypass", "-File", filepath.Join(work, "tools", "gui", "explorer-owned-actions.ps1"), "-Manifest", manifest)
		output, err := cmd.CombinedOutput()
		_ = os.WriteFile(filepath.Join(run, "gui.log"), output, 0600)
		if err != nil || !strings.Contains(string(output), "EXPLORER_GUI_PASS") {
			t.Fatalf("账号入口GUI失败: %v %s", err, output)
		}
	}
	first := login(nameA, passA)
	rootA := a.CfapiMountPath()
	identityA := nativeSpaceIdentity(base, first.User.UserID, drive.SpacePersonal)
	gui(rootA, first.User.DisplayName())
	if err := namespace.Register(namespace.SpaceEntries("", []namespace.SpaceMount{{Kind: "personal", TargetPath: rootA, UserID: first.User.UserID, DisplayName: first.User.DisplayName()}})); err != nil {
		t.Fatal(err)
	}
	rootID, err := cfapi.ReadDirectoryNTFSIdentity(rootA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.UpdateNickName("云盘测试甲改名"); err != nil {
		t.Fatal("专用账号改名失败")
	}
	a.refreshNativeSpaceLabel()
	gui(rootA, "云盘测试甲改名")
	if a.CfapiMountPath() != rootA || a.accountSession != first {
		t.Fatal("同账号改名重建空间或会话")
	}
	afterID, err := cfapi.ReadDirectoryNTFSIdentity(rootA)
	if err != nil || afterID != rootID {
		t.Fatal("改名改变根NTFS身份")
	}
	// API确认改名不只客户端假改，稳定UserID不变。
	fresh, err := account.New(base).SessionInfo(context.Background(), first.Token)
	if err != nil || fresh.User.NickName != "云盘测试甲改名" || fresh.User.UserID != first.User.UserID {
		t.Fatal("服务器改名未确认")
	}
	a.unmountCfapiSpaces()
	second := login(nameB, passB)
	rootB := a.CfapiMountPath()
	gui(rootB, second.User.DisplayName())
	if rootB == rootA || second.User.UserID == first.User.UserID || a.boundSessionToken(first, base)() != "" {
		t.Fatal("A/B空间或旧令牌隔离失败")
	}
	a.unmountCfapiSpaces()
	third := login(nameA, passA)
	gui(rootA, third.User.DisplayName())
	if a.CfapiMountPath() != rootA || third.User.UserID != first.User.UserID || third.User.NickName != "云盘测试甲改名" {
		t.Fatal("返回A没有恢复同一空间身份")
	}
	proof := map[string]any{"status": "ACCOUNT_A_B_A_BINDING_PASS", "userA": first.User.UserID, "userB": second.User.UserID, "rootIdentityA": identityA, "sameAccountRenamePreservedRoot": true, "sameAccountRenamePreservedSession": true, "sameAccountRenamePreservedNTFS": true, "serverNickConfirmed": true, "accountSpaceIsolation": true, "oldTokenCallbackRejected": true, "restoredSameRootAfterSwitch": true, "actualGUI": true}
	b, _ := json.MarshalIndent(proof, "", "  ")
	if err := os.WriteFile(filepath.Join(work, ".rd-test", "account-binding", "result.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log("ACCOUNT_A_B_A_BINDING_PASS：真实服务器账号/根，Explorer鼠标另验，非GUI外推")
}
