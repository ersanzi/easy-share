//go:build windows

package main

import (
	_ "embed"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"easyshare/internal/winui"

	"golang.org/x/sys/windows"
)

const (
	trayWindowClass = "EasyShareTrayHost"
	trayIconID      = 1

	// 右键菜单命令 ID。
	cmdOpenMain = 1001
	cmdStatus   = 1002
	cmdQuit     = 1003

	// trayMsgUpdateStatus 由其他 goroutine 投递，把状态更新拉回消息循环线程。
	trayMsgUpdateStatus = winui.WMApp + 100
)

//go:embed build/windows/icon.ico
var trayIcon []byte

// easyShareTrayGUID 是 EasyShare 托盘图标的稳定标识。固定不变，令 Windows 跨重启
// 记住该图标的显示/隐藏偏好。切勿随意更换，改了等于变成一个「新图标」，用户此前的
// 常显区设置会丢失。
var easyShareTrayGUID = windows.GUID{
	Data1: 0xE45A9B3C,
	Data2: 0x7D21,
	Data3: 0x4F6E,
	Data4: [8]byte{0xA8, 0xB2, 0x3C, 0x9D, 0x1E, 0x5F, 0x7A, 0x80},
}

// trayHost 是 Windows 通知区域图标的宿主。
//
// 这里不使用 getlantern/systray：该库的窗口过程只处理 WM_LBUTTONUP 与
// WM_RBUTTONUP，不投递任何悬停事件，也无法通过配置获得——它唯一与悬停相关的
// 能力是 SetTooltip，走系统标准 tooltip 且无回调。
//
// 改为自建通知图标并声明 NOTIFYICON_VERSION_4，系统才会在悬停时投递
// NIN_POPUPOPEN / NIN_POPUPCLOSE，其语义正是「用富浮窗替代文本 tooltip」。
// 详见 docs/iterations/2026-08-28-tray-hover-widget.md 技术决策 1。
type trayHost struct {
	app      *App
	hwnd     uintptr
	iconData *winui.NotifyIconData
	hIcon    windows.Handle
	popup    *hoverPopup

	statusMu   sync.Mutex
	statusText string

	iconFile string // 从 embed 落盘的临时 .ico，退出时清理

	// taskbarCreatedMsg 是 explorer 重启时广播的 "TaskbarCreated" 消息号，
	// 收到它需要重新添加托盘图标。
	taskbarCreatedMsg uint32
}

var (
	trayClassOnce sync.Once
	trayClassErr  error
	trayHostMu    sync.RWMutex
	trayHosts     = map[uintptr]*trayHost{}
)

// startTray 启动 Windows 通知区域图标。
// 签名与既有实现保持一致，main.go 与 app.go 不需要改动。
func startTray(app *App) {
	app.trayOnce.Do(func() {
		go func() {
			// 窗口与消息循环必须在同一线程，且该线程不能被 Go 调度器换走。
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()

			// WebView2 是 COM 组件，必须在本线程先初始化 COM，
			// 否则浮窗创建时报 "CoInitialize has not been called"。
			// 失败不影响托盘本身，仅使浮窗降级。
			if err := winui.InitCOMForUIThread(); err != nil {
				app.logger.Printf("tray: COM 初始化失败，悬停浮窗将不可用: %v", err)
			}

			host, err := newTrayHost(app)
			if err != nil {
				app.logger.Printf("system tray start failed: %v", err)
				return
			}
			defer host.destroy()

			app.trayReady()
			go host.watchStatus()
			go host.watchUser()

			winui.RunMessageLoop()
		}()
	})
}

