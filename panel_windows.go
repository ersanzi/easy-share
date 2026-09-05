//go:build windows

package main

// 快捷面板窗口（Windows 实现）：一个自建的 Win32 弹出窗口内嵌 WebView2，加载
// 剪切板插件的 ?panel=1 页面。线程模型同托盘悬浮窗（tray_hover_windows.go）：
// 独占一个 OS 线程完成 COM 初始化、建窗、嵌 WebView2 并跑消息循环；全局热键
// （RegisterHotKey）注册在该线程上，WM_HOTKEY 直达面板线程，免跨线程转投。
//
// 与悬浮窗的关键行为差异：面板**要抢焦点**——Win+V 弹出后搜索框即打即搜、
// ↑↓/Enter 键盘操作、失焦即收起，这是「先复制后粘贴」工作流的载体。
//
// 自动粘贴序列：面板内插件成功 clipboard.write（=用户选中条目）→ 收起面板 →
// 延迟数十毫秒待系统完成焦点交接 → SetForegroundWindow 切回唤起前的焦点窗口 →
// 焦点确认切回后合成 Ctrl+V。焦点没切回去就不注入按键，宁可只复制不粘贴，
// 也不能把粘贴打进错误的窗口。

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"

	"easyshare/internal/winui"

	"github.com/wailsapp/go-webview2/pkg/edge"
	"golang.org/x/sys/windows"
)

const (
	panelWindowClass = "EasyShareClipPanel"

	// 面板窗口自己的控制消息。来自其他线程（如未来托盘入口）的请求经
	// PostMessage 转到面板线程执行；本实现内 MessageCallback/WM_HOTKEY 已在
	// 面板线程上，可直接操作窗口。
	panelMsgPaste   = winui.WMApp + 302
	panelMsgDestroy = winui.WMApp + 303

	// 粘贴延迟定时器：收起面板后给目标窗口留出激活时间再注入 Ctrl+V。
	panelPasteTimerID = 2
	panelPasteDelayMS = 80
)

type clipPanel struct {
	a *App

	// kind 表面类型：clip=剪切板快捷面板（既有）；search=全局搜索面板（2026-09-06）。
	// URL/尺寸/热键链都按 kind 取自 panelSurfaceSpec。
	kind string

	hwnd     uintptr
	chromium *edge.Chromium

	width  int32
	height int32

	// 以下两个仅面板线程读写。
	visible    bool
	foreground uintptr // 热键唤起时的前台窗口（自动粘贴的目标）

	hotkeyID   int32
	hotkeyMods uint32
	hotkeyVK   uint32
}

var (
	panelClassOnce  sync.Once
	panelClassErr   error
	panelInstances  = map[uintptr]*clipPanel{}
	panelInstanceMu sync.RWMutex

	// panelAlive 面板线程存活标记：startPanel 幂等守卫，cleanup 时清除。
	// 卸载剪切板插件时 stopPanel 销毁面板线程，重装后 startPanel 可再建。
	panelAlive atomic.Bool

	// searchPanelAlive 搜索面板存活标记（独立表面独立守卫）。
	searchPanelAlive atomic.Bool
)

// panelSurfaceSpec 面板表面的窗口标题/尺寸/URL/热键链差异全部收拢在此。
type panelSurfaceSpec struct {
	title      string
	width      int32
	height     int32
	hotkeyID   int32
	candidates []struct {
		mods uint32
		vk   uint32
		text string
	}
}

