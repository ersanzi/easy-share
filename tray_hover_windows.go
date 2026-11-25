//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"easyshare/internal/account"
	"easyshare/internal/desktoppet"
	"easyshare/internal/drive"
	"easyshare/internal/winui"

	"github.com/wailsapp/go-webview2/pkg/edge"
	"golang.org/x/sys/windows"
)

const (
	hoverWindowClass = "EasyShareHoverPopup"

	// hoverHideTimerID 是延迟收起定时器的 ID；hoverHideDelayMS 给鼠标留出从
	// 通知区域图标移动到浮窗的时间，太短会导致浮窗还没碰到就消失。
	hoverHideTimerID = 1
	hoverHideDelayMS = 80
	// 动画只由本窗口所属线程的 WM_TIMER 推进，迟到帧按真实 elapsed time 采样。
	hoverAnimationTimerID = 3
	hoverAnimationTickMS  = 16

	// 浮窗窗口自己的控制消息。浮窗运行在独立线程上，所有来自托盘线程的请求
	// 都必须经 PostMessage 转到该线程执行——窗口不能跨线程操作。
	hoverMsgShow        = winui.WMApp + 200
	hoverMsgHide        = winui.WMApp + 201
	hoverMsgScheduleHde = winui.WMApp + 202
	hoverMsgDestroy     = winui.WMApp + 203
	hoverMsgStartPinned = winui.WMApp + 204
	hoverMsgSetUser     = winui.WMApp + 205
	hoverMsgSetSpaces   = winui.WMApp + 206
	hoverMsgBeginDrag   = winui.WMApp + 207
	hoverMsgDropStatus  = winui.WMApp + 208
	hoverMsgSetTasks    = winui.WMApp + 209
	hoverMsgSetPetUI    = winui.WMApp + 210
	hoverMsgMenuGuard   = winui.WMApp + 211

	// dropMessage 是页面拖入文件时发来的消息名，与页面里的字面量必须一致。
	dropMessage = "file:drop"
)

// hoverPopup 是托盘悬停浮窗：一个自建的 Win32 弹出窗口，内部嵌入 WebView2 渲染
// tray_hover_asset_windows.go 里的页面。
//
// 之所以不复用 Wails 主窗口：Wails v2 是单窗口架构，运行时没有创建窗口的 API，
// 复用会让浮窗与主窗口互斥。详见 docs/iterations/2026-08-28-tray-hover-widget.md。
//
// 线程模型：浮窗独占一个 OS 线程，在该线程完成 COM 初始化、建窗、嵌入 WebView2
// 并运行消息循环。之所以不与托盘共用线程——go-webview2 的 Embed 内部会自己跑一个
// 嵌套消息循环直到 WebView2 就绪，与托盘的主循环放在同一线程会互相干扰。
// hoverSpace 保留内部上传目标投影兼容；UI 只展示当前账号个人容量，不再提供多空间切换。
// 共享空间即使控制面授权也标为不可用，不能因此开放本面板上传入口。
type hoverSpace struct {
	// Kind 是 personal / shared，与 drive 包的空间常量一致。
	Kind string `json:"kind"`
	// Label 是档位文案，如「网盘」「共享」。
	Label string `json:"label"`
	// Quota 保留旧内部文字接口；容量条只读取真实字节投影，绝不读取上传进度。
	Quota      string `json:"quota"`
	UsedBytes  int64  `json:"usedBytes"`
	QuotaBytes int64  `json:"quotaBytes"`
	OwnerID    string `json:"ownerId,omitempty"`
	// ReadOnly 的空间不能作上传目标，档位置灰。
	ReadOnly bool `json:"readOnly"`
	// Unavailable 表示窗口入口尚未实际挂载；不能只按配额推断可用。
	Unavailable string `json:"unavailable,omitempty"`
}

type hoverPopup struct {
	hwnd        uintptr
	chromium    *edge.Chromium
	width       int32
	pet         *desktopPet
	commandHwnd atomic.Uintptr
	closed      atomic.Bool
	done        chan struct{}
	pageReady   bool

	// 单一面板高度；hover 与固定态都提供完整空间/上传内容。
	heightPinned int32

	// 仅供托盘/后台读取，状态机与动画本体只属于创建 UI 线程。
	pinned     atomic.Bool
	petHidden  atomic.Bool
	menuActive atomic.Bool
	visible    bool

	animation      desktoppet.Animation
	animationTimer bool
	panelRect      winui.Rect
	panelDPI       int32
	panelWindow    winui.Rect
	panelClip      winui.Rect
	frameApplied   bool
	reducedMotion  bool

	// 登录态（托盘线程写、浮窗线程读）：头像字符与昵称，随账号登录/登出更新。
	userMu     sync.Mutex
	userAvatar string
	userName   string

	// 账号/容量同一把锁与同一快照；仅保留当前服务器+UserID最近有效值，换账号先清空。
	capacityMu      sync.Mutex
	capacity        hoverCapacityState
	onRefreshSpaces func()

	// 空间列表与当前选中的目标空间（托盘线程写、浮窗线程读）。
	// activeSpace 决定拖入浮窗的文件上传到哪个空间。
	spaceMu     sync.Mutex
	spaces      []hoverSpace
	activeSpace string

	// 上传工作线程只提交状态，由浮窗消息循环调用 WebView2。
	dropStatusMu sync.Mutex
	dropStatus   [3]string
	taskMu       sync.Mutex
	taskSummary  desktoppet.TaskSummary
	onNavigate   func(string)

	onOpenMain func()
	// onSpaceSelect 在用户切换目标空间时回调，供上层记住选择。
	onSpaceSelect func(kind string)
	// onFilesDropped 在用户往浮窗拖入文件时回调，参数是真实文件系统路径。
	onFilesDropped func(paths []string, space string)
	// onOpenSpace 在用户点「打开」时回调，打开当前选中的空间。
	onOpenSpace func()
	logf        func(string, ...any)

	// 兼容旧构造签名，P4 启动默认收起面板。
	startPinned bool
}

var (
	hoverClassOnce  sync.Once
	hoverClassErr   error
	hoverInstances  = map[uintptr]*hoverPopup{}
	hoverInstanceMu sync.RWMutex
)

type hoverPopupOptions struct {
	onNavigate      func(string)
	onRefreshSpaces func()
}

