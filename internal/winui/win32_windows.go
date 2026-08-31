//go:build windows

package winui

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// 遵循项目既有风格：LazyDLL + NewProc + .Call（见 internal/fsutil/fsutil_windows.go）。
// 本文件是项目首次引入 user32.dll，仅覆盖托盘浮窗所需的最小 API 面。
var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procRegisterClassExW   = user32.NewProc("RegisterClassExW")
	procCreateWindowExW    = user32.NewProc("CreateWindowExW")
	procDestroyWindow      = user32.NewProc("DestroyWindow")
	procDefWindowProcW     = user32.NewProc("DefWindowProcW")
	procGetMessageW        = user32.NewProc("GetMessageW")
	procTranslateMessage   = user32.NewProc("TranslateMessage")
	procDispatchMessageW   = user32.NewProc("DispatchMessageW")
	procPostQuitMessage    = user32.NewProc("PostQuitMessage")
	procPostMessageW       = user32.NewProc("PostMessageW")
	procShowWindow         = user32.NewProc("ShowWindow")
	procSetWindowPos       = user32.NewProc("SetWindowPos")
	procSetForegroundWin   = user32.NewProc("SetForegroundWindow")
	procGetCursorPos       = user32.NewProc("GetCursorPos")
	procMonitorFromRect    = user32.NewProc("MonitorFromRect")
	procGetMonitorInfoW    = user32.NewProc("GetMonitorInfoW")
	procSystemParametersIW = user32.NewProc("SystemParametersInfoW")
	procRegisterWindowMsgW = user32.NewProc("RegisterWindowMessageW")
	procLoadImageW         = user32.NewProc("LoadImageW")
	procDestroyIcon        = user32.NewProc("DestroyIcon")
	procCreatePopupMenu    = user32.NewProc("CreatePopupMenu")
	procAppendMenuW        = user32.NewProc("AppendMenuW")
	procDestroyMenu        = user32.NewProc("DestroyMenu")
	procTrackPopupMenu     = user32.NewProc("TrackPopupMenu")
	procGetClientRect      = user32.NewProc("GetClientRect")
	procGetWindowRect      = user32.NewProc("GetWindowRect")
	procReleaseCapture     = user32.NewProc("ReleaseCapture")
	procGetWindowLongW     = user32.NewProc("GetWindowLongPtrW")
	procSetWindowLongW     = user32.NewProc("SetWindowLongPtrW")
	procSendMessageW       = user32.NewProc("SendMessageW")
	procSetTimer           = user32.NewProc("SetTimer")
	procKillTimer          = user32.NewProc("KillTimer")
	procGetDpiForWindow    = user32.NewProc("GetDpiForWindow")
	procSetProcessDPIAware = user32.NewProc("SetProcessDPIAware")

	procShellNotifyIconW      = shell32.NewProc("Shell_NotifyIconW")
	procShellNotifyIconGetRct = shell32.NewProc("Shell_NotifyIconGetRect")

	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
)