func (p *clipPanel) spec() panelSurfaceSpec {
	if p.kind == "search" {
		return panelSurfaceSpec{
			title:    "搜索",
			width:    searchSurfaceWidth,
			height:   searchSurfaceHeight,
			hotkeyID: 1,
			candidates: []struct {
				mods uint32
				vk   uint32
				text string
			}{
				// Alt+Space 是 Windows 窗口系统菜单键，全局注册会把它抢走——
				// 但那正是 Everything/uTools 们的既定惯例（呼出价值大于菜单键），
				// 被占时回退组合键，回退结果同样在页面底部展示
				{winui.ModAlt | winui.ModNoRepeat, VKSpace, "Alt+Space"},
				{winui.ModControl | winui.ModAlt | winui.ModNoRepeat, VKSpace, "Ctrl+Alt+Space"},
				{winui.ModWin | winui.ModAlt | winui.ModNoRepeat, VKSpace, "Win+Alt+Space"},
			},
		}
	}
	return panelSurfaceSpec{
		title:    "剪切板",
		width:    panelWidth,
		height:   panelHeight,
		hotkeyID: 1,
		candidates: []struct {
			mods uint32
			vk   uint32
			text string
		}{
			{winui.ModWin | winui.ModNoRepeat, winui.VKV, "Win+V"},
			{winui.ModWin | winui.ModAlt | winui.ModNoRepeat, winui.VKV, "Win+Alt+V"},
			{winui.ModControl | winui.ModAlt | winui.ModNoRepeat, winui.VKV, "Ctrl+Alt+V"},
			{winui.ModAlt | winui.ModShift | winui.ModNoRepeat, winui.VKV, "Alt+Shift+V"},
		},
	}
}

// VKSpace Space 键虚拟码（winui 包未收录）。
const VKSpace = 0x20

// startPanel 拉起快捷面板（独立线程，失败只记日志不阻断主程序）。
// 幂等：面板已存活时不重复创建。
func startPanel(a *App) {
	if a.panelURL == "" {
		a.logger.Printf("panel: 静态服务未就绪，跳过面板启动")
		return
	}
	if !panelAlive.CompareAndSwap(false, true) {
		return // 已在运行
	}
	p := &clipPanel{a: a, kind: "clip", hotkeyID: 1}
	ready := make(chan error, 1)
	go p.run(ready)
	if err := <-ready; err != nil {
		a.logger.Printf("panel: %v", err)
	}
}

// startSearchPanel 拉起全局搜索面板（独立线程窗口；失败只记日志）。
// 与剪切板面板互不隶属：搜索表面不随插件启停，登录后常驻热键。
func startSearchPanel(a *App) {
	ensurePanelServer(a)
	if a.searchURL == "" {
		return
	}
	if !searchPanelAlive.CompareAndSwap(false, true) {
		return
	}
	p := &clipPanel{a: a, kind: "search", hotkeyID: 1}
	ready := make(chan error, 1)
	go p.run(ready)
	if err := <-ready; err != nil {
		a.logger.Printf("search panel: %v", err)
	}
}

// stopSearchPanel 销毁搜索面板（退出登录等场景）。
func stopSearchPanel(a *App) {
	panelInstanceMu.RLock()
	var target *clipPanel
	for _, cand := range panelInstances {
		if cand.kind == "search" {
			target = cand
			break
		}
	}
	panelInstanceMu.RUnlock()
	if target != nil {
		winui.PostMessage(target.hwnd, panelMsgDestroy, 0, 0)
	}
}

// stopPanel 销毁面板线程（注销热键、关窗、结束 WebView2）。
// 插件被卸载/禁用时调用；重装后 startPanel 会重建。
func stopPanel(a *App) {
	panelInstanceMu.RLock()
	var p *clipPanel
	for _, cand := range panelInstances {
		if cand.kind == "clip" {
			p = cand
			break
		}
	}
	panelInstanceMu.RUnlock()
	if p != nil {
		winui.PostMessage(p.hwnd, panelMsgDestroy, 0, 0)
	}
	a.panelEmitMu.Lock()
	a.panelEmit = nil
	a.panelEmitMu.Unlock()
}

