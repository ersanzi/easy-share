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

// TestSyncRemovesLocallyDeletedCloudFile 验证**云端删掉的文件**会不会从本地消失。
//
// 为什么现在必须查这条：`Sync` 此前**不可重复调用**（第一次刷新就报
// ERROR_ALREADY_EXISTS），所以「云端变了本地跟不跟」这个问题一直摸不到。
// 刚把 Sync 修成可重复（走 CfUpdatePlaceholder），这条路径**第一次变得可达**——
// 修好一个缺陷常常会让下一个暴露出来。
//
// 预期（先写下，再用实测核对）：当前 BuildTree 只做「创建/更新」，
// **没有任何删除逻辑**，所以云端删掉的文件应当**仍然留在本地**——
// 即一个点开就报错的"幽灵文件"。
//
// 本用例把观测结果如实记下来，不预设结论。
func TestSyncRemovesLocallyDeletedCloudFile(t *testing.T) {
	if !Available() {
		t.Skip("本机无 CfApi")
	}
	root := t.TempDir()

	files := map[string][]byte{
		"keep.txt": []byte("this file stays in the cloud"),
		"gone.txt": []byte("this file will be deleted in the cloud"),
	}
	list := func(context.Context) ([]CloudEntry, error) {
		out := make([]CloudEntry, 0, len(files))
		id := int64(9000)
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
		SyncRootIdentity: []byte("easyshare-delete-probe"),
	})
	if err != nil {
		t.Fatalf("挂载失败: %v", err)
	}
	defer func() { _ = m.Close() }()

	gone := filepath.Join(root, "gone.txt")
	if _, err := os.Stat(gone); err != nil {
		t.Fatalf("前置条件不成立：gone.txt 未建立：%v", err)
	}

	// 云端删掉它
	delete(files, "gone.txt")

	if err := m.Sync(list); err != nil {
		t.Fatalf("Sync 失败: %v", err)
	}

	// 云端已经没有了——本地应该也看不到
	if _, err := os.Stat(gone); err == nil {
		t.Errorf("⚠️ 云端已删除的文件**仍留在本地**（%s）——"+
			"BuildTree 只做创建/更新，没有删除逻辑。用户会看到一个"+
			"点开就报错的幽灵文件，而且它会**永久**留着（每次 Sync 都不动它）", gone)
	} else {
		t.Logf("本地已跟随云端删除（Stat 返回 %v）", err)
	}
	// 对照：没被删的那个必须还在（防止"把整个根清掉"这种过度删除）
	if _, err := os.Stat(filepath.Join(root, "keep.txt")); err != nil {
		t.Errorf("未删除的 keep.txt 消失了：%v", err)
	}
}