// newHoverPopup 保留既有参数，附加内部 options 不产生 Wails 导出绑定。
func newHoverPopup(dpi int32, startPinned bool, onOpenMain func(), onSpaceSelect func(string), onFilesDropped func([]string, string), onOpenSpace func(), logf func(string, ...any), options ...hoverPopupOptions) (*hoverPopup, error) {
	scale := func(v int32) int32 { return v * dpi / 96 }
	p := &hoverPopup{
		width:          scale(hoverPopupWidth),
		heightPinned:   scale(hoverPopupHeightPinned),
		startPinned:    startPinned,
		onOpenMain:     onOpenMain,
		onSpaceSelect:  onSpaceSelect,
		onFilesDropped: onFilesDropped,
		onOpenSpace:    onOpenSpace,
		logf:           logf,
		done:           make(chan struct{}),
	}

	if len(options) > 0 {
		p.onNavigate = options[0].onNavigate
		p.onRefreshSpaces = options[0].onRefreshSpaces
	}
	ready := make(chan error, 1)
	go p.run(ready)
	if err := <-ready; err != nil {
		return nil, err
	}
	return p, nil
}

// run 是浮窗线程的主体：初始化、建窗、嵌入 WebView2，然后跑消息循环。
func (p *hoverPopup) run(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(p.done)
	restoreDPI := winui.EnterPerMonitorDPI()
	defer restoreDPI()
	// 原生面板与 WebView 使用同一系统减少动态效果偏好。
	p.reducedMotion = !winui.ClientAreaAnimationsEnabled()

	// COM 初始化与释放必须在创建宠物/面板的同一线程，S_FALSE 也要成对释放。
	releaseCOM, err := winui.AcquireCOMSTA()
	if err != nil {
		ready <- err
		return
	}
	defer releaseCOM()
	if err := ensureHoverClass(); err != nil {
		ready <- err
		return
	}

	// WS_EX_TOOLWINDOW：不出现在任务栏和 Alt-Tab 中。
	//
	// 刻意**不加 WS_EX_NOACTIVATE**：带该标志的窗口无法被激活，Windows 的拖放循环
	// 就不会把它当作放置目标——HTML5 的 drop 事件与 WM_DROPFILES 都收不到，
	// 表现为「拖进去毫无反应」。本机对照可证：能正常接收拖放的桌面挂件
	// （Nexus、PowerToys QuickAccess）与 Wails 主窗口都不带此标志。
	//
	// 「弹出时不抢焦点」改由 ShowWindowNoActivate（SW_SHOWNOACTIVATE）达成，
	// 那是显示时的行为而非窗口的固有属性，不影响拖放。
	hwnd, err := winui.CreateWindow(
		winui.WSExToolWindow|winui.WSExTopMost,
		hoverWindowClass, "私人云盘",
		winui.WSPopup|winui.WSClipChildren,
		0, 0, p.width, p.heightPinned, 0,
	)
	if err != nil {
		ready <- err
		return
	}
	p.hwnd = hwnd
	p.commandHwnd.Store(hwnd)

	hoverInstanceMu.Lock()
	hoverInstances[hwnd] = p
	hoverInstanceMu.Unlock()

	pet, err := newDesktopPet(p)
	if err != nil {
		p.cleanup()
		ready <- err
		return
	}
	p.pet = pet
	// 先显示原生宠物，面板默认为隐藏；Embed 嵌套循环也只属于此 UI 线程。
	if err := p.embedWebView(); err != nil {
		p.logf("pet: 面板初始化失败，原生宠物继续常驻: %v", err)
	}

	ready <- nil
	winui.RunMessageLoop()
	p.cleanup()
}

// embedWebView 把 WebView2 嵌入浮窗窗口并加载页面。
//
// 注意 go-webview2 的 Embed 无论成功失败都返回 true，错误只经 errorCallback
// 上报，因此不能用它的返回值判断成败——这里用 embedErr 收集真实结果。
func (p *hoverPopup) embedWebView() error {
	var embedErr error
	chromium := edge.NewChromium()

	// 必须使用独立的用户数据目录：Wails 主窗口的 WebView2 环境默认落在
	// %AppData%\easyshare.exe，且带有 Wails 自己的 AdditionalBrowserArgs。
	// 同一目录配不同选项会让第二个环境创建失败（HRESULT 0x8007139F
	// ERROR_INVALID_STATE），表现为「控制器创建失败」。
	dataPath, err := hoverDataPath()
	if err != nil {
		return err
	}
	chromium.DataPath = dataPath

	chromium.MessageCallback = p.handleWebMessage
	// 拖入文件走这条回调：页面把 File 对象作为「附加对象」随消息传来，
	// 原生侧才能读到真实路径。详见 handleDroppedObjects。
	chromium.MessageWithAdditionalObjectsCallback = p.handleDroppedObjects
	chromium.SetErrorCallback(func(e error) {
		// 先记下首个错误供 Embed 返回后判定，同时写日志便于排障。
		if embedErr == nil {
			embedErr = e
		}
		p.logf("hover popup webview error: %v", e)
	})

	chromium.Embed(p.hwnd)
	if embedErr != nil {
		return embedErr
	}
	if chromium.GetController() == nil {
		return fmt.Errorf("webview2 控制器未就绪（可能缺少 WebView2 Runtime）")
	}

	p.chromium = chromium
	p.resizeWebViewOnOwnThread()
	// 必须显式设为可见：嵌入场景下控制器的 IsVisible 不保证默认为真，
	// 不设时 WebView2 不会创建渲染子窗口，表现为浮窗完全透明（看到的是桌面）。
	if err := p.chromium.Show(); err != nil {
		return fmt.Errorf("webview2 显示失败: %w", err)
	}

	// 页面通过 HTML5 drop + postMessageWithAdditionalObjects 接收文件。
	// AllowExternalDrag 直接对应 Controller.AllowExternalDrop；关闭它会屏蔽外部拖入，
	// 不会把拖放转交宿主 WM_DROPFILES。导航默认行为由页面 preventDefault 阻止。
	if err := p.chromium.AllowExternalDrag(true); err != nil {
		p.logf("hover popup: 启用 WebView2 外部拖放失败，拖入上传可能无效: %v", err)
	}

	// 面板无论 hover/固定都允许外部 drop；只用显示时 noactivate 避免抢焦点。
	p.chromium.NavigateToString(hoverPopupHTML)
	return nil
}

// resizeWebViewOnOwnThread 直调控制器，不用后台 timer；WebView 保持物理 bounds 与本屏 DIP。
func (p *hoverPopup) resizeWebViewOnOwnThread() {
	if p.chromium == nil {
		return
	}
	controller := p.chromium.GetController()
	if controller == nil {
		return
	}
	r := winui.GetClientRect(p.hwnd)
	if p.pet != nil && p.panelRect.Height() > 0 {
		upward := p.panelRect.Bottom <= winui.GetWindowRect(p.pet.hwnd).Top
		r, _ = desktoppet.RevealBounds(p.panelRect, p.animation.Tick(p.pet.nowMS()), upward)
	}
	if err := controller.PutBounds(edge.Rect{Left: r.Left, Top: r.Top, Right: r.Right, Bottom: r.Bottom}); err != nil {
		p.logf("pet panel resize: %v", err)
	}
	if err := controller.NotifyParentWindowPositionChanged(); err != nil {
		p.logf("pet panel position: %v", err)
	}
	// go-webview2 Embed 默认禁用自动监测；这里按真实宿主显示器开启，CSS DIP 随跨屏 DPI。
	if c3 := controller.GetICoreWebView2Controller3(); c3 != nil {
		_ = winui.WebViewDetectMonitorScale(c3)
		winui.ReleaseCOMInterface(c3)
	}
}

