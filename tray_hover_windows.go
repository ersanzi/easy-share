//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"easyshare/internal/account"
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
	hoverHideDelayMS = 220

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
// hoverSpace 是浮窗切换器里的一档：一个该账号真正可用的空间。
//
// 只放能用的：没配额的个人空间、没授权的共享空间不进这个列表——列出来让用户选，
// 选完再被控制面拒掉，是最差的顺序。
type hoverSpace struct {
	// Kind 是 personal / shared，与 drive 包的空间常量一致。
	Kind string `json:"kind"`
	// Label 是档位文案，如「网盘」「共享」。
	Label string `json:"label"`
	// Quota 是配额那行文案，如「已用 1.2 GB / 10 GB」。
	Quota string `json:"quota"`
	// ReadOnly 的空间不能作上传目标，档位置灰。
	ReadOnly bool `json:"readOnly"`
}

type hoverPopup struct {
	hwnd     uintptr
	chromium *edge.Chromium
	width    int32

	// 两档高度（已按 DPI 缩放）：悬停态紧凑，固定态加高以容纳拖放区。
	heightHover  int32
	heightPinned int32

	// pinned 为固定态。它由浮窗线程写、托盘线程读（判断左键/右键是否收起浮窗），
	// 故用 atomic 保证跨线程读安全。
	pinned atomic.Bool

	// pointerInside 表示光标当前是否在浮窗内。它与托盘的 NIN_POPUPCLOSE 共同
	// 决定是否收起：只要光标进了浮窗就不收，否则鼠标永远走不到按钮上。
	// 只在浮窗线程读写。
	pointerInside bool
	visible       bool

	// pendingRect 是待弹出位置，由托盘线程写、浮窗线程读，故需加锁。
	pendingRect winui.Rect
	rectMu      sync.Mutex

	// 登录态（托盘线程写、浮窗线程读）：头像字符与昵称，随账号登录/登出更新。
	userMu     sync.Mutex
	userAvatar string
	userName   string

	// 空间列表与当前选中的目标空间（托盘线程写、浮窗线程读）。
	// activeSpace 决定拖入浮窗的文件上传到哪个空间。
	spaceMu     sync.Mutex
	spaces      []hoverSpace
	activeSpace string

	// dragged 记录用户是否手动拖过窗口。拖过之后就别再把它拽回右下角，
	// 否则每次显示都跳回原位，等于把用户的摆放丢掉。
	dragged atomic.Bool

	onOpenMain func()
	// onSpaceSelect 在用户切换目标空间时回调，供上层记住选择。
	onSpaceSelect func(kind string)
	// onFilesDropped 在用户往浮窗拖入文件时回调，参数是真实文件系统路径。
	onFilesDropped func(paths []string)
	// onOpenSpace 在用户点「打开」时回调，打开当前选中的空间。
	onOpenSpace func()
	logf        func(string, ...any)

	// startPinned 为 true 时，页面首次加载完成后浮窗自动进入固定态并停靠在
	// 桌面右下角（B 形态：双击 exe 即常驻）。
	startPinned bool
}

var (
	hoverClassOnce  sync.Once
	hoverClassErr   error
	hoverInstances  = map[uintptr]*hoverPopup{}
	hoverInstanceMu sync.RWMutex
)

// newHoverPopup 在独立线程上创建浮窗并等待其就绪。
// 返回 error 时调用方应继续提供「无浮窗」的托盘，而不是终止托盘本身。
func newHoverPopup(dpi int32, startPinned bool, onOpenMain func(), onSpaceSelect func(string), onFilesDropped func([]string), onOpenSpace func(), logf func(string, ...any)) (*hoverPopup, error) {
	scale := func(v int32) int32 { return v * dpi / 96 }
	p := &hoverPopup{
		width:          scale(hoverPopupWidth),
		heightHover:    scale(hoverPopupHeightHover),
		heightPinned:   scale(hoverPopupHeightPinned),
		startPinned:    startPinned,
		onOpenMain:     onOpenMain,
		onSpaceSelect:  onSpaceSelect,
		onFilesDropped: onFilesDropped,
		onOpenSpace:    onOpenSpace,
		logf:           logf,
	}

	ready := make(chan error, 1)
	go p.run(ready)
	if err := <-ready; err != nil {
		return nil, err
	}
	return p, nil
}