func (p *clipPanel) run(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// WebView2 是 COM 组件，必须先在本线程初始化 COM。
	if err := winui.InitCOMForUIThread(); err != nil {
		ready <- err
		return
	}
	if err := ensurePanelClass(); err != nil {
		ready <- err
		return
	}

	spec := p.spec()
	// WS_EX_TOOLWINDOW：不出现在任务栏和 Alt-Tab。初始给非零尺寸——WebView2
	// 嵌入阶段对零尺寸父窗口会报「参数不正确」，DPI 缩放后再挪到屏幕外待命。
	hwnd, err := winui.CreateWindow(
		winui.WSExToolWindow|winui.WSExTopMost,
		panelWindowClass, spec.title,
		winui.WSPopup|winui.WSClipChildren,
		0, 0, spec.width, spec.height, 0,
	)
	if err != nil {
		ready <- err
		return
	}
	p.hwnd = hwnd

	dpi := winui.DpiForWindow(hwnd)
	scale := func(v int32) int32 { return v * dpi / 96 }
	p.width = scale(spec.width)
	p.height = scale(spec.height)
	winui.MoveWindow(hwnd, -32000, -32000, p.width, p.height)

	panelInstanceMu.Lock()
	panelInstances[hwnd] = p
	panelInstanceMu.Unlock()

	if err := p.embedWebView(); err != nil {
		p.cleanup()
		ready <- err
		return
	}
	// 全局热键回退链：优先 Win+V（与系统剪贴板历史同键——历史开着就抢不到，
	// 抢到了则正是本功能的意图：顶替系统面板）。回退键刻意避开高频组合：
	// Win+Shift+V（实测被占）与 Ctrl+Shift+V（浏览器/Office 的「粘贴为纯文本」，
	// 全局注册会把它从所有应用手里抢走）都不进链。选中的组合会作为 hk 参数带给
	// 面板页展示（面板底部显示实际生效快捷键），避免静默回退后用户找不到入口。
	hotkeyCandidates := spec.candidates
	hotkeyText := ""
	for _, cand := range hotkeyCandidates {
		if err := winui.RegisterHotKey(p.hwnd, p.hotkeyID, cand.mods, cand.vk); err == nil {
			p.hotkeyMods, p.hotkeyVK = cand.mods, cand.vk
			hotkeyText = cand.text
			if cand.text == "Win+V" {
				p.a.logger.Printf("panel: 全局热键 %s", cand.text)
			} else {
				p.a.logger.Printf("panel: Win+V 被占用（Windows 剪贴板历史或其他软件），全局热键回退为 %s", cand.text)
			}
			break
		}
	}
	if hotkeyText == "" {
		p.a.logger.Printf("panel: 全局热键全部注册失败，快捷面板将无法用热键唤起")
	}

	// 加载面板页：带上实际注册到的热键（面板底部展示），静默回退不再让用户摸不着头脑。
	navigateURL := p.a.panelURL
	if p.kind == "search" {
		navigateURL = p.a.searchURL
	}
	p.chromium.Navigate(navigateURL + "&hk=" + url.QueryEscape(hotkeyText))

	// 事件通道：剪切板面板注入事件流；搜索面板注入 Eval 通道（异步回投结果）。
	if p.kind == "clip" {
		p.a.panelEmitMu.Lock()
		p.a.panelEmit = p.emitEvent
		p.a.panelEmitMu.Unlock()
	} else {
		p.a.searchEmitMu.Lock()
		p.a.searchEmit = p.evalScript
		p.a.searchEmitMu.Unlock()
	}

	ready <- nil
	winui.RunMessageLoop()
	if p.kind == "search" {
		searchPanelAlive.Store(false)
	} else {
		panelAlive.Store(false)
	}
	p.cleanup()
}