// hoverDataPath 返回浮窗专用的 WebView2 用户数据目录。
func hoverDataPath() (string, error) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		return "", fmt.Errorf("LOCALAPPDATA 未设置")
	}
	dir := filepath.Join(base, "EasyShare", "webview2-widget")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// handleWebMessage 处理页面通过 postMessage 发来的消息。
// WebView2 传来的字符串是 JSON 字面量，因此带引号。
// 该回调在浮窗线程上触发。
func (p *hoverPopup) handleWebMessage(message string, _ *edge.ICoreWebView2, _ *edge.ICoreWebView2WebMessageReceivedEventArgs) {
	if p.closed.Load() {
		return
	}
	text := trimJSONQuotes(message)
	if p.pet != nil && (p.pet.hidden || p.petHidden.Load()) {
		// 已隐藏期间到达的旧 DOM 进出/拖放事件不能重新开启 UI 状态或计时器。
		switch text {
		case "pin-toggle", "pointer-enter", "pointer-leave", "external-drag-enter", "external-drag-leave":
			return
		}
	}
	// 空间切换带参数，前缀匹配后单独处理
	if kind, ok := strings.CutPrefix(text, "space-select:"); ok {
		p.spaceMu.Lock()
		valid := false
		for _, space := range p.spaces {
			if kind == account.SpacePersonal && space.Kind == kind && !space.ReadOnly && space.Unavailable == "" {
				valid = true
				break
			}
		}
		if valid {
			p.activeSpace = kind
		}
		p.spaceMu.Unlock()
		if valid && p.onSpaceSelect != nil {
			p.onSpaceSelect(kind)
		}
		return
	}
	switch text {
	case "begin-drag":
		// 兼容旧消息但不移动面板；只有原生宠物接收窗口拖动。
		return
	case "open-space":
		// 打开可能要等资源管理器起来，别占住浮窗线程
		if p.onOpenSpace != nil {
			go p.onOpenSpace()
		}
	case "open-main":
		if p.onOpenMain != nil {
			p.onOpenMain()
		}
	case "open-tasks", "open-settings":
		view := "settings"
		if text == "open-tasks" {
			view = "transfers"
		}
		if p.onNavigate != nil {
			p.onNavigate(view)
		} else if p.onOpenMain != nil {
			p.onOpenMain()
		}
	case "page-ready":
		p.pageReady = true
		p.setUserOnOwnThread()
		p.setSpacesOnOwnThread()
		p.setDropStatusOnOwnThread()
		p.setTasksOnOwnThread()
		p.applyPinnedToPage(p.IsPinned())
	case "pin-toggle":
		p.togglePinOnOwnThread()
	case "pointer-enter":
		if p.pet != nil {
			p.pet.pointer(desktoppet.Panel, true)
		}
	case "pointer-leave":
		if p.pet != nil {
			p.pet.pointer(desktoppet.Panel, false)
		}
	case "external-drag-enter":
		if p.pet != nil {
			p.pet.state.SetExternalDrag(true)
			p.pet.present()
		}
	case "external-drag-leave":
		if p.pet != nil {
			p.pet.state.SetExternalDrag(false)
			p.pet.syncPointer(false)
		}
	}
}

// --- 以下三个方法供托盘线程调用，只投递消息，不直接碰窗口 ---

func (p *hoverPopup) post(msg uint32) {
	if !p.closed.Load() {
		if hwnd := p.commandHwnd.Load(); hwnd != 0 {
			winui.PostMessage(hwnd, msg, 0, 0)
		}
	}
}

// ShowNear 保留旧内部接口但不再响应托盘 hover；只在宠物旁展示面板。
func (p *hoverPopup) ShowNear(_ winui.Rect) { p.post(hoverMsgShow) }

// Hide 仅请求收起未固定面板。
func (p *hoverPopup) Hide()         { p.post(hoverMsgHide) }
func (p *hoverPopup) scheduleHide() { p.post(hoverMsgScheduleHde) }

// SetPetVisible 只提交明确的显示状态，不切换任务/backend，也不跨线程调用 Win32。
// 原子目标先更新，排队的旧显示请求不会短暂重新显示已经要求隐藏的宠物。
func (p *hoverPopup) SetPetVisible(visible bool) {
	if p.closed.Load() {
		return
	}
	p.petHidden.Store(!visible)
	p.post(hoverMsgSetPetUI)
}

// IsPetVisible 供托盘生成动态菜单，不读取 UI 线程的 hwnd/state。
func (p *hoverPopup) IsPetVisible() bool { return !p.closed.Load() && !p.petHidden.Load() }

// SetMenuActive 保护托盘原生菜单跨窗口期间的面板；只投递到创建线程取消待决连击。
func (p *hoverPopup) SetMenuActive(active bool) {
	p.menuActive.Store(active)
	p.post(hoverMsgMenuGuard)
}

func (p *hoverPopup) menuGuarded() bool {
	return p.menuActive.Load() || (p.pet != nil && p.pet.menuOpen)
}

// RequestSpacesRefresh 只唤醒后台容量读取器，页面/Win32 UI 不直接发 API。
func (p *hoverPopup) RequestSpacesRefresh() {
	if !p.closed.Load() && p.onRefreshSpaces != nil {
		p.onRefreshSpaces()
	}
}

// BeginCapacityRefresh 原子替换账号与容量身份，账号切换先清空旧数字/旧上传目标。
func (p *hoverPopup) BeginCapacityRefresh(server, userID string, loggedIn bool, nickName, userName string) uint64 {
	if p.closed.Load() {
		return 0
	}
	p.capacityMu.Lock()
	revision, changed := p.capacity.begin(hoverAccountIdentity(server, userID), loggedIn, nickName, userName)
	if changed {
		p.spaceMu.Lock()
		p.spaces, p.activeSpace = nil, ""
		p.spaceMu.Unlock()
		p.dropStatusMu.Lock()
		p.dropStatus = [3]string{}
		p.dropStatusMu.Unlock()
	}
	p.capacityMu.Unlock()
	if changed && p.onSpaceSelect != nil {
		p.onSpaceSelect("")
	}
	p.post(hoverMsgSetUser)
	return revision
}

