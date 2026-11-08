//go:build windows

package cfmount

import (
	"context"
	"strings"
	"testing"

	"easyshare/internal/drive"
)

func TestListDirectoryProjectionAndWritableOptIn(t *testing.T) {
	for _, explicitFirst := range []bool{true, false} {
		objects := []drive.Object{
			{Path: "docs", Directory: true, FileId: 1},
			{Path: "docs/empty", Directory: true, FileId: 2},
			{Path: "docs/a", FileId: 3, Size: 7},
		}
		if !explicitFirst {
			objects[0], objects[2] = objects[2], objects[0]
		}
		for _, writable := range []bool{false, true} {
			cfg := Config{Client: &fakeClient{objects: objects}, Token: func() string { return "token" }, Writable: writable}
			entries, err := List(cfg)(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 3 {
				t.Fatalf("重复父目录: %+v", entries)
			}
			seen := map[string]bool{}
			for _, e := range entries {
				if seen[e.Path] {
					t.Fatalf("重复路径 %s", e.Path)
				}
				seen[e.Path] = true
				if e.ReadOnly != !writable {
					t.Fatalf("只读策略不符: %+v", e)
				}
				if e.IsDir != (e.Path != "docs/a") {
					t.Fatalf("目录投影错误: %+v", e)
				}
			}
			if !findEntry(t, entries, "docs/empty").IsDir {
				t.Fatal("空目录被当作文件")
			}
		}
	}
}

func TestListRejectsFilesWithoutHydratableIdentity(t *testing.T) {
	for _, id := range []drive.FlexInt64{0, -1} {
		cfg := Config{Client: &fakeClient{objects: []drive.Object{{Path: "bad", FileId: id}}}, Token: func() string { return "token" }}
		entries, err := List(cfg)(context.Background())
		if err == nil || !strings.Contains(err.Error(), "FileId") || entries != nil {
			t.Fatalf("必须拒绝无效身份: %v %v", entries, err)
		}
	}
}