// 窗口与消息常量。
const (
	WSExToolWindow = 0x00000080 // 不在任务栏和 Alt-Tab 中出现
	WSExTopMost    = 0x00000008
	WSExNoActivate = 0x08000000 // 点击不抢焦点，浮窗弹出时不打断用户输入
	WSPopup        = 0x80000000
	WSClipChildren = 0x02000000

	SWHide         = 0
	SWShowNoActive = 4

	SWPNoSize     = 0x0001
	SWPNoMove     = 0x0002
	SWPNoActivate = 0x0010
	SWPShowWindow = 0x0040
	SWPNoZOrder   = 0x0004

	HWNDTopMost = ^uintptr(0) // (HWND)-1

	WMDestroy     = 0x0002
	WMClose       = 0x0010
	WMTimer       = 0x0113
	WMCommand     = 0x0111
	WMApp         = 0x8000
	WMContextMenu = 0x007B

	// gwlExStyle 是 GetWindowLong/SetWindowLong 取扩展样式的索引（GWL_EXSTYLE）。
	gwlExStyle = ^uintptr(19) // -20

	// WMNCLButtonDown + HTCaption：把「按住某处拖动」交给系统自己的拖窗循环。
	// 无边框窗口靠这一对实现拖动，好过自己 MoveWindow——系统那套自带贴边与多屏处理。
	WMNCLButtonDown = 0x00A1
	HTCaption       = 2

	// WMTrayCallback 是通知区域图标的回调消息号，取值须落在 WM_APP 之上。
	WMTrayCallback = WMApp + 1

	// NOTIFYICON_VERSION_4 下的通知码。NINPopupOpen/Close 正是「悬停即用富浮窗
	// 替代文本 tooltip」的语义，也是本功能得以成立的关键。
	NINSelect     = 0x0400
	NINPopupOpen  = 0x0406
	NINPopupClose = 0x0407

	// Shell_NotifyIcon 操作与标志。
	NIMAdd        = 0x0000
	NIMModify     = 0x0001
	NIMDelete     = 0x0002
	NIMSetVersion = 0x0004

	NIFMessage = 0x0001
	NIFIcon    = 0x0002
	NIFTip     = 0x0004
	NIFGUID    = 0x0020 // 用稳定 GUID 标识图标，令系统跨重启记住其显示/隐藏偏好
	NIFShowTip = 0x0080 // v4 下不设此位则不显示标准 tooltip，由浮窗接管

	NotifyIconVersion4 = 4

	MFString    = 0x0000
	MFSeparator = 0x0800
	MFGrayed    = 0x0001

	TPMLeftAlign   = 0x0000
	TPMBottomAlign = 0x0020
	TPMRightButton = 0x0002
	TPMReturnCmd   = 0x0100

	ImageIcon      = 1
	LRLoadFromFile = 0x0010
	LRDefaultSize  = 0x0040

	MonitorDefaultToNearest = 0x0002

	// SPIGetWorkArea 取主显示器工作区（不含任务栏）。
	SPIGetWorkArea = 0x0030

	CSHRedraw = 0x0002
	CSVRedraw = 0x0001
)

// Point 对应 Win32 POINT。
type Point struct {
	X int32
	Y int32
}

// Msg 对应 Win32 MSG。
type Msg struct {
	HWnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      Point
	Private uint32
}

// WndClassEx 对应 Win32 WNDCLASSEXW。
type WndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   windows.Handle
	Icon       windows.Handle
	Cursor     windows.Handle
	Background windows.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSm     windows.Handle
}

// MonitorInfo 对应 Win32 MONITORINFO。
type MonitorInfo struct {
	Size    uint32
	Monitor Rect
	Work    Rect
	Flags   uint32
}

// NotifyIconData 对应 Vista 及以上的 NOTIFYICONDATAW。
// 字段顺序与填充必须与 C 结构完全一致：Shell_NotifyIcon 会用 CbSize 校验版本，
// 大小不匹配会直接失败。amd64 下预期为 976 字节。
type NotifyIconData struct {
	CbSize           uint32
	HWnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            windows.Handle
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         windows.GUID
	HBalloonIcon     windows.Handle
}

// NotifyIconIdentifier 对应 Win32 NOTIFYICONIDENTIFIER，供 Shell_NotifyIconGetRect 使用。
type NotifyIconIdentifier struct {
	CbSize   uint32
	HWnd     uintptr
	UID      uint32
	GuidItem windows.GUID
}

// GetModuleHandle 返回当前进程的模块句柄，用于注册窗口类。
func GetModuleHandle() windows.Handle {
	h, _, _ := procGetModuleHandleW.Call(0)
	return windows.Handle(h)
}

// RegisterWindowMessage 注册一个系统级消息名并返回其消息号。
// 用于监听 "TaskbarCreated"——explorer.exe 重启时会向所有顶层窗口广播它，
// 应用需借此重新添加自己的通知区域图标，否则 explorer 重启后图标永久消失。
func RegisterWindowMessage(name string) uint32 {
	p, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0
	}
	msg, _, _ := procRegisterWindowMsgW.Call(uintptr(unsafe.Pointer(p)))
	return uint32(msg)
}

// InitCOMForUIThread 在当前线程以单线程套间（STA）初始化 COM。
//
// WebView2 是 COM 组件，创建环境前必须先在该线程完成 COM 初始化，否则会得到
// "CoInitialize has not been called"。带消息循环的 UI 线程应使用 STA。
//
// 调用方必须已经 runtime.LockOSThread：COM 初始化绑定的是线程而非 goroutine。
// 若该线程已按其他模式初始化过，返回 RPC_E_CHANGED_MODE，此时沿用既有模式即可，
// 不视为错误。
func InitCOMForUIThread() error {
	err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED)
	if err == nil {
		return nil
	}
	if errno, ok := err.(syscall.Errno); ok && windows.Handle(errno) == windows.RPC_E_CHANGED_MODE {
		return nil
	}
	return fmt.Errorf("CoInitializeEx: %w", err)
}

