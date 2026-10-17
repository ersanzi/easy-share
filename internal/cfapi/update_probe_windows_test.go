//go:build windows

package cfapi

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// mountForUpdateProbe 挂一个单文件同步根，返回根目录、当前内容与清理函数。
// 内容与大小放在指针后面，方便测试中途「云端变了」。
func mountForUpdateProbe(t *testing.T, content *[]byte) (string, func()) {
	t.Helper()
	if !Available() {
		t.Skip("本机无 CfApi")
	}
	root := t.TempDir()
	list := func(context.Context) ([]CloudEntry, error) {
		return []CloudEntry{{
			Path: "up.txt", Size: int64(len(*content)),
			ModifiedUnix: 1767225600, Identity: EncodeIdentity(8888),
			ReadOnly: true,
		}}, nil
	}
	open := func(_ context.Context, _ string, offset, length int64) (io.ReadCloser, error) {
		b := *content
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
		SyncRootIdentity: []byte("easyshare-update-probe"),
	})
	if err != nil {
		t.Fatalf("挂载失败: %v", err)
	}
	return root, func() { _ = m.Close() }
}

// TestUpdatePlaceholderChangesSizeWithoutDownload 验证
// **CfUpdatePlaceholder 能改元数据且不下载内容**（路线图切片 5 第 5 步）。
//
// 这是「Sync 可重复」的正解：远端清单变了，只把大小/时间对齐，
// 占位符与已水合内容都保留。与「删掉重建 + SUPERSEDE」相对——
// 后者会把本地内容清成 0 字节（KI-14 实测）。
func TestUpdatePlaceholderChangesSizeWithoutDownload(t *testing.T) {
	content := []byte("original cloud content") // 22 字节
	root, cleanup := mountForUpdateProbe(t, &content)
	defer cleanup()

	full := filepath.Join(root, "up.txt")
	fi, err := os.Stat(full)
	if err != nil {
		t.Fatalf("占位符不可见: %v", err)
	}
	if fi.Size() != 22 {
		t.Fatalf("初始大小 = %d，期望 22", fi.Size())
	}

	// 云端内容变长（模拟别人改了文件）
	content = []byte("a much longer cloud content than before")
	_ = content
	h, err := OpenPlaceholderHandle(full)
	if err != nil {
		t.Fatalf("打开占位符句柄失败: %v", err)
	}
	defer syscall.CloseHandle(h)

	newSize := int64(len(content))
	meta := FsMetadata{
		BasicInfo: FileBasicInfo{
			FileAttributes: FileAttributeNormal | FileAttributeReadOnly,
			LastWriteTime:  TimeToFiletime(1767225700),
			ChangeTime:     TimeToFiletime(1767225700),
			CreationTime:   TimeToFiletime(1767225600),
			LastAccessTime: TimeToFiletime(1767225700),
		},
		FileSize: newSize,
	}
	if err := UpdatePlaceholder(h, &meta, EncodeIdentity(8888), UpdateFlagMarkInSync); err != nil {
		t.Fatalf("CfUpdatePlaceholder 失败: %v", err)
	}

	// ① 元数据已更新
	fi2, err := os.Stat(full)
	if err != nil {
		t.Fatalf("更新后 Stat 失败: %v", err)
	}
	if fi2.Size() != newSize {
		t.Errorf("更新后大小 = %d，期望 %d", fi2.Size(), newSize)
	}
	// ② **没有发生下载**：磁盘分配仍应为 0（内容没被拉下来）
	if alloc := allocatedSize(t, full); alloc != 0 {
		t.Errorf("更新元数据后磁盘分配 = %d，期望 0——"+
			"说明发生了下载；CfUpdatePlaceholder 不该拉内容", alloc)
	}
}

