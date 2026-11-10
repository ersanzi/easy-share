//go:build windows

package cfapi

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 普通本地文件不能依靠分配大小判断是否可删，尤其是尚未上传的空文件。
func TestGhostCleanupPreservesNestedLocalFiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "deleted", "nested")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"empty.txt", "content.txt"} {
		data := []byte(nil)
		if name == "content.txt" {
			data = []byte("仅存的本地文件")
		}
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0600); err != nil {
			t.Fatal(err)
		}
		if isSafeGhostPlaceholder(p) {
			t.Fatalf("普通本地文件被判为可删占位符: %s", p)
		}
	}
	emptyDir := filepath.Join(root, "empty-dir")
	if err := os.Mkdir(emptyDir, 0700); err != nil {
		t.Fatal(err)
	}
	removed, skipped := removeGhosts(root, nil)
	if removed != 1 || skipped != 2 {
		t.Fatalf("removed=%d skipped=%d，期望 1/2", removed, skipped)
	}
	for _, name := range []string{"empty.txt", "content.txt"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("本地文件及其父目录必须保留: %v", err)
		}
	}
	if _, err := os.Stat(emptyDir); !os.IsNotExist(err) {
		t.Fatalf("空目录未清理: %v", err)
	}
}

// 同一幽灵目录中水合/本地子项要保留，但安全占位符仍应清理。
func TestGhostCleanupMixedNestedDirectory(t *testing.T) {
	if !Available() {
		t.Skip("本机无 CfApi")
	}
	root := t.TempDir()
	entries := []CloudEntry{
		{Path: "gone", IsDir: true, Identity: EncodeIdentity(9100)},
		{Path: "gone/nested", IsDir: true, Identity: EncodeIdentity(9101)},
		{Path: "gone/nested/hydrated.txt", Size: 7, Identity: EncodeIdentity(9102)},
		{Path: "gone/nested/ghost.txt", Size: 7, Identity: EncodeIdentity(9103)},
		{Path: "gone/nested/empty-cloud.txt", Identity: EncodeIdentity(9104)},
	}
	list := func(context.Context) ([]CloudEntry, error) { return entries, nil }
	m, err := mountSpaceForTest(t, MountOptions{
		RootPath: root, Provider: testProvider, List: list,
		Open: func(context.Context, string, int64, int64) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("hydrate")), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	dir := filepath.Join(root, "gone", "nested")
	// 先真实水合，随后云端删除时本地内容是唯一副本。
	hydrated := filepath.Join(dir, "hydrated.txt")
	if _, err := os.ReadFile(hydrated); err != nil {
		t.Fatal(err)
	}
	localEmpty := filepath.Join(dir, "local-empty.txt")
	if err := os.WriteFile(localEmpty, nil, 0600); err != nil {
		t.Fatal(err)
	}
	removeGhosts(root, nil)
	if got, err := os.ReadFile(hydrated); err != nil || string(got) != "hydrate" {
		t.Fatalf("本地内容丢失: %q %v", got, err)
	}
	if _, err := os.Stat(localEmpty); err != nil {
		t.Fatalf("本地零字节文件丢失: %v", err)
	}
	for _, name := range []string{"ghost.txt", "empty-cloud.txt"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("安全占位符 %s 未清理: %v", name, err)
		}
	}
}

// 首次拉清单或建树失败，都应允许立即在同一路径重新挂载。
func TestMountSpaceInitialSyncFailureCleansResources(t *testing.T) {
	if !Available() {
		t.Skip("本机无 CfApi")
	}
	for _, stage := range []string{"list", "tree"} {
		t.Run(stage, func(t *testing.T) {
			root := t.TempDir()
			local := filepath.Join(root, "local.txt")
			if err := os.WriteFile(local, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			opt := MountOptions{
				RootPath: root, Provider: testProvider,
				Open: func(context.Context, string, int64, int64) (io.ReadCloser, error) {
					return nil, io.EOF
				},
				List: func(context.Context) ([]CloudEntry, error) {
					if stage == "list" {
						return nil, errFakeCloudDown
					}
					return []CloudEntry{{Path: "missing/child.txt", Identity: EncodeIdentity(9200)}}, nil
				},
			}
			m, err := mountSpaceForTest(t, opt)
			if err == nil || m != nil {
				if m != nil {
					_ = m.Close()
				}
				t.Fatalf("失败挂载应返回 nil/error，得到 %v / %v", m, err)
			}
			if stage == "list" && !errors.Is(err, errFakeCloudDown) {
				t.Fatalf("原始错误丢失: %v", err)
			}
			opt.List = func(context.Context) ([]CloudEntry, error) { return nil, nil }
			m, err = mountSpaceForTest(t, opt)
			if err != nil {
				t.Fatalf("失败后重新挂载失败: %v", err)
			}
			defer m.Close()
			if got, err := os.ReadFile(local); err != nil || string(got) != "keep" {
				t.Fatalf("失败清理损坏本地文件: %q %v", got, err)
			}
		})
	}
}

// 文件清单未显式列出目录时，也必须保留所有仍被引用的祖先目录。
func TestGhostCleanupPreservesImplicitParentDirectories(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "parent", "child")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	removed, _ := removeGhosts(root, []CloudEntry{{Path: "parent\\child\\pending.txt"}})
	if removed != 0 {
		t.Fatalf("仍被云端条目引用的目录被删除: %d", removed)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	}
}