// embedWebView 嵌入 WebView2 并加载插件面板页。go-webview2 的 Embed 无论成败
// 都返回 true，真实错误只经 errorCallback 上报，故用 embedErr 收集判定。
func (p *clipPanel) embedWebView() error {
	var embedErr error
	chromium := edge.NewChromium()

	// 独立用户数据目录：与悬浮窗同理，同一目录配不同选项会创建失败。
	dataPath, err := panelWebViewDataPath()
	if err != nil {
		return err
	}
	chromium.DataPath = dataPath
	chromium.MessageCallback = p.handleWebMessage
	chromium.SetErrorCallback(func(e error) {
		if embedErr == nil {
			embedErr = e
		}
		p.a.logger.Printf("panel webview error: %v", e)
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
	if err := p.chromium.Show(); err != nil {
		return fmt.Errorf("webview2 显示失败: %w", err)
	}
	// Navigate 挪到 run() 热键注册之后：URL 需要带上实际生效的热键参数。
	return nil
}

// panelWebViewDataPath 返回面板专用的 WebView2 用户数据目录。
// 与悬浮窗同理：必须与 Wails 主窗口的目录分开，同一目录配不同选项会创建失败。
func panelWebViewDataPath() (string, error) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		return "", fmt.Errorf("LOCALAPPDATA 未设置")
	}
	dir := filepath.Join(base, "EasyShare", "webview2-panel")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// handleWebMessage 处理面板页发来的消息（能力 RPC / 窗口控制）。
// 该回调在面板线程上触发（消息泵分发），可直接操作窗口与 Eval。
func (p *clipPanel) handleWebMessage(message string, _ *edge.ICoreWebView2, _ *edge.ICoreWebView2WebMessageReceivedEventArgs) {
	replyJS, dismiss, paste := panelProcessMessage(p.a, message)
	if replyJS != "" {
		p.chromium.Eval(replyJS)
	}
	if paste {
		// 选中条目：收起 + 延迟回贴。走消息投递，保持回调尽快返回。
		winui.PostMessage(p.hwnd, panelMsgPaste, 0, 0)
		return
	}
	if dismiss {
		p.hideOnOwnThread()
	}
}

// evalScript 在面板页执行任意脚本；可见性守卫与 emitEvent 相同。任意线程可调。
func (p *clipPanel) evalScript(script string) {
	if p.chromium == nil || !p.visible {
		return
	}
	p.chromium.Eval(script)
}

// emitEvent 向面板页推事件（clipboard:changed、panel:shown）。可从任意线程调用
// （与悬浮窗 SetDropStatus 同一先例：Eval 线程安全）。
// 面板不可见时不 Eval：ExecuteScript 在导航未完成等时机会返回参数错误，
// 且不可见时推事件本来就没有意义。
func (p *clipPanel) emitEvent(event string, payload any) {
	if p.chromium == nil || !p.visible {
		return
	}
	if s := panelEventScript(event, payload); s != "" {
		p.chromium.Eval(s)
	}
}

// showOnOwnThread 贴着光标弹出面板并接管焦点。仅面板线程调用。
func (p *clipPanel) showOnOwnThread() {
	if p.visible {
		return
	}
	// 记录唤起前的前台窗口：自动粘贴的落点。若是本进程窗口则粘贴序列会自行跳过。
	p.foreground = winui.GetForegroundWindow()

	pos := winui.GetCursorPos()
	anchor := winui.Rect{Left: pos.X, Top: pos.Y, Right: pos.X + 1, Bottom: pos.Y + 1}
	if work := winui.WorkAreaForRect(anchor); work.Width() > 0 && work.Height() > 0 {
		x, y := clampToWork(pos.X+12, pos.Y+12, p.width, p.height, work)
		winui.MoveWindowTopMost(p.hwnd, x, y, p.width, p.height)
	}
	winui.ShowWindowActivate(p.hwnd)
	winui.SetForegroundWindow(p.hwnd)
	if p.chromium != nil {
		p.chromium.Focus()
	}
	p.visible = true
	p.emitEvent("panel:shown", nil)
}

// hideOnOwnThread 收起面板。仅面板线程调用。
func (p *clipPanel) hideOnOwnThread() {
	winui.HideWindow(p.hwnd)
	p.visible = false
}

// toggleOnOwnThread 热键开关面板。仅面板线程调用。
func (p *clipPanel) toggleOnOwnThread() {
	if p.visible {
		p.hideOnOwnThread()
		return
	}
	p.showOnOwnThread()
}

// pasteOnOwnThread 启动自动粘贴序列：先收起，再给目标窗口留激活时间。仅面板线程调用。
// 注意：foreground 在定时器回调用完之前不能清零（粘贴目标靠它传递）。
func (p *clipPanel) pasteOnOwnThread() {
	p.hideOnOwnThread()
	prev := p.foreground
	p.a.logger.Printf("panel: 粘贴序列开始，prev=0x%x", prev)
	if prev == 0 || prev == p.hwnd {
		p.a.logger.Printf("panel: 粘贴跳过（目标无效）")
		p.foreground = 0
		return
	}
	// 粘贴目标若是本进程窗口（如主窗口），把内容粘进自己没有意义。
	if _, pid := winui.GetWindowThreadProcessId(prev); pid == windows.GetCurrentProcessId() {
		p.a.logger.Printf("panel: 粘贴跳过（目标是本进程窗口）")
		p.foreground = 0
		return
	}
	winui.SetTimer(p.hwnd, panelPasteTimerID, panelPasteDelayMS)
}

// pasteTimerOnOwnThread 焦点切回目标窗口后合成 Ctrl+V。仅面板线程调用。
func (p *clipPanel) pasteTimerOnOwnThread() {
	winui.KillTimer(p.hwnd, panelPasteTimerID)
	prev := p.foreground
	p.foreground = 0
	if prev == 0 {
		return
	}
	winui.SetForegroundWindow(prev)
	now := winui.GetForegroundWindow()
	p.a.logger.Printf("panel: 粘贴定时器触发，prev=0x%x now=0x%x", prev, now)
	if now == prev {
		winui.SendCtrlV()
		p.a.logger.Printf("panel: 已注入 Ctrl+V")
	} else {
		p.a.logger.Printf("panel: 焦点未切回，放弃粘贴（宁缺毋滥）")
	}
}

// cleanup 释放窗口与 WebView2。只在面板线程上调用。
func (p *clipPanel) cleanup() {
	if p.chromium != nil {
		p.chromium.ShuttingDown()
		p.chromium = nil
	}
	if p.hwnd != 0 {
		if p.hotkeyVK != 0 {
			winui.UnregisterHotKey(p.hwnd, p.hotkeyID)
			p.hotkeyVK = 0
		}
		panelInstanceMu.Lock()
		delete(panelInstances, p.hwnd)
		panelInstanceMu.Unlock()
		winui.DestroyWindow(p.hwnd)
		p.hwnd = 0
	}
}

// clampToWork 把「光标右下角弹出」的位置收回显示器工作区内。
func clampToWork(x, y, w, h int32, work winui.Rect) (int32, int32) {
	if x+w > work.Right {
		x = work.Right - w
	}
	if y+h > work.Bottom {
		y = work.Bottom - h
	}
	if x < work.Left {
		x = work.Left
	}
	if y < work.Top {
		y = work.Top
	}
	return x, y
}

// ensurePanelClass 注册面板窗口类，进程内只注册一次。
func ensurePanelClass() error {
	panelClassOnce.Do(func() {
		className, err := windows.UTF16PtrFromString(panelWindowClass)
		if err != nil {
			panelClassErr = err
			return
		}
		wc := winui.WndClassEx{
			Style:     winui.CSHRedraw | winui.CSVRedraw,
			WndProc:   windows.NewCallback(panelWndProc),
			Instance:  winui.GetModuleHandle(),
			ClassName: className,
		}
		panelClassErr = winui.RegisterClass(&wc)
	})
	return panelClassErr
}

// panelWndProc 是面板的窗口过程，运行在面板线程上。
func panelWndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	panelInstanceMu.RLock()
	p := panelInstances[hwnd]
	panelInstanceMu.RUnlock()

	if p != nil {
		switch msg {
		case winui.WMHotkey:
			if wParam == uintptr(p.hotkeyID) {
				p.toggleOnOwnThread()
			}
			return 0
		case panelMsgPaste:
			p.pasteOnOwnThread()
			return 0
		case panelMsgDestroy:
			winui.PostQuitMessage(0)
			return 0
		case winui.WMTimer:
			if wParam == panelPasteTimerID {
				p.pasteTimerOnOwnThread()
				return 0
			}
		case winui.WMActivate:
			// 失去激活即收起：点别处、切窗口，面板就该走（同 Win+V 习惯）。
			// 自身的粘贴序列先收起后切焦点，此处重复收起无害（visible 已为 false）。
			if uint32(wParam)&0xffff == winui.WAInactive && p.visible {
				p.hideOnOwnThread()
			}
		case winui.WMClose:
			// 面板不接受外部关闭请求，只隐藏，生命周期随进程。
			p.hideOnOwnThread()
			return 0
		}
	}
	return winui.DefWindowProc(hwnd, msg, wParam, lParam)
}