// TestUpdatePlaceholderPreservesHydratedContent 是本轮**最要紧**的一条。
//
// KI-14 的数据丢失路径是「删掉重建」：本地已水合的内容被清成 0 字节。
// CfUpdatePlaceholder 换的是元数据，**不能碰内容**。
//
// 用例：先读文件把内容水合到本地（磁盘分配 > 0），
// 再改元数据，然后**再读一次**——内容必须与云端一致，不能变空。
//
// 为什么必须实测：这是「用第 5 步替代 SUPERSEDE」这个决定的**全部依据**。
// 若 UpdatePlaceholder 也会清内容，那它跟删掉重建没区别，
// 本轮的工作就是白做，必须回去重新设计。
func TestUpdatePlaceholderPreservesHydratedContent(t *testing.T) {
	content := []byte("hydrated payload that must survive")
	root, cleanup := mountForUpdateProbe(t, &content)
	defer cleanup()

	full := filepath.Join(root, "up.txt")

	// ① 水合：读一次，内容进本地
	got, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("首次读取失败: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("首次读取内容不符：%q", got)
	}
	if allocBefore := allocatedSize(t, full); allocBefore == 0 {
		t.Log("（提示：首次读取后磁盘分配仍为 0，可能未真正水合；继续验证内容）")
	}

	// ② 改元数据（大小不变，只动时间——模拟「云端仅时间戳变了」）
	h, err := OpenPlaceholderHandle(full)
	if err != nil {
		t.Fatalf("打开句柄失败: %v", err)
	}
	meta := FsMetadata{
		BasicInfo: FileBasicInfo{
			FileAttributes: FileAttributeNormal | FileAttributeReadOnly,
			LastWriteTime:  TimeToFiletime(1767225800),
			ChangeTime:     TimeToFiletime(1767225800),
			CreationTime:   TimeToFiletime(1767225600),
			LastAccessTime: TimeToFiletime(1767225800),
		},
		FileSize: int64(len(content)),
	}
	if err := UpdatePlaceholder(h, &meta, EncodeIdentity(8888), UpdateFlagMarkInSync); err != nil {
		syscall.CloseHandle(h)
		t.Fatalf("CfUpdatePlaceholder 失败: %v", err)
	}
	syscall.CloseHandle(h)

	// ③ **内容必须还在且正确**
	after, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("更新元数据后读取失败: %v —— 内容可能被清掉了", err)
	}
	if !bytes.Equal(after, content) {
		t.Fatalf("⚠️ 更新元数据后内容变了：%q（期望 %q）——"+
			"说明 CfUpdatePlaceholder 也会破坏已水合内容，"+
			"「用第 5 步替代 SUPERSEDE」这个方案不成立", after, content)
	}
	t.Logf("已水合内容在 CfUpdatePlaceholder 后完好（%d 字节）", len(after))
}

// TestUpdatePlaceholderKeepsReadOnly 验证更新后**只读位不丢**。
//
// 若 CfUpdatePlaceholder 把只读位冲掉（我们传的 attributes 里带了它，
// 但内核可能按自己的语义合并），KI-14 的防护就会在每次同步后失效——
// 用户改一次云端文件，本地就又能写了。
func TestUpdatePlaceholderKeepsReadOnly(t *testing.T) {
	content := []byte("readonly must persist")
	root, cleanup := mountForUpdateProbe(t, &content)
	defer cleanup()

	full := filepath.Join(root, "up.txt")
	h, err := OpenPlaceholderHandle(full)
	if err != nil {
		t.Fatalf("打开句柄失败: %v", err)
	}
	meta := FsMetadata{
		BasicInfo: FileBasicInfo{
			FileAttributes: FileAttributeNormal | FileAttributeReadOnly,
			LastWriteTime:  TimeToFiletime(1767225900),
			ChangeTime:     TimeToFiletime(1767225900),
			CreationTime:   TimeToFiletime(1767225600),
			LastAccessTime: TimeToFiletime(1767225900),
		},
		FileSize: int64(len(content)),
	}
	if err := UpdatePlaceholder(h, &meta, EncodeIdentity(8888), UpdateFlagMarkInSync); err != nil {
		syscall.CloseHandle(h)
		t.Fatalf("CfUpdatePlaceholder 失败: %v", err)
	}
	syscall.CloseHandle(h)

	// 只读位必须还在：写入仍应被拒
	if err := os.WriteFile(full, []byte("should not work"), 0o644); err == nil {
		t.Error("更新元数据后占位符又可写了——KI-14 的只读防护被冲掉，" +
			"每次同步后用户都能改坏本地内容")
	} else {
		t.Logf("更新后仍只读（写入被拒：%v）", err)
	}
}