// curHeight 返回当前状态对应的窗口高度。
func (p *hoverPopup) curHeight() int32 {
	if p.pinned.Load() {
		return p.heightPinned
	}
	return p.heightHover
}

// run 是浮窗线程的主体：初始化、建窗、嵌入 WebView2，然后跑消息循环。
func (p *hoverPopup) run(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// WebView2 是 COM 组件，必须先在本线程初始化 COM。
	if err := winui.InitCOMForUIThread(); err != nil {
		ready <- err
		return
	}
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
		0, 0, p.width, p.heightHover, 0,
	)
	if err != nil {
		ready <- err
		return
	}
	p.hwnd = hwnd

	hoverInstanceMu.Lock()
	hoverInstances[hwnd] = p
	hoverInstanceMu.Unlock()

	if err := p.embedWebView(); err != nil {
		p.cleanup()
		ready <- err
		return
	}

	ready <- nil

	// 启动即固定必须在消息循环运行「之后」执行：WebView2 只有在循环开始泵消息后
	// 才会渲染，若在循环前同步显示窗口，窗口虽 visible 但完全透明（穿透到桌面）。
	// 因此投递消息，由下面的 RunMessageLoop 取出后再显示。
	if p.startPinned {
		winui.PostMessage(p.hwnd, hoverMsgStartPinned, 0, 0)
	}

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
	p.chromium.Resize()
	// 必须显式设为可见：嵌入场景下控制器的 IsVisible 不保证默认为真，
	// 不设时 WebView2 不会创建渲染子窗口，表现为浮窗完全透明（看到的是桌面）。
	if err := p.chromium.Show(); err != nil {
		return fmt.Errorf("webview2 显示失败: %w", err)
	}

	// 关掉 WebView2 自己的外部拖放：它默认接管拖放，把消息吃掉，
	// 于是宿主窗口的 WM_DROPFILES 永远收不到（这正是拖入无反应的原因）。
	// 关掉之后拖放落到宿主窗口，我们才能拿到真实文件路径。
	//
	// 旧版 Runtime 不支持该能力，返回 UnsupportedCapabilityError；此时拖放无法工作，
	// 但不该因此让浮窗整体不可用——只记日志。
	if err := p.chromium.AllowExternalDrag(false); err != nil {
		p.logf("hover popup: 关闭 WebView2 内建拖放失败，拖入上传可能无效: %v", err)
	}

	// 启动即固定（B 形态）：用 Init 注入的脚本在页面脚本运行前置好标志，页面 onload
	// 时自行套用固定样式——不依赖 NavigationCompleted 回调（该回调时序不可靠）。
	// 原生窗口的固定显示由 run() 在消息循环启动后投递 hoverMsgStartPinned 完成，
	// 不能在此同步显示（循环未运行时 WebView2 不渲染，窗口会透明）。
	if p.startPinned {
		p.chromium.Init("window.__esStartPinned = true;")
	}
	p.chromium.NavigateToString(hoverPopupHTML)
	return nil
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
	text := trimJSONQuotes(message)
	// 空间切换带参数，前缀匹配后单独处理
	if kind, ok := strings.CutPrefix(text, "space-select:"); ok {
		p.spaceMu.Lock()
		p.activeSpace = kind
		p.spaceMu.Unlock()
		if p.onSpaceSelect != nil {
			p.onSpaceSelect(kind)
		}
		return
	}
	switch text {
	case "begin-drag":
		// 必须投递到窗口线程再调：系统的拖窗循环要在拥有窗口的线程上跑
		winui.PostMessage(p.hwnd, hoverMsgBeginDrag, 0, 0)
	case "open-space":
		// 打开可能要等资源管理器起来，别占住浮窗线程
		if p.onOpenSpace != nil {
			go p.onOpenSpace()
		}
	case "open-main":
		// 固定态下点设置只打开主窗口，不收起浮窗；未固定则收起。
		if !p.pinned.Load() {
			p.hideOnOwnThread()
		}
		if p.onOpenMain != nil {
			p.onOpenMain()
		}
	case "pin-toggle":
		p.togglePinOnOwnThread()
	case "pointer-enter":
		p.pointerInside = true
		winui.KillTimer(p.hwnd, hoverHideTimerID)
	case "pointer-leave":
		p.pointerInside = false
		p.scheduleHideOnOwnThread()
	}
}

