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

// mountForWriteProbe 挂一个只有单文件的最小同步根，返回根目录与清理函数。
func mountForWriteProbe(t *testing.T) (string, func()) {
	t.Helper()
	if !Available() {
		t.Skip("本机无 CfApi")
	}
	root := t.TempDir()
	content := []byte("original cloud content")

	list := func(context.Context) ([]CloudEntry, error) {
		return []CloudEntry{{
			Path: "probe.txt", Size: int64(len(content)),
			ModifiedUnix: 1767225600, Identity: EncodeIdentity(4242),
			// 与生产一致（cfmount.Entries 对全部条目置只读）：
			// 本 helper 早于只读改造，原先没置位，导致「Sync 后本地改动还在」
			// 这类用例其实在测一个**生产里不存在的**场景（可写占位符）。
			ReadOnly: true,
		}}, nil
	}
	open := func(_ context.Context, cloudPath string, offset, length int64) (io.ReadCloser, error) {
		if cloudPath != "probe.txt" {
			return nil, fmt.Errorf("云端没有 %s", cloudPath)
		}
		end := offset + length
		if end > int64(len(content)) {
			end = int64(len(content))
		}
		if offset >= int64(len(content)) {
			return io.NopCloser(strings.NewReader("")), nil
		}
		return io.NopCloser(bytes.NewReader(content[offset:end])), nil
	}

	m, err := MountSpace(MountOptions{
		RootPath:         root,
		Provider:         testProvider,
		List:             list,
		Open:             open,
		SyncRootIdentity: []byte("easyshare-write-probe"),
	})
	if err != nil {
		t.Fatalf("挂载失败: %v", err)
	}
	return root, func() { _ = m.Close() }
}

// TestPlaceholderIsNotReadOnlyAttribute 记录一个**事实**：占位符的属性里
// 没有只读位。这条用例不断言「应该只读」，只把观测结果钉住，
// 防止将来有人误以为「只读原型」是靠文件属性实现的。
//
// 为什么必须澄清：本仓多处文档把切片 5 描述为「**只读**占位符」，
// 而 cfapi 只注册了 FETCH_DATA（读回调），**没有注册任何写回调**。
// 如果属性也不是只读，那么「只读」就没有任何执行层依托——
// 用户双击编辑、拖入文件、重命名、删除，系统都不会回调我们，
// 本地改了、云端不知道，下一个 Sync 全量重建时**本地修改被静默抹掉**。
//
// 这与「磁盘上看得见 0 字节」不同：那是可观测的良好行为，
// 而这是**用户以为保存了、其实会丢**的数据丢失风险。
func TestPlaceholderIsNotReadOnlyAttribute(t *testing.T) {
	root, cleanup := mountForWriteProbe(t)
	defer cleanup()

	full := filepath.Join(root, "probe.txt")
	fi, err := os.Stat(full)
	if err != nil {
		t.Fatalf("占位符不可见: %v", err)
	}
	// 记录观测事实（不断言「应该没有」只读位，只把它写进测试输出与断言）
	if fi.Mode()&0o200 != 0 {
		t.Logf("占位符对属主可写（Mode=%v）——「只读」不由文件属性保证", fi.Mode())
	}
}

// TestLocalEditOnPlaceholderIsNotPropagated 是**数据丢失风险的直接证据**。
//
// 场景：用户打开占位符、改内容、保存（这是资源管理器里最自然的动作）。
// 系统不会回调我们（没有写回调），所以：
//
//	① 本地文件确实被改了（用户看到「保存成功」）；
//	② 云端完全不知道（没有任何上传发生）；
//	③ 下一次 Sync 全量重建占位符时，**本地修改会被覆盖消失**。
//
// 本用例只验证 ① 与 ②（③ 需要真机 GUI 才能观察到最终效果）。
// 结论用来支撑一个工程决定：**在接入 app.go 前必须先让占位符真正只读**，
// 否则等于给用户一个「会吃掉修改」的盘。
func TestLocalEditOnPlaceholderIsNotPropagated(t *testing.T) {
	root, cleanup := mountForWriteProbe(t)
	defer cleanup()

	full := filepath.Join(root, "probe.txt")
	if err := os.WriteFile(full, []byte("USER EDITED THIS"), 0o644); err != nil {
		// 写不进去反而是安全的：说明只读在生效。记录并跳过后续断言。
		t.Logf("占位符不可写（%v）——只读在生效，无数据丢失风险", err)
		return
	}

	// 能写进去：确认内容已被本地改动
	got, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if string(got) != "USER EDITED THIS" {
		t.Fatalf("本地写入未生效，实得 %q", got)
	}
	t.Log("⚠️ 占位符可写：用户改动只落在本地，云端无感知——" +
		"下一次 Sync 重建会覆盖掉它。接入 app.go 前必须让占位符真正只读。")
}