// CompleteCapacityRefresh 工作线程只提交真实 MySpaces 快照；UI 线程随后 Eval 最新同账号视图。
func (p *hoverPopup) CompleteCapacityRefresh(revision uint64, spaces []account.Space, mounted bool, readErr error) {
	if p.closed.Load() {
		return
	}
	p.capacityMu.Lock()
	applied := p.capacity.complete(revision, spaces, mounted, readErr, time.Now())
	if applied {
		projected := projectHoverSpaces(spaces, mounted)
		if readErr != nil || p.capacity.view.Stale || !p.capacity.view.Known {
			projected = nil
		}
		p.spaceMu.Lock()
		p.spaces = projected
		p.activeSpace = ""
		for _, space := range projected {
			if space.Kind == account.SpacePersonal && !space.ReadOnly && space.Unavailable == "" && p.capacity.view.UploadEnabled {
				p.activeSpace = account.SpacePersonal
				break
			}
		}
		selected := p.activeSpace
		p.spaceMu.Unlock()
		if p.onSpaceSelect != nil {
			p.onSpaceSelect(selected)
		}
	}
	p.capacityMu.Unlock()
	if applied {
		p.post(hoverMsgSetSpaces)
	}
}

func (p *hoverPopup) capacitySnapshot() hoverCapacityView {
	p.capacityMu.Lock()
	defer p.capacityMu.Unlock()
	return p.capacity.view
}

// Destroy 只投递到创建线程；关闭后工作线程不再投递到可能重用的 hwnd。
func (p *hoverPopup) Destroy() {
	if p.closed.CompareAndSwap(false, true) {
		if hwnd := p.commandHwnd.Load(); hwnd != 0 {
			winui.PostMessage(hwnd, hoverMsgDestroy, 0, 0)
		}
	}
}

func (p *hoverPopup) WaitClosed(timeout time.Duration) {
	if p.done == nil {
		return
	}
	select {
	case <-p.done:
	case <-time.After(timeout):
	}
}

// SetUser 更新悬浮窗展示的登录账号（头像字符 + 昵称）。托盘线程调用。
// 未登录时头像回落为「我」、昵称空。
func (p *hoverPopup) SetUser(loggedIn bool, nickName, userName string) {
	if p.closed.Load() {
		return
	}
	// 旧接口没有稳定身份，不能把传入的新昵称和之前容量拼接；清空到待确认状态。
	p.BeginCapacityRefresh("", "", loggedIn, nickName, userName)
	avatar := "我"
	name := ""
	if loggedIn {
		text := strings.TrimSpace(nickName)
		if text == "" {
			text = strings.TrimSpace(userName)
		}
		display := text
		if r := []rune(display); len(r) > 0 {
			avatar = string(r[0])
			name = display
		}
	}
	p.userMu.Lock()
	p.userAvatar = avatar
	p.userName = name
	p.userMu.Unlock()
	p.post(hoverMsgSetUser)
}

// SetSpaces 保留旧内部目标列表接口；只有个人可写项能被选中，共享永不成为上传目标。
func (p *hoverPopup) SetSpaces(spaces []hoverSpace) {
	if p.closed.Load() {
		return
	}
	p.spaceMu.Lock()
	p.spaces = append([]hoverSpace(nil), spaces...)
	// 当前选中的空间如果已经不在列表里（配额被收回、授权被撤销），清掉让页面重选
	stillThere := false
	for _, space := range spaces {
		if space.Kind == account.SpacePersonal && space.Kind == p.activeSpace && !space.ReadOnly && space.Unavailable == "" {
			stillThere = true
			break
		}
	}
	if !stillThere {
		p.activeSpace = ""
		for _, space := range spaces {
			if space.Kind == account.SpacePersonal && !space.ReadOnly && space.Unavailable == "" {
				p.activeSpace = space.Kind
				break
			}
		}
	}
	selected := p.activeSpace
	p.spaceMu.Unlock()
	if p.onSpaceSelect != nil {
		p.onSpaceSelect(selected)
	}
	p.post(hoverMsgSetSpaces)
}

// ActiveSpace 返回当前选中的目标空间，未选时为空串。托盘线程与主线程都可读。
func (p *hoverPopup) ActiveSpace() string {
	p.spaceMu.Lock()
	defer p.spaceMu.Unlock()
	return p.activeSpace
}

// --- 以下方法只在浮窗线程上执行 ---

// showOnOwnThread 定位唯一面板并请求展开；重复 present 不重启同一目标动画。
func (p *hoverPopup) showOnOwnThread() {
	if p.pet == nil || p.pet.hidden || p.petHidden.Load() || p.hwnd == 0 {
		return
	}
	petRect := winui.GetWindowRect(p.pet.hwnd)
	work, dpi := winui.WorkAreaAndDPIForRect(petRect, p.pet.dpi)
	if work.Width() <= 0 || work.Height() <= 0 {
		return
	}
	p.width = desktoppet.Scale(hoverPopupWidth, dpi)
	p.heightPinned = desktoppet.Scale(hoverPopupHeightPinned, dpi)
	candidate := desktoppet.PanelRect(petRect, work, p.width, p.heightPinned, 0)
	// 面板按自身最终位置的显示器独立取 DPI，而非沿用宠物窗口缩放。
	if panelWork, panelDPI := winui.WorkAreaAndDPIForRect(candidate, dpi); panelWork.Width() > 0 {
		dpi = panelDPI
		p.width = desktoppet.Scale(hoverPopupWidth, dpi)
		p.heightPinned = desktoppet.Scale(hoverPopupHeightPinned, dpi)
		candidate = desktoppet.PanelRect(petRect, panelWork, p.width, p.heightPinned, 0)
	}
	p.panelRect = candidate
	p.panelDPI = dpi
	if !p.animation.Expanded() {
		p.RequestSpacesRefresh() // 仅展开目标的边沿唤醒后台，不在每帧/每次 present 发网络请求。
	}
	p.animateOnOwnThread(true)
}

// animateOnOwnThread / onAnimationTimer 不创建新窗口、WebView 或后台动画循环。
func (p *hoverPopup) animateOnOwnThread(expanded bool) {
	if p.pet == nil || p.hwnd == 0 {
		return
	}
	value := p.animation.SetExpanded(expanded, p.pet.nowMS(), p.reducedMotion)
	p.applyAnimationFrameOnOwnThread(value)
	if p.animation.Running() {
		if !p.animationTimer {
			winui.SetTimer(p.hwnd, hoverAnimationTimerID, hoverAnimationTickMS)
			p.animationTimer = true
		}
	} else {
		winui.KillTimer(p.hwnd, hoverAnimationTimerID)
		p.animationTimer = false
	}
}

func (p *hoverPopup) onAnimationTimer() {
	if p.pet == nil || p.pet.hidden || p.petHidden.Load() {
		p.hideImmediatelyOnOwnThread()
		return
	}
	p.applyAnimationFrameOnOwnThread(p.animation.Tick(p.pet.nowMS()))
	if !p.animation.Running() {
		winui.KillTimer(p.hwnd, hoverAnimationTimerID)
		p.animationTimer = false
	}
}

