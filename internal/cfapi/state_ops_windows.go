//go:build windows

package cfapi

// 本文件是占位符的**状态位操作**：pin state（资源管理器右键「始终保留在这台电脑上」）
// 与 dehydrate（「释放空间」）——WPS 与钉盘都把它做成桌面标配，见
// docs/intelligence/2026-10-15-wps-cfapi-placeholder-reverse.md §3.5 的
// `freeUpPlaceholder` / `KCfApiSyncFolderFreeUpHandler`，以及同文 §0b 第四步。
//
// 枚举取值与函数原型**逐字取自本机 Windows SDK 头**：
// `Windows Kits\10\Include\10.0.26100.0\um\cfapi.h` 第 860–914 行。
// 刻意不沿用本包 `CallbackType` 那批从 WPS 二进制里读出的编号——那是 WPS 自己
// 编译期 SDK 的编号，与本机不一致（详见 cfapi_windows.go 里 CallbackType 的更正）。

import (
	"fmt"
	"syscall"
)

// PinState 是 CF_PIN_STATE（cfapi.h:891-897）。
type PinState int32

const (
	// PinStateUnspecified：平台自行决定内容在不在本地。
	PinStateUnspecified PinState = 0
	// PinStatePinned：内容必须留在本地，且**平台会拒绝之后对它的 dehydrate 请求**。
	PinStatePinned PinState = 1
	// PinStateUnpinned：平台会通知提供方把内容从本地撤掉。
	PinStateUnpinned PinState = 2
	// PinStateExcluded：该占位符永不与云端同步（只有同步提供方能设）。
	PinStateExcluded PinState = 3
	// PinStateInherit：按父级重新计算（相当于「撤销本地选择」）。
	PinStateInherit PinState = 4
)

// CF_SET_PIN_FLAGS（cfapi.h:899-904）。递归位只对目录句柄有意义。
const (
	SetPinFlagNone               uint32 = 0x00000000
	SetPinFlagRecurse            uint32 = 0x00000001
	SetPinFlagRecurseOnly        uint32 = 0x00000002
	SetPinFlagRecurseStopOnError uint32 = 0x00000004
)

// writeDaclAccess 是 WRITE_DAC (0x00040000)。
//
// 为什么不是 READ_DATA：`CfSetPinState` 要求「READ_DATA 或 WRITE_DAC」二者之一，
// 而带 READ_DATA 打开占位符**会直接触发水合**（本包实测，见
// handle_probe_windows_test.go 与 FileReadAttributes 的注释）——
// 「释放空间」的操作把文件先下载回来，是彻底的自打脸。WRITE_DAC 不请求数据访问权，
// 内核因此不会水合。
const writeDaclAccess = 0x00040000

// cfEOF 是 SDK 里表示「到文件尾」的 Length 取值（-1）。
const cfEOF = ^int64(0)

// openStateHandle 为状态位操作打开句柄：属性访问权 + WRITE_DAC，不含数据访问权。
//
// FILE_FLAG_BACKUP_SEMANTICS 让目录句柄也能打开——pin 整个目录要靠它。
func openStateHandle(absPath string) (syscall.Handle, error) {
	pathPtr, err := syscall.UTF16PtrFromString(absPath)
	if err != nil {
		return 0, fmt.Errorf("cfapi: 路径转换失败: %w", err)
	}
	h, err := syscall.CreateFile(
		pathPtr,
		FileReadAttributes|writeDaclAccess,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil,
		syscall.OPEN_EXISTING,
		syscall.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return 0, fmt.Errorf("cfapi: 打开 %s 失败: %w", absPath, err)
	}
	return h, nil
}

// SetPinState 设置占位符（或目录）的 pin 状态。
//
// flags 传 SetPinFlag* 的组合；对目录用 Recurse 才能把整棵子树钉住。
// 同步调用（不传 Overlapped），与 SDK 的 `LPOVERLAPPED = NULL` 语义一致。
func SetPinState(absPath string, state PinState, flags uint32) error {
	p, err := proc("CfSetPinState")
	if err != nil {
		return err
	}
	handle, err := openStateHandle(absPath)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(handle)

	// CfSetPinState(HANDLE, CF_PIN_STATE, CF_SET_PIN_FLAGS, LPOVERLAPPED)  (cfapi.h:908-914)
	r1, _, _ := p.Call(
		uintptr(handle),
		uintptr(int32(state)),
		uintptr(flags),
		0,
	)
	if err := checkHResult(r1, "CfSetPinState"); err != nil {
		return fmt.Errorf("cfapi: %s 设置 %s pin=%d 失败: %w", "CfSetPinState", absPath, state, err)
	}
	return nil
}

// Dehydrate 把已水合的内容从本地撤掉、退回空壳（「释放空间」的内核动作）。
//
// 语义边界：钉住（PinStatePinned）的占位符会被平台**拒绝** dehydrate，
// 这正是「始终保留在这台电脑上」生效的判据，本包的测试就是按这个反向断言的。
func Dehydrate(absPath string) error {
	p, err := proc("CfDehydratePlaceholder")
	if err != nil {
		return err
	}
	handle, err := openStateHandle(absPath)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(handle)

	// CfDehydratePlaceholder(HANDLE, LARGE_INTEGER StartingOffset, LARGE_INTEGER Length,
	//                         CF_DEHYDRATE_FLAGS, LPOVERLAPPED)  (cfapi.h:882-889)
	// LARGE_INTEGER 是 8 字节联合体，按 int64 传值即可；区间取「0 → 文件尾」。
	// eof 必须是**运行期变量**：常量 -1 直接转 uintptr 会编译期溢出。
	eof := cfEOF
	r1, _, _ := p.Call(
		uintptr(handle),
		uintptr(0),
		uintptr(eof),
		uintptr(0), // CF_DEHYDRATE_FLAG_NONE：前台语义，调用返回即完成
		0,
	)
	if err := checkHResult(r1, "CfDehydratePlaceholder"); err != nil {
		return fmt.Errorf("cfapi: 释放 %s 空间失败: %w", absPath, err)
	}
	return nil
}

// Hydrate 主动把内容拉到本地（钉住之外让文件「落盘」的另一条路）。
//
// 原型与 Dehydrate 同形：CfHydratePlaceholder(HANDLE, StartingOffset, Length,
// CF_HYDRATE_FLAGS, LPOVERLAPPED)  (cfapi.h:866-873)。
// 目前只有测试与「释放空间后可否再拿回来」这条链路会用到它。
func Hydrate(absPath string) error {
	p, err := proc("CfHydratePlaceholder")
	if err != nil {
		return err
	}
	handle, err := openStateHandle(absPath)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(handle)

	eof := cfEOF // 同 Dehydrate：-1 不能作常量直接转 uintptr
	r1, _, _ := p.Call(
		uintptr(handle),
		uintptr(0),
		uintptr(eof),
		uintptr(0), // CF_HYDRATE_FLAG_NONE
		0,
	)
	if err := checkHResult(r1, "CfHydratePlaceholder"); err != nil {
		return fmt.Errorf("cfapi: 预取 %s 内容失败: %w", absPath, err)
	}
	return nil
}