// RegisterClass 注册窗口类，返回失败时携带系统错误。
func RegisterClass(wc *WndClassEx) error {
	wc.Size = uint32(unsafe.Sizeof(*wc))
	atom, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(wc)))
	if atom == 0 {
		return fmt.Errorf("RegisterClassExW: %w", err)
	}
	return nil
}

// CreateWindow 创建窗口。width/height 为 0 时创建零尺寸窗口（message-only 场景）。
func CreateWindow(exStyle uint32, className, title string, style uint32, x, y, w, h int32, parent uintptr) (uintptr, error) {
	cn, err := syscall.UTF16PtrFromString(className)
	if err != nil {
		return 0, err
	}
	tt, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return 0, err
	}
	hwnd, _, callErr := procCreateWindowExW.Call(
		uintptr(exStyle),
		uintptr(unsafe.Pointer(cn)),
		uintptr(unsafe.Pointer(tt)),
		uintptr(style),
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		parent, 0, uintptr(GetModuleHandle()), 0,
	)
	if hwnd == 0 {
		return 0, fmt.Errorf("CreateWindowExW: %w", callErr)
	}
	return hwnd, nil
}

// DestroyWindow 销毁窗口。
func DestroyWindow(hwnd uintptr) { procDestroyWindow.Call(hwnd) }

// DefWindowProc 是窗口过程的默认处理。
func DefWindowProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return r
}

// RunMessageLoop 运行消息循环直至收到 WM_QUIT。
// 调用方必须先 runtime.LockOSThread，窗口与消息循环需在同一线程。
func RunMessageLoop() {
	var msg Msg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		// GetMessage 返回 0 表示 WM_QUIT，-1 表示错误，两者都应结束循环。
		if int32(r) <= 0 {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

// PostQuitMessage 请求消息循环退出。
func PostQuitMessage(code int32) { procPostQuitMessage.Call(uintptr(code)) }

// PostMessage 向指定窗口投递消息，可跨线程调用。
// 托盘状态更新等来自其他 goroutine 的请求都经由它回到消息循环所在线程。
func PostMessage(hwnd uintptr, msg uint32, wParam, lParam uintptr) {
	procPostMessageW.Call(hwnd, uintptr(msg), wParam, lParam)
}

// ShowWindowNoActivate 显示窗口但不激活，避免浮窗抢走当前前台窗口的焦点。
func ShowWindowNoActivate(hwnd uintptr) { procShowWindow.Call(hwnd, SWShowNoActive) }

// HideWindow 隐藏窗口。
func HideWindow(hwnd uintptr) { procShowWindow.Call(hwnd, SWHide) }

// MoveWindowTopMost 把窗口移动到指定位置并置顶，不改变激活状态。
func MoveWindowTopMost(hwnd uintptr, x, y, w, h int32) {
	procSetWindowPos.Call(hwnd, HWNDTopMost,
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		SWPNoActivate|SWPShowWindow)
}

// SetForegroundWindow 把窗口提到前台。
func SetForegroundWindow(hwnd uintptr) { procSetForegroundWin.Call(hwnd) }

// GetCursorPos 返回光标的屏幕坐标。
func GetCursorPos() Point {
	var p Point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	return p
}

// GetWindowRect 返回窗口的屏幕矩形。
func GetWindowRect(hwnd uintptr) Rect {
	var r Rect
	procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	return r
}

// GetClientRect 返回窗口客户区矩形（左上角为 0,0）。
func GetClientRect(hwnd uintptr) Rect {
	var r Rect
	procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	return r
}

// WorkAreaForRect 返回与指定矩形相交最多的显示器的工作区（不含任务栏）。
// 多显示器下必须按矩形取，不能用主屏工作区代替。
//
// 这里用 MonitorFromRect 而不是 MonitorFromPoint：后者的第一个参数是按值传递的
// POINT（8 字节），在 x64 上会被打包进单个寄存器，用 LazyProc.Call 传两个
// uintptr 会让 dwFlags 收到 Y 坐标，函数静默返回 NULL。收指针的版本没有这个歧义。
func WorkAreaForRect(r Rect) Rect {
	mon, _, _ := procMonitorFromRect.Call(uintptr(unsafe.Pointer(&r)), MonitorDefaultToNearest)
	if mon == 0 {
		return Rect{}
	}
	info := MonitorInfo{Size: uint32(unsafe.Sizeof(MonitorInfo{}))}
	if ok, _, _ := procGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&info))); ok == 0 {
		return Rect{}
	}
	return info.Work
}

