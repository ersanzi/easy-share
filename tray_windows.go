//go:build windows

package main

import (
	"context"
	_ "embed"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"easyshare/internal/account"
	"easyshare/internal/desktoppet"
	"easyshare/internal/winui"

	"golang.org/x/sys/windows"
)

const (
	trayWindowClass = "EasyShareTrayHost"
	trayIconID      = 1

	// 右键菜单命令 ID。
	cmdOpenMain      = 1001
	cmdStatus        = 1002
	cmdQuit          = 1003
	cmdSettings      = 1004
	cmdPetVisibility = 1005

	// trayMsgUpdateStatus 由其他 goroutine 投递，把状态更新拉回消息循环线程。
	trayMsgUpdateStatus = winui.WMApp + 100
	trayMsgStop         = winui.WMApp + 101
	trayLifetimeTimerID = 1
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
	app            *App
	hwnd           uintptr
	commandHwnd    atomic.Uintptr
	iconData       *winui.NotifyIconData
	hIcon          windows.Handle
	popup          *hoverPopup
	spaceRefreshCh chan struct{}
	ctx            context.Context
	cancel         context.CancelFunc
	destroyed      bool

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

			// 托盘线程只用 Win32；COM 仅由宠物/面板独立线程初始化并释放。
			host, err := newTrayHost(app)
			if err != nil {
				app.logger.Printf("system tray start failed: %v", err)
				return
			}
			defer host.destroy()

			app.trayReady()
			go host.watchStatus()
			go host.watchUser()
			go host.watchTasks()
			winui.SetTimer(host.hwnd, trayLifetimeTimerID, 250)

			winui.RunMessageLoop()
		}()
	})
}