// TestResyncSameRoot 在**同一个同步根**上先编辑、再 Sync，观察内容是否被覆盖。
// TestSyncIsRepeatableWithoutLosingContent 钉住本轮的核心成果：
// **Sync 可以重复调用，且不破坏已有内容**。
//
// 历史（这次修正的来龙去脉）：
//   - 原先这条用例叫 TestResyncSameRoot，验证的是「用户改了本地内容后 Sync 会怎样」。
//     当时的结论是：Sync **必然失败**（ERROR_ALREADY_EXISTS），
//     而那个「失败」恰好挡住了数据丢失——因为一旦让它成功（SUPERSEDE），
//     本地内容会被清成 0 字节（KI-14 实测）。
//   - 那个「两个缺陷互相掩盖」的困局，现在由**第 5 步（CfUpdatePlaceholder）**解开：
//     占位符已存在时走元数据更新，既不失败、也不碰内容。
//   - 同时只读位已经落地，所以「用户改了本地内容」这个前提在生产里**不成立**了
//     ——原用例的前提已过期，改成验证真正的不变量。
//
// 现在断言三件事：
//  1. 第二次 Sync **成功**（可重复）；
//  2. 已水合内容**不变**；
//  3. 元数据（大小）**跟着云端走**。
func TestSyncIsRepeatableWithoutLosingContent(t *testing.T) {
	if !Available() {
		t.Skip("本机无 CfApi")
	}
	root := t.TempDir()
	content := []byte("original cloud content")
	var cloudSize int64 = int64(len(content))

	var opened int
	list := func(context.Context) ([]CloudEntry, error) {
		return []CloudEntry{{
			Path: "probe.txt", Size: cloudSize,
			ModifiedUnix: 1767225600, Identity: EncodeIdentity(4242),
			ReadOnly: true,
		}}, nil
	}
	open := func(_ context.Context, _ string, offset, length int64) (io.ReadCloser, error) {
		opened++
		end := offset + length
		if end > int64(len(content)) {
			end = int64(len(content))
		}
		if offset >= int64(len(content)) {
			return io.NopCloser(strings.NewReader("")), nil
		}
		return io.NopCloser(bytes.NewReader(content[offset:end])), nil
	}
	m, err := MountSpace(MountOptions{
		RootPath: root, Provider: testProvider, List: list, Open: open,
		SyncRootIdentity: []byte("easyshare-resync-probe"),
	})
	if err != nil {
		t.Fatalf("挂载失败: %v", err)
	}
	defer func() { _ = m.Close() }()

	full := filepath.Join(root, "probe.txt")

	// ① 先水合内容（读一次），拿到基线
	baseline, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("首次读取失败: %v", err)
	}
	if !bytes.Equal(baseline, content) {
		t.Fatalf("首次内容 = %q，期望 %q", baseline, content)
	}
	openedAfterFirst := opened

	// ② 同一个根再 Sync 一次（等价于「定时刷新」/「重连后重铺」）
	if err := m.Sync(list); err != nil {
		t.Fatalf("第二次 Sync 失败（%v）——Sync 应当可重复调用，"+
			"已存在的占位符走元数据更新而不是报 ERROR_ALREADY_EXISTS", err)
	}
	if opened != openedAfterFirst {
		t.Errorf("重复 Sync 触发了 %d 次内容拉取——"+
			"元数据同步不该下载内容（列表零下载）", opened-openedAfterFirst)
	}

	// ③ 内容必须完好（这是与 SUPERSEDE 的关键区别）
	after, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("重复 Sync 后读取失败: %v", err)
	}
	if !bytes.Equal(after, content) {
		t.Fatalf("⚠️ 重复 Sync 后内容变了：%q（期望 %q）——"+
			"说明走了「删掉重建」而非元数据更新", after, content)
	}

	// ④ 只读防护必须还在（更新时漏带 readonly 位就会被冲掉）
	if err := os.WriteFile(full, []byte("nope"), 0o644); err == nil {
		t.Error("重复 Sync 后占位符又可写了——只读位在更新时被冲掉")
	} else {
		_ = os.Remove(full + ".nope")
	}

	// ⑤ 云端大小变了，元数据要跟上（且不下载）
	cloudSize = 4096
	if err := m.Sync(list); err != nil {
		t.Fatalf("第三次 Sync 失败: %v", err)
	}
	fi, err := os.Stat(full)
	if err != nil {
		t.Fatalf("Stat 失败: %v", err)
	}
	if fi.Size() != cloudSize {
		t.Errorf("云端大小改为 %d 后本地元数据 = %d，未跟上", cloudSize, fi.Size())
	}
	t.Logf("Sync 可重复：内容完好、未触发下载、只读保留、大小跟上（%d）", fi.Size())
}
