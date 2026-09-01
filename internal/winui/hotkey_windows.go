//go:build windows

package winui

import (
	"fmt"
	"unsafe"
)

// 快捷面板所需的补充 API 面：全局热键、前台窗口查询与合成按键。
// 与 win32_windows.go 共用同一组 dll 句柄与 LazyDLL 风格。

var (
	procRegisterHotKey           = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey         = user32.NewProc("UnregisterHotKey")
	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	procGetWindowThreadProcessID = user32.NewProc("GetWindowThreadProcessId")
	procKeybdEvent               = user32.NewProc("keybd_event")
	procMoveWindow               = user32.NewProc("MoveWindow")
)

// 热键与按键常量。
const (
	// WMHotkey 是 RegisterHotKey 注册的热键被按下时投递到注册线程的消息。
	WMHotkey = 0x0312
	// WMActivate 通知窗口激活状态变化；wParam 低位 = WAInactive 表示失去激活。
	WMActivate = 0x0006
	WAInactive = 0

	// RegisterHotKey 的修饰符（fsModifiers）。
	ModAlt     = 0x0001
	ModControl = 0x0002
	ModShift   = 0x0004
	ModWin     = 0x0008
	// ModNoRepeat 按住不放只触发一次。
	ModNoRepeat = 0x4000

	// SendCtrlV 用到的虚拟键与标志。
	VKControl      = 0x11
	VKV            = 0x56
	KEYEVENTFKeyup = 0x0002

	// SWShow 是 ShowWindow 的「激活并显示」命令（与 SWShowNoActivate 相对）。
	SWShow = 1
)

// RegisterHotKey 注册全局热键。热键与线程绑定：WMHotkey 只投递到调用线程，
// 调用方须在拥有消息循环的线程上注册（快捷面板线程）。失败返回错误（如热键被占用）。
func RegisterHotKey(hwnd uintptr, id int32, mods, vk uint32) error {
	r1, _, err := procRegisterHotKey.Call(hwnd, uintptr(id), uintptr(mods), uintptr(vk))
	if r1 == 0 {
		return fmt.Errorf("RegisterHotKey: %w", err)
	}
	return nil
}

// UnregisterHotKey 注销全局热键（进程退出时系统也会自动回收）。
func UnregisterHotKey(hwnd uintptr, id int32) {
	procUnregisterHotKey.Call(hwnd, uintptr(id))
}

// GetForegroundWindow 返回当前前台窗口（可能为 0）。
func GetForegroundWindow() uintptr {
	r1, _, _ := procGetForegroundWindow.Call()
	return r1
}

// GetWindowThreadProcessId 返回窗口所属线程 ID 与进程 ID。
func GetWindowThreadProcessId(hwnd uintptr) (threadID uint32, pid uint32) {
	r1, _, _ := procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if r1 == 0 {
		return 0, 0
	}
	return uint32(r1), pid
}

// ShowWindowActivate 以激活方式显示窗口（SW_SHOW）——快捷面板弹出时要立刻
// 接管键盘焦点（搜索框即打即搜），与悬浮窗的 SWShowNoActivate 相反。
func ShowWindowActivate(hwnd uintptr) {
	procShowWindow.Call(hwnd, SWShow)
}

// MoveWindow 改变窗口位置与尺寸（不改变显示状态——隐藏窗口移动不闪现，
// 显示中的窗口保持可见）。MoveWindowTopMost 只带 SWPShowWindow，不适合隐藏态。
func MoveWindow(hwnd uintptr, x, y, w, h int32) bool {
	r1, _, _ := procMoveWindow.Call(hwnd, uintptr(x), uintptr(y), uintptr(w), uintptr(h), 1)
	return r1 != 0
}

// SendCtrlV 合成一次 Ctrl+V 按键（keybd_event 注入到系统输入流）。
// 用于面板选中条目后的「自动粘贴」：写入剪切板 + 焦点切回 + 合成粘贴。
// macOS 的等价能力（CGEvent Cmd+V）需要辅助功能授权，行为差异见 panel_darwin.go。
func SendCtrlV() {
	procKeybdEvent.Call(VKControl, 0, 0, 0)
	procKeybdEvent.Call(VKV, 0, 0, 0)
	procKeybdEvent.Call(VKV, 0, KEYEVENTFKeyup, 0)
	procKeybdEvent.Call(VKControl, 0, KEYEVENTFKeyup, 0)
}
