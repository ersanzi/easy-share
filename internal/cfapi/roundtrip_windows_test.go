//go:build windows

package cfapi

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// testProvider 是测试用的固定提供方身份。
//
// 用固定 GUID 而非随机：注册-注销往返要可重复，且万一清理失败，
// 留下的痕迹可被辨认。
var testProvider = ProviderInfo{
	Name:    "EasyShareTest",
	Version: "0.1.0",
	ID: windows.GUID{
		Data1: 0x1A2B3C4D, Data2: 0x5E6F, Data3: 0x4A7B,
		Data4: [8]byte{0x8C, 0x9D, 0x0E, 0x1F, 0x2A, 0x3B, 0x4C, 0x5D},
	},
}

// TestAvailable 确认本机 cldapi.dll 可用且能解析出我们要的函数。
//
// 这是「动态加载」这条设计的直接验证：若将来 Windows 改了导出名，
// 本用例会先于真机失败。
func TestAvailable(t *testing.T) {
	if !Available() {
		t.Fatal("Available() = false，本机应支持 CfApi（Win10 1709+）")
	}
	for _, name := range []string{
		"CfRegisterSyncRoot", "CfUnregisterSyncRoot", "CfCreatePlaceholders",
	} {
		if _, err := proc(name); err != nil {
			t.Errorf("解析 %s 失败: %v", name, err)
		}
	}
}

// TestRegisterUnregisterSyncRoot 在临时目录上做注册/注销往返。
//
// 它验证的不只是「函数能调到」，而是**结构体布局真的被内核接受**——
// 布局错位通常在这里返回非 0 状态码，而不是等到创建占位符才炸。
func TestRegisterUnregisterSyncRoot(t *testing.T) {
	if !Available() {
		t.Skip("本机无 CfApi")
	}
	root := t.TempDir()

	sr, err := NewSyncRoot(testProvider,
		[]byte("easyshare-syncroot-test"),
		[]byte("easyshare-fileid-"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := RegisterSyncRoot(root, &sr.Registration, &sr.Policies, RegisterFlagNone); err != nil {
		t.Fatalf("RegisterSyncRoot 失败: %v（0x80070005 类权限错误请以管理员重试）", err)
	}
	if err := UnregisterSyncRoot(root); err != nil {
		t.Errorf("UnregisterSyncRoot 失败（已注册但未清理，需手工处理 %s）: %v", root, err)
	}
	if _, err := os.Stat(filepath.Clean(root)); err != nil {
		t.Errorf("操作后目录不可访问: %v", err)
	}
}

// TestCreatePlaceholder 是切片 5 的核心验收：在同步根里创建一个占位符，
// 断言它**看得见、大小正确、但磁盘占用为 0**。
//
// 这条用例证明了「列表零下载、点开才拉」的前半段——占位符真的建出来了，
// 且资源管理器读到的元数据与云端一致。
func TestCreatePlaceholder(t *testing.T) {
	if !Available() {
		t.Skip("本机无 CfApi")
	}
	root := t.TempDir()

	sr, err := NewSyncRoot(testProvider,
		[]byte("easyshare-syncroot-placeholder-test"),
		[]byte("easyshare-fileid-"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := RegisterSyncRoot(root, &sr.Registration, &sr.Policies, RegisterFlagNone); err != nil {
		t.Fatalf("RegisterSyncRoot 失败: %v", err)
	}
	defer func() {
		if err := UnregisterSyncRoot(root); err != nil {
			t.Errorf("清理注册失败: %v", err)
		}
	}()

	const cloudSize = int64(12345678) // 云端真实大小，明显大于 0
	const mtime = 1767225600          // 2026-01-01 UTC

	ph := NewPlaceholders([]PlaceholderSpec{{
		RelativeName: "hello.txt",
		Size:         cloudSize,
		ModifiedUnix: mtime,
		CreatedUnix:  mtime,
		Identity:     []byte("easyshare-fileid-000001"),
	}})
	if len(ph.Infos) != 1 {
		t.Fatalf("构造占位符失败：得到 %d 条", len(ph.Infos))
	}

	processed, err := CreatePlaceholders(root, ph.Infos, CreateFlagNone)
	if err != nil {
		t.Fatalf("CreatePlaceholders 失败: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d，期望 1", processed)
	}
	// 批量接口允许部分成功：必须逐条检查 Result
	if r := ph.Infos[0].Result; r != 0 {
		t.Fatalf("占位符 Result = 0x%08x，期望 0", uint32(r))
	}

	// 核心断言①：文件在磁盘上看得见
	p := filepath.Join(root, "hello.txt")
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatalf("占位符未被创建: %v", err)
	}
	// 核心断言②：资源管理器/Stat 读到的是**云端大小**
	if fi.Size() != cloudSize {
		t.Errorf("Stat 大小 = %d，期望云端真实大小 %d", fi.Size(), cloudSize)
	}
	// 核心断言③：磁盘实际占用 ≈ 0（未下载）
	// 占位符的「逻辑大小」是 cloudSize，但它在磁盘上不占数据块。
	alloc := allocatedSize(t, p)
	if alloc > 4096 {
		t.Errorf("磁盘分配 %d 字节，期望接近 0（应只留占位符元数据）", alloc)
	}
	t.Logf("占位符：Stat 大小=%d，磁盘分配=%d", fi.Size(), alloc)
}
