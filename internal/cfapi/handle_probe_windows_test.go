//go:build windows

package cfapi

import (
	"path/filepath"
	"syscall"
	"testing"
)

// TestOpenPlaceholderHandleDoesNotHydrate 定位「打开句柄是否触发下载」。
//
// 背景：TestUpdatePlaceholderChangesSizeWithoutDownload 发现更新元数据后
// **磁盘分配 = 39 字节**（等于文件大小），说明内容被拉下来了。
// 必须查清是哪一步触发的——若是 OpenPlaceholderHandle 用 GENERIC_READ，
// 那么**每次元数据同步都会顺带下载整个文件**，
// 「列表零下载」这个核心承诺就废了。
//
// 本用例把「打开」与「更新」两步分开测量。
func TestOpenPlaceholderHandleDoesNotHydrate(t *testing.T) {
	content := []byte("content that should not be downloaded")
	root, cleanup := mountForUpdateProbe(t, &content)
	defer cleanup()
	full := filepath.Join(root, "up.txt")

	// ① 打开前
	before := allocatedSize(t, full)
	if before != 0 {
		t.Fatalf("前置条件不成立：打开前磁盘分配 = %d，期望 0", before)
	}

	// ② 打开句柄（当前实现用 GENERIC_READ）
	h, err := OpenPlaceholderHandle(full)
	if err != nil {
		t.Fatalf("打开句柄失败: %v", err)
	}
	afterOpen := allocatedSize(t, full)
	syscall.CloseHandle(h)

	if afterOpen != 0 {
		t.Errorf("⚠️ **仅打开句柄就触发了下载**：磁盘分配 %d → %d。"+
			"若元数据同步也这样，每次同步都会把整个文件拉下来——"+
			"「列表零下载」的核心承诺失效。应改用 FILE_READ_ATTRIBUTES "+
			"（只读属性，不要数据访问权）", before, afterOpen)
	} else {
		t.Logf("打开句柄未触发下载（磁盘分配仍为 %d）", afterOpen)
	}
}

// TestUpdatePlaceholderWithAttributesOnlyAccess 验证用
// **FILE_READ_ATTRIBUTES** 打开句柄能否完成元数据更新，且不触发下载。
//
// 这是上面那条的诊断结论：CfUpdatePlaceholder 是「元数据」操作，
// 不该要求数据访问权。若这条通过，OpenPlaceholderHandle 就该改用
// FILE_READ_ATTRIBUTES——既满足接口，又不违反「零下载」。
func TestUpdatePlaceholderWithAttributesOnlyAccess(t *testing.T) {
	content := []byte("attributes-only access test")
	root, cleanup := mountForUpdateProbe(t, &content)
	defer cleanup()
	full := filepath.Join(root, "up.txt")

	// 只请求属性访问权，不要 GENERIC_READ
	pathPtr, err := syscall.UTF16PtrFromString(full)
	if err != nil {
		t.Fatalf("路径转换失败: %v", err)
	}
	const fileReadAttributes = 0x0080
	h, err := syscall.CreateFile(
		pathPtr,
		fileReadAttributes,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil,
		syscall.OPEN_EXISTING,
		syscall.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		t.Skipf("FILE_READ_ATTRIBUTES 打开失败（%v）——该方案不可行，需另找办法", err)
	}
	defer syscall.CloseHandle(h)

	if alloc := allocatedSize(t, full); alloc != 0 {
		t.Errorf("用属性权限打开也触发了下载：分配 = %d", alloc)
	}

	meta := FsMetadata{
		BasicInfo: FileBasicInfo{
			FileAttributes: FileAttributeNormal | FileAttributeReadOnly,
			LastWriteTime:  TimeToFiletime(1767226000),
			ChangeTime:     TimeToFiletime(1767226000),
			CreationTime:   TimeToFiletime(1767225600),
			LastAccessTime: TimeToFiletime(1767226000),
		},
		FileSize: int64(len(content)),
	}
	if err := UpdatePlaceholder(h, &meta, EncodeIdentity(8888), UpdateFlagMarkInSync); err != nil {
		t.Fatalf("用属性权限句柄做 CfUpdatePlaceholder 失败: %v —— "+
			"说明该接口确实需要数据访问权，那「零下载的元数据同步」做不到，需另设计", err)
	}
	if alloc := allocatedSize(t, full); alloc != 0 {
		t.Errorf("更新后触发了下载：分配 = %d（期望 0）", alloc)
	}
	t.Logf("FILE_READ_ATTRIBUTES 可完成元数据更新且磁盘分配为 0")
}