// applyAnimationFrameOnOwnThread 固定宿主，以完整WebView负高度偏移配合真实region裁剪。
// 全部控制器操作只在所属线程；隐藏终帧只 Hide，收起不停止上传/任务或释放 COM。
func (p *hoverPopup) applyAnimationFrameOnOwnThread(value float64) {
	if p.hwnd == 0 {
		return
	}
	if value <= 0 || p.panelRect.Width() <= 0 || p.panelRect.Height() <= 0 {
		if p.visible {
			if p.chromium != nil {
				_ = p.chromium.Hide()
			}
			winui.HideWindow(p.hwnd)
			p.visible = false
		}
		p.frameApplied = false
		p.panelWindow, p.panelClip = winui.Rect{}, winui.Rect{}
		return
	}
	window := p.panelRect
	upward := p.pet != nil && window.Bottom <= winui.GetWindowRect(p.pet.hwnd).Top
	bounds, clip := desktoppet.RevealBounds(window, value, upward)
	moved := !p.frameApplied || window != p.panelWindow
	clipped := !p.frameApplied || clip != p.panelClip
	if clipped {
		if err := winui.SetClippedRoundedRegion(p.hwnd, clip, desktoppet.Scale(12, p.panelDPI)); err != nil {
			p.logf("pet panel clip: %v", err)
			return
		}
	}
	if moved {
		resized := !p.frameApplied || window.Width() != p.panelWindow.Width() || window.Height() != p.panelWindow.Height()
		winui.MoveWindowNoActivate(p.hwnd, window)
		if resized {
			p.resizeWebViewOnOwnThread()
		} else if p.chromium != nil {
			if controller := p.chromium.GetController(); controller != nil {
				_ = controller.NotifyParentWindowPositionChanged()
			}
		}
	}
	// PutBounds accepts signed parent-client coordinates; keep width/height constant.
	// The host's real window region clips both rendering and pointer input.
	if p.chromium != nil {
		if controller := p.chromium.GetController(); controller != nil {
			if err := controller.PutBounds(edge.Rect{Left: bounds.Left, Top: bounds.Top, Right: bounds.Right, Bottom: bounds.Bottom}); err != nil {
				p.logf("pet panel reveal bounds: %v", err)
			}
		}
	}
	p.panelWindow, p.panelClip, p.frameApplied = window, clip, true
	if !p.visible {
		if p.chromium != nil {
			_ = p.chromium.Show()
		}
		winui.ShowWindowNoActivate(p.hwnd)
		p.visible = true
	}
	// 小工作区不可避免有重叠时，仍让宠物处于面板之上、保持可拖动。
	if moved && p.pet != nil && !p.pet.hidden {
		winui.RaiseWindowNoActivate(p.pet.hwnd)
	}
}

// togglePinOnOwnThread 只切换展开固定，不改变宠物位置、窗口拖放能力或面板尺寸。
func (p *hoverPopup) togglePinOnOwnThread() {
	if p.pet != nil {
		p.pet.togglePin()
	}
}

// SetDropStatus 把上传状态写回浮窗的拖放区。
//
// kind 取 "busy" / "ok" / "err"，空串恢复默认样式。可从任意线程调用。
func (p *hoverPopup) SetDropStatus(title, hint, kind string) {
	if p.closed.Load() {
		return
	}
	p.dropStatusMu.Lock()
	p.dropStatus = [3]string{title, hint, kind}
	p.dropStatusMu.Unlock()
	p.post(hoverMsgDropStatus)
}

// SetTasks 后台只提交真实 Core 投影与 PostMessage，不能访问 WebView2/COM。
func (p *hoverPopup) SetTasks(summary desktoppet.TaskSummary) {
	if p.closed.Load() {
		return
	}
	p.taskMu.Lock()
	p.taskSummary = summary
	p.taskMu.Unlock()
	p.post(hoverMsgSetTasks)
}

func (p *hoverPopup) setTasksOnOwnThread() {
	if p.chromium == nil || !p.pageReady {
		return
	}
	p.taskMu.Lock()
	summary := p.taskSummary
	p.taskMu.Unlock()
	if summary.Label == "" {
		return
	}
	payload, err := json.Marshal(summary)
	if err == nil {
		p.chromium.Eval(fmt.Sprintf("window.applyTasks && window.applyTasks(%s)", payload))
	}
}

// setDropStatusOnOwnThread 只能由创建 WebView2 的浮窗线程调用。
func (p *hoverPopup) setDropStatusOnOwnThread() {
	if p.chromium == nil || !p.pageReady {
		return
	}
	p.capacityMu.Lock()
	defer p.capacityMu.Unlock()
	p.dropStatusMu.Lock()
	status := p.dropStatus
	p.dropStatusMu.Unlock()
	if status[0] == "" {
		return
	}
	// 文案含文件名与错误信息，走 JSON 免得引号把 JS 搞坏。
	payload, err := json.Marshal(status)
	if err != nil {
		return
	}
	// 状态回显与容量使用同一身份版本，旧账号排队的 Eval 不得覆到新账号卡片。
	p.chromium.Eval(fmt.Sprintf(
		"window.applyDropStatus && window.applyDropStatus(%s[0],%s[1],%s[2],%d)",
		payload, payload, payload, p.capacity.view.AccountVersion))
}

// applyPinnedToPage 把固定态同步给页面（驱动按钮高亮、拖放区与拖动把手显隐）。
func (p *hoverPopup) applyPinnedToPage(pinned bool) {
	if p.chromium == nil || !p.pageReady {
		return
	}
	if pinned {
		p.chromium.Eval("window.applyPinned(true)")
		return
	}
	p.chromium.Eval("window.applyPinned(false)")
}

// IsPinned 供托盘线程查询固定态，判断左键/右键是否应收起浮窗。
func (p *hoverPopup) IsPinned() bool {
	return p.pinned.Load()
}

// enterStartPinned 保留内部消息兼容，启动默认不投递；只固定展开，不锁位置。
func (p *hoverPopup) enterStartPinned() {
	if p.pet != nil && !p.pet.state.IsPinned() {
		p.pet.togglePin()
	}
}

func (p *hoverPopup) scheduleHideOnOwnThread() {
	if p.pet != nil {
		p.pet.syncPointer(false)
	}
}

func (p *hoverPopup) onHideTimer() {
	if p.pet != nil {
		p.pet.syncPointer(true)
	}
}

// setUserOnOwnThread 将昵称与稳定身份容量同帧写入，只在 WebView 所属线程执行。
func (p *hoverPopup) setUserOnOwnThread() { p.setCapacityOnOwnThread() }

