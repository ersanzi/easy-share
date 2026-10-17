//go:build windows

package cfapi

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBuildTree 是编排层的真机验收：给一份**扁平清单**（含多层目录），
// 断言建出来的本地树结构、大小都对，且**不下载任何内容**。
//
// 这份清单模仿真实控制面返回的形态：目录不带尾斜杠，文件带大小，
// 混排且未按深度排序（编排层必须自己排序）。
func TestBuildTree(t *testing.T) {
	if !Available() {
		t.Skip("本机无 CfApi")
	}
	root := t.TempDir()

	sr, err := NewSyncRoot(testProvider, []byte("easyshare-syncroot-tree"), []byte("esf:"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := RegisterSyncRoot(root, &sr.Registration, &sr.Policies, RegisterFlagNone); err != nil {
		t.Fatalf("RegisterSyncRoot 失败: %v", err)
	}
	defer func() { _ = UnregisterSyncRoot(root) }()

	// 故意乱序 + 多层，验证编排层自己排序（先父后子）
	entries := []CloudEntry{
		{Path: "docs/2026/plan.md", Size: 111, ModifiedUnix: 1767225600, Identity: []byte("f:4")},
		{Path: "readme.txt", Size: 22, ModifiedUnix: 1767225600, Identity: []byte("f:1")},
		{Path: "docs", IsDir: true, ModifiedUnix: 1767225600, Identity: []byte("d:1")},
		{Path: "docs/2026", IsDir: true, ModifiedUnix: 1767225600, Identity: []byte("d:2")},
		{Path: "docs/guide.md", Size: 33, ModifiedUnix: 1767225600, Identity: []byte("f:3")},
		{Path: "empty", IsDir: true, ModifiedUnix: 1767225600, Identity: []byte("d:3")},
	}

	stats := BuildTree(root, entries)
	if len(stats.Failures) != 0 {
		t.Fatalf("建树失败 %d 条：%+v（错误：%s）", len(stats.Failures), stats.Failures, stats.Error())
	}
	if stats.DirsCreated != 3 || stats.FilesCreated != 3 {
		t.Fatalf("统计 = %d 目录 / %d 文件，期望 3 / 3", stats.DirsCreated, stats.FilesCreated)
	}

	// ── 断言本地结构 ────────────────────────────────────────────
	for _, d := range []string{"docs", "docs/2026", "empty"} {
		fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(d)))
		if err != nil {
			t.Errorf("目录 %s 不存在: %v", d, err)
			continue
		}
		if !fi.IsDir() {
			t.Errorf("%s 不是目录", d)
		}
	}
	for _, f := range []struct {
		rel  string
		size int64
	}{
		{"readme.txt", 22}, {"docs/guide.md", 33}, {"docs/2026/plan.md", 111},
	} {
		p := filepath.Join(root, filepath.FromSlash(f.rel))
		fi, err := os.Stat(p)
		if err != nil {
			t.Errorf("文件 %s 不存在: %v", f.rel, err)
			continue
		}
		if fi.Size() != f.size {
			t.Errorf("%s 大小 = %d，期望 %d", f.rel, fi.Size(), f.size)
		}
		// 核心：**不占磁盘**（一个都没下载）
		if alloc := allocatedSize(t, p); alloc > 4096 {
			t.Errorf("%s 磁盘分配 = %d，期望接近 0", f.rel, alloc)
		}
	}
	t.Logf("建树完成：%d 目录 / %d 文件，全部零字节", stats.DirsCreated, stats.FilesCreated)
}

// TestBuildTreePathDepthAndSplit 覆盖编排层的纯函数（不需要 CfApi）。
func TestBuildTreePathDepthAndSplit(t *testing.T) {
	for _, c := range []struct {
		in    string
		depth int
		par   string
		name  string
	}{
		{"a.txt", 1, "", "a.txt"},
		{"a/b.txt", 2, "a", "b.txt"},
		{"a/b/c.txt", 3, "a/b", "c.txt"},
		{"/a.txt", 1, "", "a.txt"},
		{"a\\b.txt", 2, "a", "b.txt"}, // 容错：反斜杠也当分隔符
		{"", 0, "", ""},
	} {
		if got := pathDepth(c.in); got != c.depth {
			t.Errorf("pathDepth(%q) = %d，期望 %d", c.in, got, c.depth)
		}
		p, n := splitParent(c.in)
		if p != c.par || n != c.name {
			t.Errorf("splitParent(%q) = (%q,%q)，期望 (%q,%q)", c.in, p, n, c.par, c.name)
		}
	}
}

// TestBuildTreePartialFailure 验证「父目录创建失败 → 子项被记为失败但不 panic」。
//
// 手法：给一个不存在的 rootPath（未注册为同步根），让所有创建都失败。
func TestBuildTreePartialFailure(t *testing.T) {
	if !Available() {
		t.Skip("本机无 CfApi")
	}
	// 未注册为同步根的普通目录：CfCreatePlaceholders 应拒绝
	root := t.TempDir()

	entries := []CloudEntry{
		{Path: "d", IsDir: true, Identity: []byte("d:1")},
		{Path: "d/f.txt", Size: 5, Identity: []byte("f:1")},
	}
	stats := BuildTree(root, entries)
	if len(stats.Failures) == 0 {
		t.Fatal("在未注册的目录上建树竟然成功了——与预期不符")
	}
	if stats.DirsCreated != 0 || stats.FilesCreated != 0 {
		t.Fatalf("统计应为 0/0，实得 %d/%d", stats.DirsCreated, stats.FilesCreated)
	}
	if stats.Error() == "" {
		t.Error("Failures 非空时 Error() 不应为空")
	}
	t.Logf("预期内的整体失败：%s", stats.Error())
}