// --- 以下三个方法供托盘线程调用，只投递消息，不直接碰窗口 ---

// ShowNear 请求把浮窗显示在通知区域图标附近。
func (p *hoverPopup) ShowNear(iconRect winui.Rect) {
	if p.hwnd == 0 {
		return
	}
	p.rectMu.Lock()
	p.pendingRect = iconRect
	p.rectMu.Unlock()
	winui.PostMessage(p.hwnd, hoverMsgShow, 0, 0)
}

// Hide 请求立即收起浮窗。
func (p *hoverPopup) Hide() {
	if p.hwnd == 0 {
		return
	}
	winui.PostMessage(p.hwnd, hoverMsgHide, 0, 0)
}

// scheduleHide 请求延迟收起浮窗。
func (p *hoverPopup) scheduleHide() {
	if p.hwnd == 0 {
		return
	}
	winui.PostMessage(p.hwnd, hoverMsgScheduleHde, 0, 0)
}

// Destroy 请求销毁浮窗并结束其线程。
func (p *hoverPopup) Destroy() {
	if p.hwnd == 0 {
		return
	}
	winui.PostMessage(p.hwnd, hoverMsgDestroy, 0, 0)
}

// SetUser 更新悬浮窗展示的登录账号（头像字符 + 昵称）。托盘线程调用。
// 未登录时头像回落为「我」、昵称空。
func (p *hoverPopup) SetUser(loggedIn bool, nickName, userName string) {
	if p.hwnd == 0 {
		return
	}
	avatar := "我"
	name := ""
	if loggedIn {
		display := nickName
		if display == "" {
			display = userName
		}
		if r := []rune(display); len(r) > 0 {
			avatar = string(r[0])
			name = display
		}
	}
	p.userMu.Lock()
	p.userAvatar = avatar
	p.userName = name
	p.userMu.Unlock()
	winui.PostMessage(p.hwnd, hoverMsgSetUser, 0, 0)
}

// SetSpaces 更新可切换的空间列表。托盘线程调用。
//
// 传空列表即隐藏切换器——未登录、或该账号一个可用空间都没有时就是这种情况。
func (p *hoverPopup) SetSpaces(spaces []hoverSpace) {
	if p.hwnd == 0 {
		return
	}
	p.spaceMu.Lock()
	p.spaces = spaces
	// 当前选中的空间如果已经不在列表里（配额被收回、授权被撤销），清掉让页面重选
	stillThere := false
	for _, space := range spaces {
		if space.Kind == p.activeSpace && !space.ReadOnly {
			stillThere = true
			break
		}
	}
	if !stillThere {
		p.activeSpace = ""
	}
	p.spaceMu.Unlock()
	winui.PostMessage(p.hwnd, hoverMsgSetSpaces, 0, 0)
}

// ActiveSpace 返回当前选中的目标空间，未选时为空串。托盘线程与主线程都可读。
func (p *hoverPopup) ActiveSpace() string {
	p.spaceMu.Lock()
	defer p.spaceMu.Unlock()
	return p.activeSpace
}

// --- 以下方法只在浮窗线程上执行 ---

