//go:build windows

package cfapi

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGhostCleanupSparesHydratedFile 是幽灵清理的**安全闸**。
//
// 云端删除后，**已水合**的占位符在本地持有完整内容，而那份内容
// 在云端已经不存在了——它是**本地仅存**的一份。
// 清理逻辑若一视同仁地删，就是销毁用户数据，且用户毫无察觉。
//
// 本用例：先水合一个文件 → 云端删掉它 → Sync → **它必须还在**。
// 这是「代价不对称取保守一侧」的可执行版本：少删一个幽灵只是碍眼，
// 误删一个水合文件是真丢数据。
func TestGhostCleanupSparesHydratedFile(t *testing.T) {
	if !Available() {
		t.Skip("本机无 CfApi")
	}
	root := t.TempDir()
	files := map[string][]byte{
		"hydrated.txt":   []byte("local only copy after cloud delete"),
		"never-read.txt": []byte("this one is never hydrated"),
	}
	list := func(context.Context) ([]CloudEntry, error) {
		out := make([]CloudEntry, 0, len(files))
		id := int64(8000)
		for p, b := range files {
			id++
			out = append(out, CloudEntry{
				Path: p, Size: int64(len(b)),
				ModifiedUnix: 1767225600, Identity: EncodeIdentity(id),
				ReadOnly: true,
			})
		}
		return out, nil
	}
	open := func(_ context.Context, cloudPath string, offset, length int64) (io.ReadCloser, error) {
		b, ok := files[cloudPath]
		if !ok {
			return nil, io.EOF
		}
		end := offset + length
		if end > int64(len(b)) {
			end = int64(len(b))
		}
		if offset >= int64(len(b)) {
			return io.NopCloser(strings.NewReader("")), nil
		}
		return io.NopCloser(bytes.NewReader(b[offset:end])), nil
	}
	m, err := MountSpace(MountOptions{
		RootPath: root, Provider: testProvider, List: list, Open: open,
		SyncRootIdentity: []byte("easyshare-ghost-safety"),
	})
	if err != nil {
		t.Fatalf("挂载失败: %v", err)
	}
	defer func() { _ = m.Close() }()

	hydrated := filepath.Join(root, "hydrated.txt")
	neverRead := filepath.Join(root, "never-read.txt")

	// 只水合第一个
	if _, err := os.ReadFile(hydrated); err != nil {
		t.Fatalf("水合失败: %v", err)
	}
	if alloc := allocatedSize(t, hydrated); alloc == 0 {
		t.Skip("该文件未真正水合（磁盘分配 0），本用例前提不成立")
	}
	if alloc := allocatedSize(t, neverRead); alloc != 0 {
		t.Fatalf("前置条件不成立：never-read.txt 已被水合（分配 %d）", alloc)
	}

	// 云端把两个都删掉
	delete(files, "hydrated.txt")
	delete(files, "never-read.txt")
	if err := m.Sync(list); err != nil {
		t.Fatalf("Sync 失败: %v", err)
	}

	// ① 已水合的必须保留——它是本地仅存的一份
	if _, err := os.Stat(hydrated); err != nil {
		t.Fatalf("⚠️ 已水合的幽灵文件被删掉了（%v）——"+
			"那份内容是本地仅存，这是数据丢失", err)
	}
	got, err := os.ReadFile(hydrated)
	if err != nil {
		t.Fatalf("保留下来的文件读不出内容: %v", err)
	}
	if !bytes.Equal(got, []byte("local only copy after cloud delete")) {
		t.Errorf("内容变了：%q", got)
	}
	t.Logf("已水合幽灵被保留（%d 字节，本地仅存）", len(got))

	// ② 未水合的应当被清掉（它不持有任何字节，删了不丢数据）
	if _, err := os.Stat(neverRead); err == nil {
		t.Errorf("未水合的幽灵文件应被清理，但它还在")
	} else {
		t.Log("未水合幽灵已清理（不持有数据，删了不丢）")
	}
}
