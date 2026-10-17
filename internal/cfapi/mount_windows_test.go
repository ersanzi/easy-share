//go:build windows

package cfapi

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMountSpaceEndToEnd 是全栈验收：模拟一个「云端」（内存里几个文件），
// 走完整流程挂载，然后：
//
//  1. 断言本地树结构正确、**全部零字节**（列表阶段不下载）；
//  2. 读一个文件，断言内容**逐字节等于云端内容**（按需拉取的完整往返）；
//  3. 断言只拉了被读的那个文件（**没被读的文件不该产生任何流量**）。
//
// 这是「生产级形态」的最小可运行证明：用户看到的是完整的树，
// 但磁盘上不占地方，只有真正打开的文件才会下载。
func TestMountSpaceEndToEnd(t *testing.T) {
	if !Available() {
		t.Skip("本机无 CfApi")
	}
	root := t.TempDir()

	// ── 模拟云端 ────────────────────────────────────────────────
	cloudFiles := map[string][]byte{
		"readme.txt":     []byte("hello from cloud"),
		"docs/guide.md":  []byte("# Guide\n\n这是文档内容。\n"),
		"docs/api/v1.md": []byte("API v1 spec"),
	}
	var opened []string // 记录被真正拉取的云端路径

	list := func(ctx context.Context) ([]CloudEntry, error) {
		var out []CloudEntry
		// 目录要显式列出（真实控制面里由 es_file 目录层提供）
		out = append(out,
			CloudEntry{Path: "docs", IsDir: true, ModifiedUnix: 1767225600, Identity: EncodeIdentity(9001)},
			CloudEntry{Path: "docs/api", IsDir: true, ModifiedUnix: 1767225600, Identity: EncodeIdentity(9002)},
		)
		id := int64(1000)
		for p, b := range cloudFiles {
			id++
			out = append(out, CloudEntry{
				Path: p, Size: int64(len(b)),
				ModifiedUnix: 1767225600,
				Identity:     EncodeIdentity(id),
			})
		}
		return out, nil
	}
	open := func(ctx context.Context, cloudPath string, offset, length int64) (io.ReadCloser, error) {
		opened = append(opened, cloudPath)
		b, ok := cloudFiles[cloudPath]
		if !ok {
			return nil, fmt.Errorf("云端没有 %s", cloudPath)
		}
		if offset >= int64(len(b)) {
			return io.NopCloser(strings.NewReader("")), nil
		}
		end := offset + length
		if end > int64(len(b)) {
			end = int64(len(b))
		}
		return io.NopCloser(bytes.NewReader(b[offset:end])), nil
	}

	// ── 挂载 ────────────────────────────────────────────────────
	m, err := MountSpace(MountOptions{
		RootPath:         root,
		Provider:         testProvider,
		List:             list,
		Open:             open,
		SyncRootIdentity: []byte("easyshare-e2e-test"),
	})
	if err != nil {
		t.Fatalf("MountSpace 失败: %v", err)
	}
	defer func() {
		if err := m.Close(); err != nil {
			t.Errorf("Close 失败: %v", err)
		}
	}()

	// ① 树结构 + 零字节
	for _, f := range []string{"readme.txt", "docs/guide.md", "docs/api/v1.md"} {
		p := filepath.Join(root, filepath.FromSlash(f))
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("%s 未建成: %v", f, err)
		}
		want := int64(len(cloudFiles[f]))
		if fi.Size() != want {
			t.Errorf("%s 逻辑大小 = %d，期望 %d", f, fi.Size(), want)
		}
		if alloc := allocatedSize(t, p); alloc > 4096 {
			t.Errorf("%s 磁盘分配 = %d，期望 0（列表阶段不应下载）", f, alloc)
		}
	}
	if len(opened) != 0 {
		t.Fatalf("列表阶段就拉取了内容：%v（占位符不该触发下载）", opened)
	}

	// ② 读一个文件 → 按需拉取
	got, err := os.ReadFile(filepath.Join(root, "docs", "guide.md"))
	if err != nil {
		t.Fatalf("读取 docs/guide.md 失败: %v", err)
	}
	if !bytes.Equal(got, cloudFiles["docs/guide.md"]) {
		t.Errorf("内容不匹配：\n got=%q\nwant=%q", got, cloudFiles["docs/guide.md"])
	}

	// ③ 只拉了被读的那个
	if len(opened) != 1 || opened[0] != "docs/guide.md" {
		t.Errorf("拉取记录 = %v，期望只拉 docs/guide.md", opened)
	}

	// 另一个文件仍未拉取
	if _, err := os.Stat(filepath.Join(root, "readme.txt")); err != nil {
		t.Errorf("readme.txt 应仍在：%v", err)
	}
	readmeAlloc := allocatedSize(t, filepath.Join(root, "readme.txt"))
	t.Logf("读前拉取 %v；guide.md 已水合，readme.txt 磁盘分配 = %d", opened, readmeAlloc)
}

// TestEncodeDecodeIdentity 覆盖身份编解码（纯函数）。
func TestEncodeDecodeIdentity(t *testing.T) {
	for _, id := range []int64{0, 1, 42, 1234567890123456789} {
		b := EncodeIdentity(id)
		got, ok := decodeIdentity(b)
		if !ok {
			t.Fatalf("EncodeIdentity(%d) 无法解码：%q", id, b)
		}
		if want := fmt.Sprint(id); got != want {
			t.Fatalf("decodeIdentity(%q) = %q，期望 %q", b, got, want)
		}
	}
	// 非法输入
	for _, b := range [][]byte{nil, {}, []byte("nope"), []byte("esf:")} {
		if _, ok := decodeIdentity(b); ok {
			t.Errorf("decodeIdentity(%q) 竟然成功", b)
		}
	}
}
