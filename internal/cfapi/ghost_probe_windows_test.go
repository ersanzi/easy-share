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

// mountForGhostProbe 挂一个单文件同步根，云端内容可变、可删。
func mountForGhostProbe(t *testing.T, files map[string][]byte) (string, *Mount, func()) {
	t.Helper()
	if !Available() {
		t.Skip("本机无 CfApi")
	}
	root := t.TempDir()
	list := func(context.Context) ([]CloudEntry, error) {
		out := make([]CloudEntry, 0, len(files))
		id := int64(7000)
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
		SyncRootIdentity: []byte("easyshare-ghost-probe"),
	})
	if err != nil {
		t.Fatalf("挂载失败: %v", err)
	}
	return root, m, func() { _ = m.Close() }
}

// TestRetainedGhostStillReadableFromLocal 验证**保留下来的水合幽灵仍可读**。
//
// 这是「保留而不删」这个决定的**收益**：内容本来就在本地，
// 云端虽然删了，但用户仍能打开它、把内容救回去。
// 若保留下来却读不出（例如读取强制经回调、而回调按新索引查不到），
// 那保留就没有意义——用户看着一个打不开的文件，还是拿不到数据。
//
// 场景按队列走：水合 → 云端删 → Sync（清理未水合的、保留水合的）→ 读。
func TestRetainedGhostStillReadableFromLocal(t *testing.T) {
	files := map[string][]byte{"keepme.txt": []byte("local content that must stay reachable")}
	root, m, cleanup := mountForGhostProbe(t, files)
	defer cleanup()
	full := filepath.Join(root, "keepme.txt")

	// ① 水合，让内容落到本地
	before, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("首次读取失败: %v", err)
	}
	if alloc := allocatedSize(t, full); alloc == 0 {
		t.Skip("文件未真正水合（磁盘分配 0），本用例前提不成立")
	}

	// ② 云端删除，然后 Sync
	delete(files, "keepme.txt")
	if err := m.Sync(func(ctx context.Context) ([]CloudEntry, error) {
		out := make([]CloudEntry, 0, len(files))
		id := int64(7100)
		for p, b := range files {
			id++
			out = append(out, CloudEntry{
				Path: p, Size: int64(len(b)),
				ModifiedUnix: 1767225600, Identity: EncodeIdentity(id),
				ReadOnly: true,
			})
		}
		return out, nil
	}); err != nil {
		t.Fatalf("Sync 失败: %v", err)
	}

	// ③ 它必须还在（已水合 → 保留）
	if _, err := os.Stat(full); err != nil {
		t.Fatalf("已水合幽灵被删掉了: %v", err)
	}

	// ④ 而且必须**还读得出内容**——这是保留的意义所在
	after, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("保留下来却读不出（%v）——那保留就没有意义，"+
			"用户看着一个打不开的文件还是拿不到数据", err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("内容变了：%q（期望 %q）", after, before)
	}
	t.Logf("保留下来的水合幽灵仍可读（%d 字节）——用户能救回本地仅存的内容", len(after))
}

// TestHydratedGhostRetainsLocalContent 记录一个**关键的数据安全事实**：
// 已水合的幽灵文件，本地仍持有内容。
//
// 这决定了修法：若「云端删除 → 本地删除」直接删掉它，
// 会把这份**本地仅存**的内容一起销毁。用户可能正是靠它在离线时看文件。
// 因此删除本地幽灵时要么只删**未水合**的，要么明确接受「云端是唯一真相源」。
func TestHydratedGhostRetainsLocalContent(t *testing.T) {
	files := map[string][]byte{"hydrated.txt": []byte("local only copy after cloud delete")}
	root, _, cleanup := mountForGhostProbe(t, files)
	defer cleanup()
	full := filepath.Join(root, "hydrated.txt")

	if _, err := os.ReadFile(full); err != nil {
		t.Fatalf("水合失败: %v", err)
	}
	alloc := allocatedSize(t, full)
	delete(files, "hydrated.txt")

	// 云端删了，但本地内容还在
	got, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	t.Logf("云端已删，本地仍持有 %d 字节（磁盘分配 %d）——"+
		"直接删掉它就是销毁本地仅存副本", len(got), alloc)
}