// GetPrimaryWorkArea 返回主显示器的工作区（不含任务栏）。
// 启动时浮窗要固定在右下角，此时不依赖任何窗口或图标位置，用主屏工作区即可。
func GetPrimaryWorkArea() Rect {
	var r Rect
	ok, _, _ := procSystemParametersIW.Call(SPIGetWorkArea, 0, uintptr(unsafe.Pointer(&r)), 0)
	if ok == 0 {
		return Rect{}
	}
	return r
}

// DpiForWindow 返回窗口所在显示器的 DPI，取不到时回退 96（100% 缩放）。
// GetDpiForWindow 需要 Windows 10 1607 及以上。
func DpiForWindow(hwnd uintptr) int32 {
	if err := procGetDpiForWindow.Find(); err != nil {
		return 96
	}
	dpi, _, _ := procGetDpiForWindow.Call(hwnd)
	if dpi == 0 {
		return 96
	}
	return int32(dpi)
}

// LoadIconFromFile 从 .ico 文件加载图标。
// Windows 的 SetIcon 只接受 ICO 字节，PNG 无效，因此调用方须提供 .ico。
func LoadIconFromFile(path string) (windows.Handle, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	h, _, callErr := procLoadImageW.Call(0, uintptr(unsafe.Pointer(p)),
		ImageIcon, 0, 0, LRLoadFromFile|LRDefaultSize)
	if h == 0 {
		return 0, fmt.Errorf("LoadImageW(%s): %w", path, callErr)
	}
	return windows.Handle(h), nil
}

// DestroyIcon 释放图标句柄。
func DestroyIcon(h windows.Handle) { procDestroyIcon.Call(uintptr(h)) }

// --- 通知区域图标 ---

// NewNotifyIconData 构造一条通知区域图标数据，自动填好 CbSize 与提示文字。
func NewNotifyIconData(hwnd uintptr, id uint32, tip string) *NotifyIconData {
	data := &NotifyIconData{
		CbSize:           uint32(unsafe.Sizeof(NotifyIconData{})),
		HWnd:             hwnd,
		UID:              id,
		UCallbackMessage: WMTrayCallback,
	}
	copyUTF16(data.SzTip[:], tip)
	return data
}

// ShellNotifyIcon 调用 Shell_NotifyIconW。
func ShellNotifyIcon(message uint32, data *NotifyIconData) error {
	r, _, err := procShellNotifyIconW.Call(uintptr(message), uintptr(unsafe.Pointer(data)))
	if r == 0 {
		return fmt.Errorf("Shell_NotifyIconW(msg=%d): %w", message, err)
	}
	return nil
}

// SetNotifyIconVersion4 把图标切换到 NOTIFYICON_VERSION_4。
// 只有切换成功后系统才会投递 NIN_POPUPOPEN/NIN_POPUPCLOSE；失败时悬停将退化为
// 标准 tooltip，浮窗不会被触发。
func SetNotifyIconVersion4(data *NotifyIconData) error {
	data.UVersion = NotifyIconVersion4
	return ShellNotifyIcon(NIMSetVersion, data)
}

// NotifyIconRect 返回通知区域图标的屏幕矩形。
// 优先使用它而非推算任务栏位置：它天然适配多显示器、任务栏停靠边缘和图标折叠。
//
// 图标以 GUID 注册时必须按 GUID 查询（此时传入的 guid 非零）；否则按 hwnd+uid 查。
func NotifyIconRect(hwnd uintptr, id uint32, guid windows.GUID) (Rect, error) {
	if err := procShellNotifyIconGetRct.Find(); err != nil {
		return Rect{}, err
	}
	ident := NotifyIconIdentifier{
		CbSize:   uint32(unsafe.Sizeof(NotifyIconIdentifier{})),
		HWnd:     hwnd,
		UID:      id,
		GuidItem: guid,
	}
	var r Rect
	hr, _, _ := procShellNotifyIconGetRct.Call(
		uintptr(unsafe.Pointer(&ident)), uintptr(unsafe.Pointer(&r)))
	// 返回 HRESULT：S_OK 为 0，非 0 即失败（图标被折叠进溢出区时可能返回 S_FALSE）。
	if hr != 0 {
		return Rect{}, fmt.Errorf("Shell_NotifyIconGetRect: HRESULT 0x%X", hr)
	}
	return r, nil
}

// --- 右键菜单 ---