// newTrayHost 创建消息宿主窗口、注册通知图标，并尝试创建悬停浮窗。
func newTrayHost(app *App) (*trayHost, error) {
	if err := ensureTrayClass(); err != nil {
		return nil, err
	}

	host := &trayHost{app: app, statusText: "启动中…"}
	// explorer 重启会广播此消息，届时需要重新添加托盘图标。
	host.taskbarCreatedMsg = winui.RegisterWindowMessage("TaskbarCreated")

	hwnd, err := winui.CreateWindow(0, trayWindowClass, "EasyShare", 0, 0, 0, 0, 0, 0)
	if err != nil {
		return nil, err
	}
	host.hwnd = hwnd

	trayHostMu.Lock()
	trayHosts[hwnd] = host
	trayHostMu.Unlock()

	if err := host.addIcon(); err != nil {
		host.destroy()
		return nil, err
	}

	// 浮窗创建失败不影响托盘本身：降级为「仅菜单」的托盘，悬停回落到系统 tooltip。
	// startPinned=false：启动只常驻托盘图标（像腾讯/WPS），不自动弹浮窗；
	// 悬停图标才弹出，点固定才常驻。
	// 拖入回调要能回写状态到浮窗自己，而浮窗此刻还没构造出来，
	// 故先声明再闭包引用——构造完成后 host.popup 已就位。
	onDropped := func(paths []string) {
		app.uploadDroppedToSpace(paths, func(title, hint, kind string) {
			if host.popup != nil {
				host.popup.SetDropStatus(title, hint, kind)
			}
		})
	}
	onOpenSpace := func() {
		if err := app.OpenCurrentSpace(); err != nil && host.popup != nil {
			// 打不开要说清楚，否则用户点了没反应会以为按钮坏了
			host.popup.SetDropStatus("无法打开", err.Error(), "err")
		}
	}
	popup, popupErr := newHoverPopup(winui.DpiForWindow(hwnd), false,
		app.showWindow, app.SetDropSpace, onDropped, onOpenSpace, app.logger.Printf)
	if popupErr != nil {
		app.logger.Printf("hover popup unavailable, tray degrades to menu only: %v", popupErr)
	} else {
		host.popup = popup
	}
	return host, nil
}

// addIcon 加载图标、构造图标数据并首次注册到通知区域。
func (h *trayHost) addIcon() error {
	iconPath, err := h.materializeIcon()
	if err != nil {
		return err
	}
	h.iconFile = iconPath

	// Windows 的图标 API 只接受 ICO 字节，PNG 无效（见 AGENTS.md 关键坑）。
	hIcon, err := winui.LoadIconFromFile(iconPath)
	if err != nil {
		return err
	}
	h.hIcon = hIcon

	data := winui.NewNotifyIconData(h.hwnd, trayIconID, "EasyShare - 局域网文件传输")
	data.HIcon = hIcon
	// 用稳定 GUID 标识托盘图标：Windows 11 默认把新图标丢进溢出区，且应用无法用代码
	// 强制进入常显区（这是系统限制）。但带稳定 GUID 后，系统会按 GUID 记住用户的
	// 显示/隐藏与摆放偏好，跨重启和重新构建都保持——用户把图标提升到常显区一次即可长期生效。
	// GUID 与 exe 路径绑定，开发期 exe 固定在 build/bin 下，路径稳定。
	data.GuidItem = easyShareTrayGUID
	// 不设 NIF_SHOWTIP：v4 下省略它表示不显示标准 tooltip，把悬停让给浮窗。
	data.UFlags = winui.NIFMessage | winui.NIFIcon | winui.NIFTip | winui.NIFGUID
	h.iconData = data

	return h.registerIcon()
}