// showOnOwnThread 按 pendingRect 定位并显示浮窗。
func (p *hoverPopup) showOnOwnThread() {
	// 已固定时窗口常驻，悬停图标不应打断或移动它。
	if p.pinned.Load() && p.visible {
		return
	}
	winui.KillTimer(p.hwnd, hoverHideTimerID)

	// 用户手动摆过位置：只管显示，不再重新定位。
	// 否则每次弹出都跳回托盘图标旁，等于把用户的摆放丢掉。
	if p.dragged.Load() {
		winui.ShowWindowNoActivate(p.hwnd)
		p.visible = true
		return
	}

	p.rectMu.Lock()
	iconRect := p.pendingRect
	p.rectMu.Unlock()

	work := winui.WorkAreaForRect(iconRect)
	if work.Width() == 0 || work.Height() == 0 {
		p.logf("hover popup: 取工作区失败，跳过本次弹出")
		return
	}

	const gap = 8
	h := p.curHeight()
	x, y := winui.PopupPosition(iconRect, work, p.width, h, gap)
	winui.MoveWindowTopMost(p.hwnd, x, y, p.width, h)
	winui.ShowWindowNoActivate(p.hwnd)
	if p.chromium != nil {
		p.chromium.Resize()
	}
	p.visible = true
}

// togglePinOnOwnThread 切换固定态。
//
// 固定：加高到 pinned 高度并重新定位到右下角，保持常驻。
// 取消固定：**不重定位、不缩放、不立即隐藏**。因为此刻鼠标正停在固定按钮上，
// 一旦缩小/移动窗口，窗口会从光标下方挪走，触发「光标不在窗内」误判而立即消失
// （这是之前的 bug）。改为只切回悬停样式并标记指针在窗内，等用户真正移开鼠标时
// 再按常规悬停逻辑收起；下次悬停会以悬停高度正常弹出。
func (p *hoverPopup) togglePinOnOwnThread() {
	pinned := !p.pinned.Load()
	p.pinned.Store(pinned)
	winui.KillTimer(p.hwnd, hoverHideTimerID)

	// 固定态去掉 WS_EX_NOACTIVATE，换取「能拖入文件」。
	//
	// 带该标志的窗口不可激活，Windows 的拖放循环就不会把它当作放置目标——
	// HTML5 的 drop 事件与 WM_DROPFILES 都收不到。本机对照可证：能正常接收拖放的
	// 桌面挂件（Nexus、PowerToys QuickAccess）以及 Wails 主窗口都不带此标志。
	//
	// 悬停态仍然保留它：那时浮窗是被动弹出的，抢焦点会打断用户正在进行的输入。
	winui.SetNoActivate(p.hwnd, !pinned)

	if pinned {
		// 已被手动摆放过：原地加高，不重定位
		if p.dragged.Load() {
			rect := winui.GetWindowRect(p.hwnd)
			winui.MoveWindowTopMost(p.hwnd, rect.Left, rect.Top, p.width, p.heightPinned)
			if p.chromium != nil {
				p.chromium.Resize()
			}
			winui.ShowWindowNoActivate(p.hwnd)
			p.visible = true
			p.pointerInside = true
			p.applyPinnedToPage(true)
			return
		}
		p.rectMu.Lock()
		iconRect := p.pendingRect
		p.rectMu.Unlock()
		work := winui.WorkAreaForRect(iconRect)
		if work.Width() != 0 && work.Height() != 0 {
			const gap = 8
			x, y := winui.PopupPosition(iconRect, work, p.width, p.heightPinned, gap)
			winui.MoveWindowTopMost(p.hwnd, x, y, p.width, p.heightPinned)
			if p.chromium != nil {
				p.chromium.Resize()
			}
		}
		p.applyPinnedToPage(true)
		return
	}

	// 取消固定：保持窗口原样可见，只切回悬停样式。指针视为在窗内，
	// 由后续 mouseleave → pointer-leave 触发常规延迟收起。
	p.pointerInside = true
	p.applyPinnedToPage(false)
}

// SetDropStatus 把上传状态写回浮窗的拖放区。
//
// kind 取 "busy" / "ok" / "err"，空串恢复默认样式。可从任意线程调用。
func (p *hoverPopup) SetDropStatus(title, hint, kind string) {
	if p.chromium == nil {
		return
	}
	// 文案含文件名与错误信息，走 JSON 免得引号把 JS 搞坏
	payload, err := json.Marshal([]string{title, hint, kind})
	if err != nil {
		return
	}
	p.chromium.Eval(fmt.Sprintf(
		"window.applyDropStatus && window.applyDropStatus(%s[0],%s[1],%s[2])",
		payload, payload, payload))
}