// Menu 是弹出菜单的轻量封装。
type Menu struct{ handle uintptr }

// NewMenu 创建一个空的弹出菜单。
func NewMenu() (*Menu, error) {
	h, _, err := procCreatePopupMenu.Call()
	if h == 0 {
		return nil, fmt.Errorf("CreatePopupMenu: %w", err)
	}
	return &Menu{handle: h}, nil
}

// AddItem 追加一个菜单项；enabled 为 false 时置灰（用于只读的状态行）。
func (m *Menu) AddItem(id uint32, text string, enabled bool) error {
	t, err := syscall.UTF16PtrFromString(text)
	if err != nil {
		return err
	}
	flags := uintptr(MFString)
	if !enabled {
		flags |= MFGrayed
	}
	r, _, callErr := procAppendMenuW.Call(m.handle, flags, uintptr(id), uintptr(unsafe.Pointer(t)))
	if r == 0 {
		return fmt.Errorf("AppendMenuW: %w", callErr)
	}
	return nil
}

// AddSeparator 追加一条分隔线。
func (m *Menu) AddSeparator() {
	procAppendMenuW.Call(m.handle, MFSeparator, 0, 0)
}

// Track 在指定屏幕坐标弹出菜单并返回被选中的命令 ID，未选择时返回 0。
// 调用前需把 owner 提到前台，否则菜单在点击别处时不会自动关闭（Win32 已知行为）。
func (m *Menu) Track(owner uintptr, x, y int32) uint32 {
	SetForegroundWindow(owner)
	cmd, _, _ := procTrackPopupMenu.Call(m.handle,
		TPMLeftAlign|TPMBottomAlign|TPMRightButton|TPMReturnCmd,
		uintptr(x), uintptr(y), 0, owner, 0)
	return uint32(cmd)
}

// Destroy 释放菜单句柄。
func (m *Menu) Destroy() {
	if m.handle != 0 {
		procDestroyMenu.Call(m.handle)
		m.handle = 0
	}
}

// --- 定时器 ---

// SetTimer 设置窗口定时器，用于悬停收起的延迟判定。
// SetNoActivate 开关窗口的 WS_EX_NOACTIVATE。
//
// 该标志让窗口无法被激活（点击不抢焦点），但代价是**收不到拖放**：
// Windows 的拖放循环不会把不可激活的窗口当作放置目标，HTML5 的 drop 事件与
// WM_DROPFILES 都不会到达。本机对照可见，能正常接收拖放的桌面挂件都不带此标志。
//
// 因此按状态切换：悬停弹出时开启（不打断用户输入），固定后关闭（换取可拖入）。
func SetNoActivate(hwnd uintptr, enable bool) {
	current, _, _ := procGetWindowLongW.Call(hwnd, gwlExStyle)
	updated := current | WSExNoActivate
	if !enable {
		updated = current &^ WSExNoActivate
	}
	if updated == current {
		return
	}
	procSetWindowLongW.Call(hwnd, gwlExStyle, updated)
}

// BeginWindowDrag 把当前的鼠标按下转交给系统的拖窗循环，实现无边框窗口的拖动。
//
// 先 ReleaseCapture 再发 WM_NCLBUTTONDOWN(HTCAPTION)：等于告诉系统「用户正按住标题栏」，
// 之后的移动、松手、贴边全由系统处理。自己监听鼠标移动再 MoveWindow 也能动，但会丢掉
// 贴边吸附与多显示器 DPI 处理，且拖动时容易掉帧。
//
// 必须在收到鼠标按下的那一刻调用——系统拖窗循环依赖当前的按键状态。
func BeginWindowDrag(hwnd uintptr) {
	procReleaseCapture.Call()
	procSendMessageW.Call(hwnd, WMNCLButtonDown, HTCaption, 0)
}

func SetTimer(hwnd uintptr, id uintptr, ms uint32) {
	procSetTimer.Call(hwnd, id, uintptr(ms), 0)
}

// KillTimer 取消窗口定时器。
func KillTimer(hwnd uintptr, id uintptr) {
	procKillTimer.Call(hwnd, id)
}

// copyUTF16 把字符串写入定长 UTF-16 缓冲区，超长时截断并保留结尾的 NUL。
func copyUTF16(dst []uint16, s string) {
	src, err := syscall.UTF16FromString(s)
	if err != nil {
		return
	}
	if len(src) > len(dst) {
		src = src[:len(dst)]
		src[len(src)-1] = 0
	}
	copy(dst, src)
}
