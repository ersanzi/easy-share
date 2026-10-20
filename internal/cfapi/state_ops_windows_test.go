//go:build windows

package cfapi

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// TestPinAndDehydrateRoundTrip 在**真机**上钉住资源管理器右键那两个开关的内核语义：
// 「始终保留在这台电脑上」= pin，「释放空间」= dehydrate。
//
// 断言的不是「调用返回成功」，而是可观测的状态：
//   - 空壳阶段磁盘分配 0；
//   - 读过之后分配量涨上来（内容真在本地了）；
//   - 钉住之后 dehydrate **必须失败**——这是 SDK 对 `CF_PIN_STATE_PINNED` 的明文承诺
//     （cfapi.h 注释 + CF_PIN_STATE 文档：平台会拒绝对已钉占位符的脱水请求），
//     也是「始终保留」到底有没有生效的唯一硬判据；
//   - 取消钉住后 dehydrate 成功、分配量回到 0，再读仍然逐字节一致
//     （证明退回的是空壳而不是把文件弄坏）。
func TestPinAndDehydrateRoundTrip(t *testing.T) {
	if !Available() {
		t.Skip("本机无 CfApi")
	}
	root := t.TempDir()

	// 32 KiB：NTFS 簇 4 KiB，太小会让分配量测量看不出差别
	content := bytes.Repeat([]byte("easyshare-hydration-payload-"), 1365) // 30030 字节
	var fetches int
	list := func(ctx context.Context) ([]CloudEntry, error) {
		return []CloudEntry{{
			Path:         "keep.txt",
			Size:         int64(len(content)),
			ModifiedUnix: 1767225600,
			Identity:     EncodeIdentity(4242),
		}}, nil
	}
	open := func(ctx context.Context, cloudPath string, offset, length int64) (io.ReadCloser, error) {
		fetches++
		if cloudPath != "keep.txt" {
			return nil, fmt.Errorf("云端没有 %s", cloudPath)
		}
		end := offset + length
		if end > int64(len(content)) {
			end = int64(len(content))
		}
		return io.NopCloser(bytes.NewReader(content[offset:end])), nil
	}

	m, err := MountSpace(MountOptions{
		RootPath:         root,
		Provider:         testProvider,
		List:             list,
		Open:             open,
		SyncRootIdentity: []byte("easyshare-state-ops-test"),
	})
	if err != nil {
		t.Fatalf("MountSpace 失败: %v", err)
	}
	defer func() {
		if err := m.Close(); err != nil {
			t.Errorf("Close 失败: %v", err)
		}
	}()

	path := filepath.Join(root, "keep.txt")

	// ① 空壳：列表可见但零占用
	if alloc := allocatedSize(t, path); alloc != 0 {
		t.Fatalf("前置条件不成立：未读过的占位符磁盘分配 = %d，期望 0", alloc)
	}

	// ② 读 → 水合
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取占位符失败: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("水合内容与云端不一致：%d 字节 vs %d 字节", len(got), len(content))
	}
	hydrated := allocatedSize(t, path)
	if hydrated < int64(len(content)) {
		t.Fatalf("读后磁盘分配 = %d，应不小于内容长度 %d", hydrated, len(content))
	}

	// ③ 钉住后必须拒绝脱水
	if err := SetPinState(path, PinStatePinned, SetPinFlagNone); err != nil {
		t.Fatalf("SetPinState(Pinned) 失败: %v", err)
	}
	if err := Dehydrate(path); err == nil {
		t.Errorf("已钉住的占位符不该能脱水——「始终保留在这台电脑上」就没有生效")
	} else {
		t.Logf("钉住后脱水被拒（预期行为）：%v", err)
	}
	if alloc := allocatedSize(t, path); alloc < int64(len(content)) {
		t.Errorf("被拒的脱水不该动内容：分配 = %d", alloc)
	}

	// ④ 取消钉住 → 释放空间生效 → 再读仍能拿回同样的内容
	if err := SetPinState(path, PinStateUnspecified, SetPinFlagNone); err != nil {
		t.Fatalf("SetPinState(Unspecified) 失败: %v", err)
	}
	before := fetches
	if err := Dehydrate(path); err != nil {
		t.Fatalf("Dehydrate 失败: %v", err)
	}
	if alloc := allocatedSize(t, path); alloc != 0 {
		t.Errorf("释放空间后磁盘分配 = %d，期望 0（应退回空壳）", alloc)
	}

	again, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("释放后再读失败: %v", err)
	}
	if !bytes.Equal(again, content) {
		t.Errorf("释放后再读内容不符：占位符被破坏了")
	}
	if fetches <= before {
		t.Errorf("释放后再读应重新触发一次按需取数，fetches=%d（前值 %d）", fetches, before)
	}
}

// TestSetPinStateOnDirectoryRecurses 验证目录句柄能带递归位调用成功。
//
// 递归位只对目录有意义（SDK 明文），而「钉住整个文件夹」是用户的真实动作。
// 这里只断言 API 层面不报错——子项的实际状态由 ①/③ 那条用例覆盖。
func TestSetPinStateOnDirectoryRecurses(t *testing.T) {
	if !Available() {
		t.Skip("本机无 CfApi")
	}
	root := t.TempDir()

	list := func(ctx context.Context) ([]CloudEntry, error) {
		return []CloudEntry{
			{Path: "docs", IsDir: true, ModifiedUnix: 1767225600, Identity: EncodeIdentity(1)},
			{Path: "docs/a.txt", Size: 8, ModifiedUnix: 1767225600, Identity: EncodeIdentity(2)},
		}, nil
	}
	open := func(ctx context.Context, cloudPath string, offset, length int64) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader([]byte("12345678"))), nil
	}
	m, err := MountSpace(MountOptions{
		RootPath:         root,
		Provider:         testProvider,
		List:             list,
		Open:             open,
		SyncRootIdentity: []byte("easyshare-pin-dir-test"),
	})
	if err != nil {
		t.Fatalf("MountSpace 失败: %v", err)
	}
	defer m.Close()

	dir := filepath.Join(root, "docs")
	if err := SetPinState(dir, PinStatePinned, SetPinFlagRecurse); err != nil {
		t.Fatalf("对目录递归设置 pin 失败: %v", err)
	}
	// 子文件应随之被钉住：脱水它必须失败（同 ③ 的判据）
	if err := Dehydrate(filepath.Join(dir, "a.txt")); err == nil {
		t.Errorf("递归钉住后子文件仍能脱水，说明 Recurse 位没生效")
	}
	if err := SetPinState(dir, PinStateUnspecified, SetPinFlagRecurse); err != nil {
		t.Fatalf("解除递归 pin 失败: %v", err)
	}
}