// applyPinnedToPage 把固定态同步给页面（驱动按钮高亮、拖放区与拖动把手显隐）。
func (p *hoverPopup) applyPinnedToPage(pinned bool) {
	if p.chromium == nil {
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

// enterStartPinned 让浮窗以固定态停靠在主屏右下角（B 形态）。
// 在浮窗线程、NavigateToString 之后立即调用。固定样式由页面依据注入标志自行套用，
// 这里只负责原生窗口：置固定标志、定位右下角、显示。
func (p *hoverPopup) enterStartPinned() {
	p.pinned.Store(true)
	// 与 togglePinOnOwnThread 同理：固定态必须去掉 NOACTIVATE 才能接收拖放。
	// 这条路径不经过 toggle，漏掉这行会导致「启动即固定」时拖不进去。
	winui.SetNoActivate(p.hwnd, false)

	work := winui.GetPrimaryWorkArea()
	if work.Width() <= 0 || work.Height() <= 0 {
		p.logf("hover popup: 取主屏工作区失败，启动固定跳过")
		return
	}
	const gap = 12
	x, y := winui.BottomRightPosition(work, p.width, p.heightPinned, gap)
	winui.MoveWindowTopMost(p.hwnd, x, y, p.width, p.heightPinned)
	winui.ShowWindowNoActivate(p.hwnd)
	if p.chromium != nil {
		p.chromium.Resize()
	}
	p.visible = true
	p.logf("hover popup: 启动固定于右下角 (%d,%d) %dx%d", x, y, p.width, p.heightPinned)
}

// scheduleHideOnOwnThread 启动延迟收起。延迟期间光标若进入浮窗，
// handleWebMessage 的 pointer-enter 会取消定时器。固定态下不收起。
func (p *hoverPopup) scheduleHideOnOwnThread() {
	if !p.visible || p.pinned.Load() {
		return
	}
	winui.SetTimer(p.hwnd, hoverHideTimerID, hoverHideDelayMS)
}

// onHideTimer 在延迟到期时决定是否真的收起。
// 这里再查一次光标位置：页面的 mouseleave 在快速移动时可能没来得及送达。
func (p *hoverPopup) onHideTimer() {
	winui.KillTimer(p.hwnd, hoverHideTimerID)
	if p.pinned.Load() || p.pointerInside {
		return
	}
	cursor := winui.GetCursorPos()
	if winui.GetWindowRect(p.hwnd).Contains(cursor.X, cursor.Y) {
		return
	}
	p.hideOnOwnThread()
}

// setUserOnOwnThread 把当前登录账号写进页面（头像字符 + 昵称）。仅浮窗线程调用。
func (p *hoverPopup) setUserOnOwnThread() {
	if p.chromium == nil {
		return
	}
	p.userMu.Lock()
	avatar := p.userAvatar
	name := p.userName
	p.userMu.Unlock()
	if avatar == "" {
		avatar = "我"
	}
	p.chromium.Eval(fmt.Sprintf("window.applyUser && window.applyUser(%q,%q)", avatar, name))
}

// setSpacesOnOwnThread 把空间列表写进页面。仅浮窗线程调用。
func (p *hoverPopup) setSpacesOnOwnThread() {
	if p.chromium == nil {
		return
	}
	p.spaceMu.Lock()
	spaces := p.spaces
	active := p.activeSpace
	p.spaceMu.Unlock()

	// 走 JSON 而不是拼字符串：档位文案含用户昵称与配额数字，拼接容易被引号搞坏
	payload, err := json.Marshal(spaces)
	if err != nil {
		p.logf("hover marshal spaces: %v", err)
		return
	}
	selected, err := json.Marshal(active)
	if err != nil {
		p.logf("hover marshal active space: %v", err)
		return
	}
	p.chromium.Eval(fmt.Sprintf("window.applySpaces && window.applySpaces(%s,%s)", payload, selected))
}

// beginDragOnOwnThread 把拖动交给系统。仅浮窗线程调用。
func (p *hoverPopup) beginDragOnOwnThread() {
	// 拖动期间不能让自动隐藏把窗口收走
	winui.KillTimer(p.hwnd, hoverHideTimerID)
	p.dragged.Store(true)
	winui.BeginWindowDrag(p.hwnd)
}

// handleDroppedObjects 从 WebView2 的「附加对象」里取出被拖入文件的真实路径。
//
// 为什么必须这样绕一圈：WebView2 会建一个子窗口盖住整个客户区，拖放落在子窗口上，
// 宿主窗口的 WM_DROPFILES 永远收不到（试过，收不到）。而页面里的 HTML5
// DataTransfer 出于安全只给文件名、不给完整路径。
//
// WebView2 为此提供了 postMessageWithAdditionalObjects：页面把 File 对象随消息一起
// 传过来，原生侧拿到 ICoreWebView2File 就能读 Path。这也是 Wails 主窗口拖放的做法。
func (p *hoverPopup) handleDroppedObjects(message string, _ *edge.ICoreWebView2, args *edge.ICoreWebView2WebMessageReceivedEventArgs) {
	if trimJSONQuotes(message) != dropMessage {
		return
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
	// 上传要回控制面且可能很久，绝不能占着这个回调——那会让整个浮窗卡住
	go p.onFilesDropped(paths)
}

// hideOnOwnThread 立即收起浮窗。
func (p *hoverPopup) hideOnOwnThread() {
	winui.KillTimer(p.hwnd, hoverHideTimerID)
	winui.HideWindow(p.hwnd)
	p.visible = false
	p.pointerInside = false
}

// cleanup 释放窗口与 WebView2。只在浮窗线程上调用。
func (p *hoverPopup) cleanup() {
	if p.chromium != nil {
		p.chromium.ShuttingDown()
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
			p.showOnOwnThread()
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
		case hoverMsgBeginDrag:
			p.beginDragOnOwnThread()
			return 0
		case hoverMsgHide:
			p.hideOnOwnThread()
			return 0
		case hoverMsgScheduleHde:
			p.scheduleHideOnOwnThread()
			return 0
		case hoverMsgDestroy:
			winui.PostQuitMessage(0)
			return 0
		case winui.WMTimer:
			if wParam == hoverHideTimerID {
				p.onHideTimer()
				return 0
			}
		case winui.WMClose:
			// 浮窗不接受外部关闭请求，只隐藏，生命周期由托盘掌握。
			p.hideOnOwnThread()
			return 0
		}
	}
	return winui.DefWindowProc(hwnd, msg, wParam, lParam)
}

// trimJSONQuotes 去掉 WebView2 传来的 JSON 字符串字面量两端的引号。
func trimJSONQuotes(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// hoverSpaces 给悬浮窗切换器提供可选空间。
//
// 与挂载同一条判定：没配额的个人空间、没授权的共享空间都不列出——列出来让用户选，
// 选完拖了文件再被控制面拒掉，是最差的顺序。共享空间只读时列出但置灰，
// 让用户知道它存在、只是不能作为上传目标。
func (a *App) hoverSpaces() ([]hoverSpace, error) {
	spaces, err := a.MySpaces()
	if err != nil {
		return nil, err
	}
	var result []hoverSpace
	for _, space := range spaces {
		if space.QuotaBytes == account.QuotaUnset {
			continue
		}
		switch space.SpaceType {
		case account.SpacePersonal:
			result = append(result, hoverSpace{
				Kind:  account.SpacePersonal,
				Label: spaceLabel(drive.SpacePersonal),
				Quota: usageSubtitle(space),
			})
		case account.SpaceShared:
			result = append(result, hoverSpace{
				Kind:     account.SpaceShared,
				Label:    spaceLabel(drive.SpaceShared),
				Quota:    usageSubtitle(space),
				ReadOnly: space.Permission != account.PermWrite,
			})
		}
	}
	return result, nil
}