func (p *hoverPopup) setCapacityOnOwnThread() {
	if p.chromium == nil || !p.pageReady {
		return
	}
	p.capacityMu.Lock()
	defer p.capacityMu.Unlock()
	view := p.capacity.view
	if p.capacity.revision == 0 {
		// 兼容隔离 GUI 的旧 SetUser 接口；没有真实 UserID 时绝不宣称有容量记录。
		p.userMu.Lock()
		view.Name, view.LoggedIn = p.userName, p.userName != ""
		p.userMu.Unlock()
		view.State, view.UsageText, view.Hint = "loading", "正在读取个人容量…", "共享云盘暂不支持此入口"
	}
	payload, err := json.Marshal(view)
	if err != nil {
		return
	}
	// 账号切换也持有 capacityMu；不能在新身份已提交之后发送旧快照给页面。
	p.chromium.Eval(fmt.Sprintf("window.applyCapacity && window.applyCapacity(%s)", payload))
}

// setSpacesOnOwnThread 不再呈现多空间切换器；与账号同源的个人容量是唯一 UI 入口。
func (p *hoverPopup) setSpacesOnOwnThread() { p.setCapacityOnOwnThread() }

// beginDragOnOwnThread 刻意不拖面板；按钮、滚动与上传区不能成为窗口把手。
func (p *hoverPopup) beginDragOnOwnThread() {}

// handleDroppedObjects 从 WebView2 的「附加对象」里取出被拖入文件的真实路径。
//
// 为什么必须这样绕一圈：WebView2 会建一个子窗口盖住整个客户区，拖放落在子窗口上，
// 宿主窗口的 WM_DROPFILES 永远收不到（试过，收不到）。而页面里的 HTML5
// DataTransfer 出于安全只给文件名、不给完整路径。
//
// WebView2 为此提供了 postMessageWithAdditionalObjects：页面把 File 对象随消息一起
// 传过来，原生侧拿到 ICoreWebView2File 就能读 Path。这也是 Wails 主窗口拖放的做法。
func (p *hoverPopup) handleDroppedObjects(message string, _ *edge.ICoreWebView2, args *edge.ICoreWebView2WebMessageReceivedEventArgs) {
	if p.closed.Load() || trimJSONQuotes(message) != dropMessage {
		return
	}
	if p.pet != nil {
		p.pet.state.SetExternalDrag(false)
		p.pet.pointer(desktoppet.Panel, true)
	}
	objects, err := args.GetAdditionalObjects()
	if err != nil {
		p.logf("hover drop: 取附加对象失败: %v", err)
		return
	}
	if objects == nil {
		return
	}
	defer objects.Release()

	count, err := objects.GetCount()
	if err != nil {
		p.logf("hover drop: 取附加对象数量失败: %v", err)
		return
	}

	paths := make([]string, 0, count)
	for i := uint32(0); i < count; i++ {
		raw, err := objects.GetValueAtIndex(i)
		if err != nil {
			p.logf("hover drop: 取第 %d 个对象失败: %v", i, err)
			continue
		}
		if raw == nil {
			// 拖入的不是文件（如网页选区），跳过而不是整批失败
			continue
		}
		file := (*edge.ICoreWebView2File)(unsafe.Pointer(raw))
		path, err := file.GetPath()
		file.Release()
		if err != nil {
			p.logf("hover drop: 取第 %d 个对象路径失败: %v", i, err)
			continue
		}
		if path != "" {
			paths = append(paths, path)
		}
	}

	if len(paths) == 0 {
		p.logf("hover drop: 未取到任何文件路径")
		return
	}
	p.logf("hover drop: %d 项", len(paths))
	if p.onFilesDropped == nil {
		return
	}
	space := p.ActiveSpace()
	if space != account.SpacePersonal {
		p.SetDropStatus("暂不能上传", "请等待云盘连接完成", "err")
		return
	}
	// 在浮窗线程固定选择，再异步上传；不在工作线程重读可能已经切换的档位。
	go p.onFilesDropped(paths, space)
}

// hideOnOwnThread 请求收起；连续进出从当前帧反向，终帧才隐藏同一 WebView。
func (p *hoverPopup) hideOnOwnThread() {
	p.animateOnOwnThread(false)
}

// hideImmediatelyOnOwnThread 用于显式隐藏宠物/退出：取消 UI 动画，不销毁 backend。
func (p *hoverPopup) hideImmediatelyOnOwnThread() {
	if p.hwnd != 0 {
		winui.KillTimer(p.hwnd, hoverAnimationTimerID)
		winui.KillTimer(p.hwnd, hoverHideTimerID)
	}
	p.animationTimer = false
	now := uint64(0)
	if p.pet != nil {
		now = p.pet.nowMS()
	}
	p.animation.SetExpanded(false, now, true)
	if p.chromium != nil {
		_ = p.chromium.Hide()
	}
	if p.hwnd != 0 {
		winui.HideWindow(p.hwnd)
	}
	p.visible, p.frameApplied = false, false
	p.panelWindow, p.panelClip = winui.Rect{}, winui.Rect{}
}

// cleanup 释放窗口与 WebView2。只在浮窗线程上调用。
func (p *hoverPopup) cleanup() {
	p.closed.Store(true)
	p.commandHwnd.Store(0)
	winui.KillTimer(p.hwnd, hoverHideTimerID)
	winui.KillTimer(p.hwnd, hoverAnimationTimerID)
	p.animationTimer = false
	if p.pet != nil {
		p.pet.destroy()
		p.pet = nil
	}
	if p.chromium != nil {
		chromium := p.chromium
		chromium.ShuttingDown()
		chromium.MessageCallback = nil
		chromium.MessageWithAdditionalObjectsCallback = nil
		chromium.NavigationCompletedCallback = nil
		controller := chromium.GetController()
		if controller != nil {
			// v1.0.22 GetCoreWebView2 + AddRef 持有两份；本次 getter 临时持有第三份。
			webview, _ := controller.GetCoreWebView2()
			if err := winui.CloseWebViewController(controller); err != nil {
				p.logf("pet close controller: %v", err)
			}
			if webview != nil {
				webview.Release()
				webview.Release()
				webview.Release()
			}
			controller.Release()
		}
		winui.ReleaseCOMInterface(chromium.Environment())
		p.chromium = nil
	}
	if p.hwnd != 0 {
		hoverInstanceMu.Lock()
		delete(hoverInstances, p.hwnd)
		hoverInstanceMu.Unlock()
		winui.DestroyWindow(p.hwnd)
		p.hwnd = 0
	}
}

// ensureHoverClass 注册浮窗窗口类，进程内只注册一次。
func ensureHoverClass() error {
	hoverClassOnce.Do(func() {
		className, err := windows.UTF16PtrFromString(hoverWindowClass)
		if err != nil {
			hoverClassErr = err
			return
		}
		wc := winui.WndClassEx{
			Style:     winui.CSHRedraw | winui.CSVRedraw,
			WndProc:   windows.NewCallback(hoverWndProc),
			Instance:  winui.GetModuleHandle(),
			ClassName: className,
		}
		hoverClassErr = winui.RegisterClass(&wc)
	})
	return hoverClassErr
}

