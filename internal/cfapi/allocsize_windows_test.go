//go:build windows

package cfapi

import (
	"syscall"
	"testing"
	"unsafe"
)

// allocatedSize 返回文件在磁盘上**实际分配的字节数**。
//
// 为什么不用 os.Stat().Size()：那是逻辑大小（占位符会报云端真实大小）。
// 要证明「占位符不占空间」，必须问文件系统实际分配了多少簇——
// GetCompressedFileSize 正是这个语义（对未下载的占位符返回 0）。
func allocatedSize(t *testing.T, path string) int64 {
	t.Helper()
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getCompressedFileSize := kernel32.NewProc("GetCompressedFileSizeW")

	var high uint32
	low, _, errno := getCompressedFileSize.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&high)),
	)
	// GetCompressedFileSize 用 INVALID_FILE_SIZE 报错；LowPart == 0xFFFFFFFF 时
	// 必须看 GetLastError 才能区分「真的是 4GB-1」与「出错」。
	if uint32(low) == 0xFFFFFFFF {
		if errno != syscall.Errno(0) {
			t.Fatalf("GetCompressedFileSize 失败: %v", errno)
		}
	}
	return (int64(high) << 32) | int64(uint32(low))
}
