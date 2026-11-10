//go:build windows

package namespace

import (
	"reflect"
	"testing"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// 只验证固定身份选择，不访问产品注册表或更改同步根。
func TestPersonalShellV2IdentityAndCleanup(t *testing.T) {
	if cloudCLSID != "{8327AC46-BCC2-45D2-A46F-0737E2CC6557}" || legacyCloudCLSID != "{E5A1F2B3-C4D5-6E7F-8A9B-0C1D2E3F4A5B}" {
		t.Fatal("固定迁移身份发生变化")
	}
	if _, err := windows.GUIDFromString(cloudCLSID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{cloudCLSID, legacyCloudCLSID} {
		if got := namespaceCleanupCLSIDs(id); !reflect.DeepEqual(got, []string{cloudCLSID, legacyCloudCLSID}) {
			t.Fatalf("启动/登出清理漏掉新旧个人身份: %v", got)
		}
	}
	for _, id := range []string{sharedSpaceCLSID, lanCLSID, "isolated"} {
		if got := namespaceCleanupCLSIDs(id); !reflect.DeepEqual(got, []string{id}) {
			t.Fatalf("其他入口被错误纳入迁移: %v", got)
		}
	}
	for _, user := range []string{"a", "b"} {
		target := `C:\test-root\` + user
		entries := SpaceEntries("", []SpaceMount{{Kind: "personal", TargetPath: target, UserID: user}})
		if len(entries) != 1 || entries[0].CLSID != cloudCLSID || entries[0].TargetPath != target || entries[0].UserID != user {
			t.Fatalf("换账号改变入口身份或丢失目标绑定: %+v", entries)
		}
	}
}

// 使用两个自有随机 GUID 验证迁移，不写产品 GUID，不发送 Shell 通知。
func TestShellV2OwnedMigration(t *testing.T) {
	owned := func() string {
		id, err := windows.GenerateGUID()
		if err != nil {
			t.Fatal(err)
		}
		guid := id.String()
		key, exists, err := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\CLSID\`+guid, registry.SET_VALUE)
		if err != nil {
			t.Fatal(err)
		}
		key.Close()
		if exists {
			t.Fatal("随机 GUID 已存在，拒绝接管")
		}
		t.Cleanup(func() {
			if err := removeNamespaceEntry(guid); err != nil {
				t.Error(err)
			}
		})
		return guid
	}
	old, current := owned(), owned()
	legacy := Entry{CLSID: old, Name: "isolated old", TargetPath: `C:\test-root\a`, UserID: "a"}
	if err := registerOne(legacy); err != nil {
		t.Fatal(err)
	}
	for _, account := range []string{"a", "a", "b"} {
		entry := Entry{CLSID: current, Name: "isolated current", TargetPath: `C:\test-root\` + account, UserID: account, writableFilesystem: true}
		if err := registerWithObsolete(entry, old); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{`Software\Classes\CLSID\` + old, `Software\Microsoft\Windows\CurrentVersion\Explorer\MyComputer\NameSpace\` + old} {
			k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
			if err == nil {
				k.Close()
				t.Fatal("旧入口没有被迁移清理")
			}
			if err != registry.ErrNotExist {
				t.Fatal(err)
			}
		}
		k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Classes\CLSID\`+current+`\Instance\InitPropertyBag`, registry.QUERY_VALUE)
		if err != nil {
			t.Fatal(err)
		}
		value, kind, err := k.GetStringValue("TargetFolderPath")
		k.Close()
		if err != nil || kind != registry.EXPAND_SZ || value != entry.TargetPath {
			t.Fatalf("新入口目标不匹配: %q type=%d err=%v", value, kind, err)
		}
	}
	if err := removeNamespaceEntry(current); err != nil {
		t.Fatal(err)
	}
	if err := removeNamespaceEntry(current); err != nil {
		t.Fatal("重复清理应幂等:", err)
	}
}
