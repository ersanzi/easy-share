//go:build windows

package namespace

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func TestNavigationAndThisPCUseOneOwnedRoot(t *testing.T) {
	guid, err := windows.GenerateGUID()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	entry := SpaceEntries("", []SpaceMount{{Kind: "personal", TargetPath: root, UserID: "71", DisplayName: "  云盘测试  "}})[0]
	entry.CLSID = guid.String()
	t.Cleanup(func() { unregisterOne(entry.CLSID) })
	if err := registerOne(entry); err != nil {
		t.Fatal(err)
	}
	for _, path := range namespacePaths(entry.CLSID) {
		key, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
		if err != nil {
			t.Fatalf("两处入口应同时存在: %s %v", path, err)
		}
		name, _, err := key.GetStringValue("")
		key.Close()
		if err != nil || name != "云盘测试 的网盘" {
			t.Fatalf("昵称未trim: %q %v", name, err)
		}
	}
	bag, err := registry.OpenKey(registry.CURRENT_USER, `Software\Classes\CLSID\`+entry.CLSID+`\Instance\InitPropertyBag`, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	target, _, err := bag.GetStringValue("TargetFolderPath")
	bag.Close()
	if err != nil || filepath.Clean(target) != filepath.Clean(root) {
		t.Fatalf("入口未指向同一root: %q %v", target, err)
	}
	unregisterOne(entry.CLSID)
	for _, path := range append(namespacePaths(entry.CLSID), `Software\Classes\CLSID\`+entry.CLSID) {
		key, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
		if err == nil {
			key.Close()
			t.Fatalf("退出未撤入口: %s", path)
		}
		if err != registry.ErrNotExist {
			t.Fatal(err)
		}
	}
}
