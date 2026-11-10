//go:build windows || darwin

package namespace

import "testing"

func TestSpaceEntriesExplicitTarget(t *testing.T) {
	target := "C:\\isolated\\server-account-space"
	entries := SpaceEntries("icon", []SpaceMount{{Kind: "personal", Port: 19082, TargetPath: target}, {Kind: "shared", TargetPath: target, ReadOnly: true}})
	for _, e := range entries {
		if e.TargetPath != target {
			t.Fatalf("显式根被 UNC 覆盖: %+v", e)
		}
	}
}

func TestSpaceEntriesLegacyTarget(t *testing.T) {
	entries := SpaceEntries("", []SpaceMount{{Kind: "personal", Port: 19082}})
	if entries[0].TargetPath != WebDAVUNC(19082) {
		t.Fatalf("旧平台端口兼容失败: %+v", entries[0])
	}
}