// hoverWndProc 是浮窗的窗口过程，运行在浮窗线程上。
func hoverWndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	hoverInstanceMu.RLock()
	p := hoverInstances[hwnd]
	hoverInstanceMu.RUnlock()

	if p != nil {
		switch msg {
		case hoverMsgShow:
			if p.pet != nil && !p.pet.hidden && !p.petHidden.Load() {
				p.pet.state.Pointer(desktoppet.Pet, true)
				p.pet.present()
			}
			return 0
		case hoverMsgStartPinned:
			p.enterStartPinned()
			return 0
		case hoverMsgSetUser:
			p.setUserOnOwnThread()
			return 0
		case hoverMsgSetSpaces:
			p.setSpacesOnOwnThread()
			return 0
		case hoverMsgDropStatus:
			p.setDropStatusOnOwnThread()
			return 0
		case hoverMsgSetTasks:
			p.setTasksOnOwnThread()
			return 0
		case hoverMsgSetPetUI:
			if p.pet != nil {
				p.pet.setVisibleOnOwnThread(!p.petHidden.Load())
			}
			return 0
		case hoverMsgMenuGuard:
			if p.pet != nil {
				p.pet.cancelInteraction()
				if p.menuGuarded() {
					p.pet.present()
				} else {
					p.pet.syncPointer(false)
				}
			}
			return 0
		case hoverMsgBeginDrag:
			p.beginDragOnOwnThread()
			return 0
		case hoverMsgHide:
			if p.pet != nil && !p.menuGuarded() && !p.pet.state.ExternalDrag && !p.pet.state.Dragging {
				p.pet.state.Collapse()
				p.pet.present()
			}
			return 0
		case hoverMsgScheduleHde:
			p.scheduleHideOnOwnThread()
			return 0
		case hoverMsgDestroy:
			winui.PostQuitMessage(0)
			return 0
		case winui.WMTimer:
			if wParam == hoverAnimationTimerID {
				p.onAnimationTimer()
				return 0
			}
			if wParam == hoverHideTimerID {
				p.onHideTimer()
				return 0
			}
		case winui.WMSize:
			p.resizeWebViewOnOwnThread()
			return 0
		case winui.WMDPIChanged:
			// 工作区 DPI/尺寸已由 showOnOwnThread 计算；不在 SetWindowPos 的同步回调中重入动画。
			p.resizeWebViewOnOwnThread()
			return 0
		case winui.WMNCHitTest:
			return winui.HTClient
		case winui.WMClose:
			if p.pet != nil {
				p.pet.state.Collapse()
				p.pet.present()
			}
			return 0
		}
	}
	return winui.DefWindowProc(hwnd, msg, wParam, lParam)
}

// trimJSONQuotes 去掉 WebView2 传来的 JSON 字符串字面量两端的引号。
func trimJSONQuotes(s string) string {
	var text string
	if json.Unmarshal([]byte(s), &text) == nil {
		return text
	}
	return s
}

// hoverSpaces 给悬浮窗切换器提供可选空间。
//
// 与挂载同一条判定：没配额的个人空间、没授权的共享空间都不列出——列出来让用户选，
// 选完拖了文件再被控制面拒掉，是最差的顺序。共享空间只读时列出但置灰，
// 让用户知道它存在、只是不能作为上传目标。
func (a *App) hoverSpaces() ([]hoverSpace, error) {
	return a.hoverSpacesWithContext(context.Background())
}

// hoverSpacesWithContext 保留旧内部列表接口；容量读取必须保留 quota=0 的原始空间行。
func (a *App) hoverSpacesWithContext(parent context.Context) ([]hoverSpace, error) {
	spaces, mounted, err := a.hoverAccountSpacesWithContext(parent)
	if err != nil {
		return nil, err
	}
	return projectHoverSpaces(spaces, mounted), nil
}