// newTrayHost 创建消息宿主窗口、注册通知图标，并尝试创建悬停浮窗。
func newTrayHost(app *App) (*trayHost, error) {
	if err := ensureTrayClass(); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	host := &trayHost{app: app, statusText: "启动中…", ctx: ctx, cancel: cancel, spaceRefreshCh: make(chan struct{}, 1)}
	// explorer 重启会广播此消息，届时需要重新添加托盘图标。
	host.taskbarCreatedMsg = winui.RegisterWindowMessage("TaskbarCreated")

	hwnd, err := winui.CreateWindow(0, trayWindowClass, "EasyShare", 0, 0, 0, 0, 0, 0)
	if err != nil {
		cancel()
		return nil, err
	}
	host.hwnd = hwnd
	host.commandHwnd.Store(hwnd)

	trayHostMu.Lock()
	trayHosts[hwnd] = host
	trayHostMu.Unlock()

	if err := host.addIcon(); err != nil {
		host.destroy()
		return nil, err
	}

	// 启动就创建常驻宠物，默认仅收起面板。上传仍走原权限/任务链路。
	var popupRef atomic.Pointer[hoverPopup]
	onDropped := func(paths []string, space string) {
		app.accountMu.RLock()
		dropSession := app.accountSession
		dropServer := strings.TrimSpace(app.config.PlatformBaseURL)
		app.accountMu.RUnlock()
		app.uploadDroppedToSpaceSelection(paths, space, func(title, hint, kind string) {
			app.accountMu.RLock()
			sameAccount := app.accountSession == dropSession && strings.TrimSpace(app.config.PlatformBaseURL) == dropServer
			if popup := popupRef.Load(); popup != nil && sameAccount {
				host.reportDroppedResult(popup, title, hint, kind)
			} else if kind == "ok" || kind == "err" {
				host.RefreshSpaces() // 旧账号终态不回显文件名，但当前账号容量仍需刷新。
			}
			app.accountMu.RUnlock()
		})
	}
	onOpenSpace := func() {
		// 唯一个人入口不受主窗残留共享选择影响；仍复用原 OpenCurrentSpace 同服务器根。
		app.SetDropSpace(account.SpacePersonal)
		if err := app.OpenCurrentSpace(); err != nil {
			if popup := popupRef.Load(); popup != nil {
				popup.SetDropStatus("无法打开", err.Error(), "err")
			}
		}
	}
	popup, popupErr := newHoverPopup(winui.DpiForWindow(hwnd), false,
		app.showWindow, app.SetDropSpace, onDropped, onOpenSpace, app.logger.Printf,
		hoverPopupOptions{onNavigate: func(view string) { openDesktopPetView(app, view) }, onRefreshSpaces: host.RefreshSpaces})
	if popupErr != nil {
		app.logger.Printf("desktop pet unavailable, tray menu remains usable: %v", popupErr)
	} else {
		host.popup = popup
		popupRef.Store(popup)
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
	// 托盘 hover 只保留轻量系统 tooltip，富面板的入口是常驻宠物。
	data.UFlags = winui.NIFMessage | winui.NIFIcon | winui.NIFTip | winui.NIFGUID | winui.NIFShowTip
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
	for {
		select {
		case <-h.ctx.Done():
			return
		case status, ok := <-h.app.trayStatusCh:
			if !ok {
				return
			}
			h.statusMu.Lock()
			h.statusText = status
			h.statusMu.Unlock()
			h.post(trayMsgUpdateStatus)
		}
	}
}

func (h *trayHost) post(message uint32) {
	if h.ctx.Err() != nil {
		return
	}
	if hwnd := h.commandHwnd.Load(); hwnd != 0 {
		winui.PostMessage(hwnd, message, 0, 0)
	}
}

// watchTasks 复用 Core.Snapshot 的真实任务读取；后台只生成快照和 PostMessage。
func (h *trayHost) watchTasks() {
	popup := h.popup
	if popup == nil {
		return
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if h.ctx.Err() != nil {
			return
		}
		if h.app.isQuitting() {
			h.post(trayMsgStop)
			return
		}
		client, err := h.app.coreClient()
		if err != nil {
			popup.SetTasks(desktoppet.SummarizeTasks(nil, false))
		} else {
			ctx, cancel := context.WithTimeout(h.ctx, 1500*time.Millisecond)
			snapshot, err := client.Snapshot(ctx)
			cancel()
			popup.SetTasks(desktoppet.SummarizeTasks(snapshot.Tasks, err == nil))
		}
		select {
		case <-h.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// RefreshSpaces 可从任意 goroutine 调用；有界合并，不触碰 hwnd/COM，也不因隐藏停止读取。
func (h *trayHost) RefreshSpaces() {
	if h == nil || (h.ctx != nil && h.ctx.Err() != nil) {
		return
	}
	select {
	case h.spaceRefreshCh <- struct{}{}:
	default:
	}
}

// notifyTraySpacesChanged 是给 App 主线的内部 hook；不新增 Wails 导出方法/前端绑定。
// delete/restore/quota 成功后调用 notifyTraySpacesChanged(a)，账号仍由后台读取最新快照。
func notifyTraySpacesChanged(a *App) {
	trayHostMu.RLock()
	defer trayHostMu.RUnlock()
	for _, host := range trayHosts {
		if host.app == a {
			host.RefreshSpaces()
		}
	}
}

func (h *trayHost) reportDroppedResult(popup *hoverPopup, title, hint, kind string) {
	popup.SetDropStatus(title, hint, kind)
	if kind == "ok" || kind == "err" {
		h.RefreshSpaces() // 部分失败也可能已有文件改变用量，必须刷新。
	}
}

// watchUser 保留 trayUserCh 与30s兜底；昵称/容量同源，后台网络回包按会话与世代校验。
func (h *trayHost) watchUser() {
	popup := h.popup
	if popup == nil {
		return
	}
	var generation atomic.Uint64
	var cancelRequest context.CancelFunc
	defer func() {
		if cancelRequest != nil {
			cancelRequest()
		}
	}()
	requestDone := make(chan uint64, 1)
	var activeRequest uint64
	refreshAgain := false
	refresh := func() {
		if cancelRequest != nil {
			cancelRequest()
		}
		activeRequest = 0
		refreshAgain = false
		request := generation.Add(1)
		h.app.accountMu.RLock()
		session := h.app.accountSession
		base := strings.TrimSpace(h.app.config.PlatformBaseURL)
		user := h.app.currentUserLocked()
		revision := popup.BeginCapacityRefresh(base, user.UserID, user.LoggedIn, user.NickName, user.UserName)
		h.app.accountMu.RUnlock()
		if !user.LoggedIn || user.UserID == "" || base == "" {
			return
		}
		ctx, cancel := context.WithCancel(h.ctx)
		cancelRequest = cancel
		activeRequest = request
		go func() {
			defer func() {
				select {
				case requestDone <- request:
				case <-h.ctx.Done():
				}
			}()
			spaces, mounted, err := h.app.hoverAccountSpacesWithContext(ctx)
			h.app.accountMu.RLock()
			defer h.app.accountMu.RUnlock()
			// 同账号重登也是新世代；旧账号/旧服务器网络结果不能回填缓存或开放上传入口。
			if ctx.Err() != nil || generation.Load() != request || h.app.accountSession != session || strings.TrimSpace(h.app.config.PlatformBaseURL) != base || h.app.currentUserLocked().UserID != user.UserID {
				return
			}
			popup.CompleteCapacityRefresh(revision, spaces, mounted, err)
		}()
	}
	refresh() // 登录恢复早于托盘创建，不依赖尚未消费的事件。
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-h.ctx.Done():
			generation.Add(1)
			return
		case _, ok := <-h.app.trayUserCh:
			if !ok {
				return
			}
			refresh()
		case <-h.spaceRefreshCh:
			if activeRequest != 0 {
				refreshAgain = true // 上传结束/展开不打断同账号正在读取的请求，完成后最多再读一次。
			} else {
				refresh()
			}
		case request := <-requestDone:
			if request == activeRequest {
				activeRequest = 0
				if refreshAgain {
					refresh()
				}
			}
		case <-ticker.C:
			if activeRequest == 0 {
				refresh()
			}
		}
	}
}

// currentStatus 返回最近一次的服务状态文字。
func (h *trayHost) currentStatus() string {
	h.statusMu.Lock()
	defer h.statusMu.Unlock()
	return h.statusText
}

// trayPetMenuItem 按目标显示态生成菜单；不通过跨线程读取窗口是否可见来猜测。
func trayPetMenuItem(popup *hoverPopup) (text string, enabled bool, show bool) {
	if popup == nil || popup.closed.Load() {
		return "显示宠物", false, true
	}
	if popup.IsPetVisible() {
		return "隐藏宠物", true, false
	}
	return "显示宠物", true, true
}

// showMenu 保留打开主窗口/设置/服务状态/退出原项，只添加宠物的动态显示项。
func (h *trayHost) showMenu() {
	menu, err := winui.NewMenu()
	if err != nil {
		h.app.logger.Printf("tray menu: %v", err)
		return
	}
	defer menu.Destroy()

	_ = menu.AddItem(cmdOpenMain, "打开主窗口", true)
	_ = menu.AddItem(cmdSettings, "设置", true)
	petText, petEnabled, showPet := trayPetMenuItem(h.popup)
	_ = menu.AddItem(cmdPetVisibility, petText, petEnabled)
	if h.popup != nil {
		h.popup.SetMenuActive(true)
		defer h.popup.SetMenuActive(false)
	}
	menu.AddSeparator()
	_ = menu.AddItem(cmdStatus, "服务状态："+h.currentStatus(), false)
	menu.AddSeparator()
	_ = menu.AddItem(cmdQuit, "退出 EasyShare", true)

	pos := winui.GetCursorPos()
	switch menu.Track(h.hwnd, pos.X, pos.Y) {
	case cmdOpenMain:
		h.app.showWindow()
	case cmdSettings:
		openDesktopPetView(h.app, "settings")
	case cmdPetVisibility:
		if h.popup != nil {
			h.popup.SetPetVisible(showPet)
		}
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
	if h.destroyed {
		return
	}
	h.destroyed = true
	if h.cancel != nil {
		h.cancel()
	}
	h.commandHwnd.Store(0)
	winui.KillTimer(h.hwnd, trayLifetimeTimerID)
	if h.iconData != nil {
		// 不删除图标会在通知区域留下无法点击的残影。
		_ = winui.ShellNotifyIcon(winui.NIMDelete, h.iconData)
		h.iconData = nil
	}
	if h.popup != nil {
		// 宠物与面板由自己的创建线程释放，等待有界，不跨线程触碰 COM。
		h.popup.Destroy()
		h.popup.WaitClosed(2 * time.Second)
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

// showPopup 不再由托盘 hover 触发；保留旧内部符号兼容。
func (h *trayHost) showPopup() {}

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
		case winui.NINPopupOpen, winui.NINPopupClose:
			// 富 hover 已迁到宠物；托盘只让 Shell 显示 NIF_SHOWTIP。
			return 0
		case winui.NINSelect:
			h.app.showWindow()
		case winui.WMContextMenu:
			h.showMenu()
		}
		return 0

	case trayMsgUpdateStatus:
		if h.iconData != nil {
			winui.SetNotifyIconTip(h.iconData, "EasyShare · "+h.currentStatus())
			_ = winui.ShellNotifyIcon(winui.NIMModify, h.iconData)
		}
		return 0
	case trayMsgStop:
		h.destroy()
		winui.PostQuitMessage(0)
		return 0
	case winui.WMTimer:
		if wParam == trayLifetimeTimerID && (h.app.isQuitting() || (h.app.ctx != nil && h.app.ctx.Err() != nil)) {
			h.destroy()
			winui.PostQuitMessage(0)
		}
		return 0

	case winui.WMDestroy:
		winui.PostQuitMessage(0)
		return 0
	}

	return winui.DefWindowProc(hwnd, msg, wParam, lParam)
}