// registerIcon 把已构造好的图标数据注册到通知区域，可重复调用。
// 首次注册与 explorer 重启后重注册都走这里。
func (h *trayHost) registerIcon() error {
	if h.iconData == nil {
		return nil
	}
	if err := winui.ShellNotifyIcon(winui.NIMAdd, h.iconData); err != nil {
		// 上次异常退出或 explorer 重启可能残留同 GUID 的注册，导致 NIM_ADD 失败。删掉再加一次。
		_ = winui.ShellNotifyIcon(winui.NIMDelete, h.iconData)
		if err2 := winui.ShellNotifyIcon(winui.NIMAdd, h.iconData); err2 != nil {
			return err2
		}
	}
	// 必须在 NIM_ADD 之后：只有切到 v4 系统才会投递 NIN_POPUPOPEN/CLOSE。
	// 失败时图标仍可用，只是悬停退化为无事件。
	if err := winui.SetNotifyIconVersion4(h.iconData); err != nil {
		h.app.logger.Printf("tray: 切换 NOTIFYICON_VERSION_4 失败，悬停浮窗不可用: %v", err)
	}
	return nil
}

// materializeIcon 把内嵌的 .ico 落盘，供 LoadImageW 读取。
func (h *trayHost) materializeIcon() (string, error) {
	dir, err := os.MkdirTemp("", "easyshare-tray-")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "icon.ico")
	if err := os.WriteFile(path, trayIcon, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// watchStatus 把来自其他 goroutine 的状态更新转成窗口消息。
// app.trayStatusCh 由 app.updateTrayStatus 写入，不能直接在该 goroutine 里碰窗口。
func (h *trayHost) watchStatus() {
	for status := range h.app.trayStatusCh {
		h.statusMu.Lock()
		h.statusText = status
		h.statusMu.Unlock()
		winui.PostMessage(h.hwnd, trayMsgUpdateStatus, 0, 0)
	}
}

// watchUser 把登录态变化推给悬浮窗（登录/登出时头像与空间切换器跟随账号）。
func (h *trayHost) watchUser() {
	for user := range h.app.trayUserCh {
		if h.popup == nil {
			continue
		}
		h.popup.SetUser(user.LoggedIn, user.NickName, user.UserName)
		if !user.LoggedIn {
			// 退出登录：清空切换器，别留着上个账号的空间
			h.popup.SetSpaces(nil)
			continue
		}
		// 读空间要回控制面，不能卡住这个循环——它还负责头像更新
		go func() {
			spaces, err := h.app.hoverSpaces()
			if err != nil {
				h.app.logger.Printf("hover spaces: %v", err)
				return
			}
			h.popup.SetSpaces(spaces)
		}()
	}
}

// currentStatus 返回最近一次的服务状态文字。
func (h *trayHost) currentStatus() string {
	h.statusMu.Lock()
	defer h.statusMu.Unlock()
	return h.statusText
}

// showMenu 在光标处弹出右键菜单，保持与既有托盘一致的三项内容。
func (h *trayHost) showMenu() {
	menu, err := winui.NewMenu()
	if err != nil {
		h.app.logger.Printf("tray menu: %v", err)
		return
	}
	defer menu.Destroy()

	_ = menu.AddItem(cmdOpenMain, "打开主窗口", true)
	menu.AddSeparator()
	_ = menu.AddItem(cmdStatus, "服务状态："+h.currentStatus(), false)
	menu.AddSeparator()
	_ = menu.AddItem(cmdQuit, "退出 EasyShare", true)

	pos := winui.GetCursorPos()
	switch menu.Track(h.hwnd, pos.X, pos.Y) {
	case cmdOpenMain:
		h.app.showWindow()
	case cmdQuit:
		h.quit()
	}
}

// quit 执行托盘退出：先收起并销毁浮窗与图标，再交给 App 走既有退出流程。
func (h *trayHost) quit() {
	h.destroy()
	h.app.quitFromTray()
	winui.PostQuitMessage(0)
}

// destroy 清理图标、浮窗、窗口与临时文件。重复调用安全。
func (h *trayHost) destroy() {
	if h.iconData != nil {
		// 不删除图标会在通知区域留下无法点击的残影。
		_ = winui.ShellNotifyIcon(winui.NIMDelete, h.iconData)
		h.iconData = nil
	}
	if h.popup != nil {
		// 浮窗运行在自己的线程上，Destroy 只投递消息，由该线程自行清理。
		h.popup.Destroy()
		h.popup = nil
	}
	if h.hIcon != 0 {
		winui.DestroyIcon(h.hIcon)
		h.hIcon = 0
	}
	if h.hwnd != 0 {
		trayHostMu.Lock()
		delete(trayHosts, h.hwnd)
		trayHostMu.Unlock()
		winui.DestroyWindow(h.hwnd)
		h.hwnd = 0
	}
	if h.iconFile != "" {
		_ = os.RemoveAll(filepath.Dir(h.iconFile))
		h.iconFile = ""
	}
}

// showPopup 在图标附近弹出浮窗。取不到图标矩形时（例如图标被折叠进溢出区）
// 静默跳过，不弹到错误位置。
func (h *trayHost) showPopup() {
	if h.popup == nil {
		return
	}
	rect, err := winui.NotifyIconRect(h.hwnd, trayIconID, easyShareTrayGUID)
	if err != nil {
		h.app.logger.Printf("tray: 取图标矩形失败，跳过悬停浮窗: %v", err)
		return
	}
	h.popup.ShowNear(rect)
}

// ensureTrayClass 注册宿主窗口类，进程内只注册一次。
func ensureTrayClass() error {
	trayClassOnce.Do(func() {
		className, err := windows.UTF16PtrFromString(trayWindowClass)
		if err != nil {
			trayClassErr = err
			return
		}
		wc := winui.WndClassEx{
			WndProc:   windows.NewCallback(trayWndProc),
			Instance:  winui.GetModuleHandle(),
			ClassName: className,
		}
		trayClassErr = winui.RegisterClass(&wc)
	})
	return trayClassErr
}

// trayWndProc 是托盘宿主窗口的窗口过程。
//
// 注意 v4 下回调消息的参数语义与旧版本相反：wParam 携带鼠标屏幕坐标，
// lParam 的低 16 位才是事件码、高 16 位是图标 ID。
func trayWndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	trayHostMu.RLock()
	h := trayHosts[hwnd]
	trayHostMu.RUnlock()

	if h == nil {
		return winui.DefWindowProc(hwnd, msg, wParam, lParam)
	}

	// explorer 重启后重新添加图标，否则图标永久消失。
	if h.taskbarCreatedMsg != 0 && msg == h.taskbarCreatedMsg {
		if err := h.registerIcon(); err != nil {
			h.app.logger.Printf("tray: explorer 重启后重注册图标失败: %v", err)
		} else {
			h.app.logger.Printf("tray: 检测到 explorer 重启，已重新添加托盘图标")
		}
		return 0
	}

	switch msg {
	case winui.WMTrayCallback:
		switch uint32(lParam & 0xFFFF) {
		case winui.NINPopupOpen:
			// 悬停到图标：弹出浮窗，替代标准 tooltip。
			h.showPopup()
		case winui.NINPopupClose:
			// 移开图标：延迟收起，给鼠标留出移动到浮窗的时间。
			if h.popup != nil {
				h.popup.scheduleHide()
			}
		case winui.NINSelect:
			// 左键单击（v4 下 NIN_SELECT 取代 WM_LBUTTONUP）。
			// 固定态下浮窗常驻，左键打开主窗口但不收起浮窗。
			if h.popup != nil && !h.popup.IsPinned() {
				h.popup.Hide()
			}
			h.app.showWindow()
		case winui.WMContextMenu:
			// 固定态下弹菜单不收起浮窗。
			if h.popup != nil && !h.popup.IsPinned() {
				h.popup.Hide()
			}
			h.showMenu()
		}
		return 0

	case trayMsgUpdateStatus:
		// 状态文字只在下次弹出菜单时读取，这里无需额外动作。
		return 0

	case winui.WMDestroy:
		winui.PostQuitMessage(0)
		return 0
	}

	return winui.DefWindowProc(hwnd, msg, wParam, lParam)
}