// hoverAccountSpacesWithContext 后台复用 MySpaces 与实际挂载判定，绝不在 UI 线程访问网络。
func (a *App) hoverAccountSpacesWithContext(parent context.Context) ([]account.Space, bool, error) {
	a.accountMu.RLock()
	session := a.accountSession
	base := strings.TrimSpace(a.config.PlatformBaseURL)
	a.accountMu.RUnlock()
	if session == nil || session.Token == "" || base == "" {
		return nil, false, fmt.Errorf("请先登录")
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	spaces, err := account.New(base).MySpaces(ctx, session.Token)
	if err != nil {
		return nil, false, err
	}
	a.accountMu.RLock()
	sameSession := a.accountSession == session && strings.TrimSpace(a.config.PlatformBaseURL) == base
	a.accountMu.RUnlock()
	if !sameSession {
		return nil, false, fmt.Errorf("账号已变化")
	}
	a.cfapi.mu.Lock()
	mounted := a.cfapi.personal != nil && a.cfapi.session == session
	a.cfapi.mu.Unlock()
	return spaces, mounted, nil
}

func projectHoverSpaces(spaces []account.Space, personalMounted bool) []hoverSpace {
	var result []hoverSpace
	for _, space := range spaces {
		if space.QuotaBytes == account.QuotaUnset {
			continue
		}
		switch space.SpaceType {
		case account.SpacePersonal:
			item := hoverSpace{Kind: account.SpacePersonal, Label: spaceLabel(drive.SpacePersonal), Quota: usageSubtitle(space), UsedBytes: space.UsedBytes, QuotaBytes: space.QuotaBytes, OwnerID: space.OwnerID}
			if !personalMounted {
				item.Unavailable = "云盘尚未连接，请等待挂载完成"
			}
			result = append(result, item)
		case account.SpaceShared:
			result = append(result, hoverSpace{Kind: account.SpaceShared, Label: spaceLabel(drive.SpaceShared), Quota: usageSubtitle(space), UsedBytes: space.UsedBytes, QuotaBytes: space.QuotaBytes, OwnerID: space.OwnerID, ReadOnly: space.Permission != account.PermWrite, Unavailable: "共享云盘暂不支持此入口"})
		}
	}
	return result
}

// hoverAccountKey 不包含昵称/令牌；服务器与稳定 UserID 共同隔离容量缓存。
type hoverAccountKey struct {
	Server string
	UserID string
}

// hoverCapacityView 把账号和容量一次性写入页面，防止新昵称短暂配上旧账号数字。
type hoverCapacityView struct {
	AccountVersion uint64  `json:"accountVersion"`
	UserID         string  `json:"userId"`
	Name           string  `json:"name"`
	LoggedIn       bool    `json:"loggedIn"`
	Known          bool    `json:"known"`
	UsedBytes      int64   `json:"usedBytes"`
	QuotaBytes     int64   `json:"quotaBytes"`
	UsedGB         string  `json:"usedGB"`
	QuotaGB        string  `json:"quotaGB"`
	Percent        float64 `json:"percent"`
	BarVisible     bool    `json:"barVisible"`
	State          string  `json:"state"`
	UsageText      string  `json:"usageText"`
	Hint           string  `json:"hint"`
	Stale          bool    `json:"stale"`
	Refreshing     bool    `json:"refreshing"`
	UpdatedAt      string  `json:"updatedAt,omitempty"`
	OpenEnabled    bool    `json:"openEnabled"`
	UploadEnabled  bool    `json:"uploadEnabled"`
}

// hoverCapacityState 只缓存当前账号最近一次有效投影，不持久化或混入任务百分比。
type hoverCapacityState struct {
	key      hoverAccountKey
	revision uint64
	view     hoverCapacityView
}

func hoverAccountIdentity(server, userID string) hoverAccountKey {
	return hoverAccountKey{Server: strings.TrimRight(strings.TrimSpace(server), "/"), UserID: strings.TrimSpace(userID)}
}

func (s *hoverCapacityState) begin(key hoverAccountKey, loggedIn bool, nickName, userName string) (revision uint64, changed bool) {
	changed = s.key != key || s.view.LoggedIn != loggedIn
	if changed {
		s.view = hoverCapacityView{}
		s.key = key
	}
	s.revision++
	s.view.UserID, s.view.LoggedIn = key.UserID, loggedIn
	s.view.Name = strings.TrimSpace(nickName)
	if s.view.Name == "" {
		s.view.Name = strings.TrimSpace(userName)
	}
	if !loggedIn {
		version := s.view.AccountVersion
		s.view = hoverCapacityView{AccountVersion: version, State: "signed-out", UsageText: "请先登录客户端", Hint: "个人云盘容量随当前账号更新"}
	} else if key.UserID == "" || key.Server == "" {
		s.view.Known, s.view.BarVisible = false, false
		s.view.State, s.view.UsageText, s.view.Hint = "unavailable", "容量暂不可用", "正在确认账号身份与服务器"
		s.view.OpenEnabled, s.view.UploadEnabled = false, false
	} else {
		s.view.Refreshing = true
		if !s.view.Known {
			s.view.State, s.view.UsageText, s.view.Hint = "loading", "正在读取个人容量…", "共享云盘暂不支持此入口"
		}
	}
	if changed {
		s.view.AccountVersion = s.revision
	}
	return s.revision, changed
}

func hoverGB(bytes int64) string {
	return fmt.Sprintf("%.2f", float64(bytes)/(1024*1024*1024))
}

// projectHoverCapacity 的百分比只来自 MySpaces 的 usedBytes/quotaBytes，超额数值不截断。
// 视觉条最多填满100%；0未开、-1不限均没有有意义的容量百分比。
func projectHoverCapacity(space account.Space, mounted bool) (hoverCapacityView, error) {
	if space.SpaceType != account.SpacePersonal || space.UsedBytes < 0 || space.QuotaBytes < account.QuotaUnlimited {
		return hoverCapacityView{}, fmt.Errorf("个人容量数据无效")
	}
	view := hoverCapacityView{Known: true, UsedBytes: space.UsedBytes, QuotaBytes: space.QuotaBytes,
		UsedGB: hoverGB(space.UsedBytes), QuotaGB: hoverGB(space.QuotaBytes), State: "finite"}
	view.OpenEnabled = mounted && space.QuotaBytes != account.QuotaUnset
	view.UploadEnabled = view.OpenEnabled
	switch space.QuotaBytes {
	case account.QuotaUnset:
		view.State, view.QuotaGB = "unset", ""
		view.UsageText = "已用 " + view.UsedGB + " GB / 未开通"
		view.Hint = "个人空间未分配容量，请联系管理员开通"
	case account.QuotaUnlimited:
		view.State, view.QuotaGB = "unlimited", ""
		view.UsageText = "已用 " + view.UsedGB + " GB / 不限"
		view.Hint = "不限容量 · 共享云盘暂不支持此入口"
	default:
		view.BarVisible = true
		view.Percent = float64(space.UsedBytes) / float64(space.QuotaBytes) * 100
		view.UsageText = "已用 " + view.UsedGB + " GB / " + view.QuotaGB + " GB"
		view.Hint = "个人云盘 · 共享云盘暂不支持此入口"
		if space.UsedBytes >= space.QuotaBytes {
			view.UploadEnabled = false
			view.Hint = "容量已满，请清理文件或联系管理员增加配额"
		}
		if space.UsedBytes > space.QuotaBytes {
			view.State = "overquota"
			view.Hint = "已超额 " + hoverGB(space.UsedBytes-space.QuotaBytes) + " GB，请清理文件或增加配额"
		}
	}
	if !mounted && space.QuotaBytes != account.QuotaUnset {
		view.Hint += " · 云盘尚未连接"
	}
	return view, nil
}

// complete 丢弃旧世代回包；离线/无效数据保留同账号有效值并标未更新，不能清零。
func (s *hoverCapacityState) complete(revision uint64, spaces []account.Space, mounted bool, readErr error, now time.Time) bool {
	if revision != s.revision || !s.view.LoggedIn || s.key.UserID == "" || s.key.Server == "" {
		return false
	}
	var personal *account.Space
	if readErr == nil {
		for i := range spaces {
			if spaces[i].SpaceType != account.SpacePersonal {
				continue
			}
			if personal != nil || (spaces[i].OwnerID != "" && spaces[i].OwnerID != s.key.UserID) {
				readErr = fmt.Errorf("个人空间与当前账号不匹配")
				break
			}
			personal = &spaces[i]
		}
		if personal == nil && readErr == nil {
			readErr = fmt.Errorf("尚无个人空间容量信息")
		}
	}
	var projected hoverCapacityView
	if readErr == nil {
		projected, readErr = projectHoverCapacity(*personal, mounted)
	}
	if readErr != nil {
		s.view.Refreshing = false
		s.view.OpenEnabled, s.view.UploadEnabled = false, false
		if s.view.Known {
			s.view.Stale = true
			s.view.Hint = "未更新 · 暂时无法读取服务器容量，保留本账号最近有效值"
		} else {
			s.view.State, s.view.UsageText, s.view.Hint = "error", "容量加载失败", "请检查网络与登录状态；没有本账号有效容量记录"
		}
		return true
	}
	projected.AccountVersion = s.view.AccountVersion
	projected.UserID, projected.Name, projected.LoggedIn = s.view.UserID, s.view.Name, true
	projected.UpdatedAt = now.UTC().Format(time.RFC3339)
	s.view = projected
	return true
}
