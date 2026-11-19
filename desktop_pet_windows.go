//go:build windows

package main

import (
	"os"
	"path/filepath"
	"sync"
	"time"
	"unsafe"

	"easyshare/internal/desktoppet"
	"easyshare/internal/winui"

	"golang.org/x/sys/windows"
)

const (
	petWindowClass    = "EasyShareDesktopPet"
	petPointerTimerID = 2
	petClickTimerID   = 4
	petCmdOpenMain    = 2001
	petCmdHide        = 2002
)

// desktopPet 是常驻的原生宿主；面板与 backend 属于 hoverPopup，不因收起而销毁。
// 两个 hwnd 在同一独立 UI 线程创建；不接管 Wails 主线程或主消息循环。
type desktopPet struct {
	popup        *hoverPopup
	hwnd         uintptr
	dpi          int32
	state        desktoppet.State
	dragOffset   winui.Point
	positionPath string
	started      time.Time
	hidePending  bool
	pointerTimer bool
	hidden       bool
	menuOpen     bool
	releasing    bool
	paintState   petVisualState
}

type petVisualState struct {
	width, height, dpi int32
	pinned             bool
	palette            winui.PetPalette
}

func petPalette() winui.PetPalette {
	return winui.PetPalette{Surface: 0x00fbf9f8, Border: 0x00e8e6e5, Ink: 0x003e2e26, Accent: 0x00e0691f}
}

func (p *desktopPet) invalidateVisualChanges() {
	r := winui.GetClientRect(p.hwnd)
	next := petVisualState{r.Width(), r.Height(), p.dpi, p.state.IsPinned(), petPalette()}
	if p.paintState.update(next) {
		winui.Invalidate(p.hwnd)
	}
}

func (s *petVisualState) update(next petVisualState) bool {
	if *s == next {
		return false
	}
	*s = next
	return true
}

var (
	petClassOnce  sync.Once
	petClassErr   error
	petInstances  = map[uintptr]*desktopPet{}
	petInstanceMu sync.RWMutex
)

func newDesktopPet(popup *hoverPopup) (*desktopPet, error) {
	if err := ensurePetClass(); err != nil {
		return nil, err
	}
	p := &desktopPet{popup: popup, dpi: 96, started: time.Now()}
	if base := os.Getenv("LOCALAPPDATA"); base != "" {
		p.positionPath = filepath.Join(base, "EasyShare", "desktop-pet-position.json")
	}
	saved, _ := desktoppet.ReadPlacement(p.positionPath)
	rect, display := desktoppet.RestorePlacement(saved, petDisplays())
	if rect.Width() <= 0 {
		work := winui.GetPrimaryWorkArea()
		rect, display = desktoppet.RestorePlacement(saved, []desktoppet.Display{{Work: work, DPI: 96, Primary: true}})
	}
	p.dpi = max(96, display.DPI)
	hwnd, err := winui.CreateWindow(winui.WSExToolWindow|winui.WSExTopMost|winui.WSExNoActivate,
		petWindowClass, "EasyShare 宠物", winui.WSPopup, rect.Left, rect.Top, rect.Width(), rect.Height(), 0)
	if err != nil {
		return nil, err
	}
	p.hwnd = hwnd
	petInstanceMu.Lock()
	petInstances[hwnd] = p
	petInstanceMu.Unlock()
	p.dpi = winui.DpiForWindow(hwnd)
	if err := p.round(); err != nil {
		p.destroy()
		return nil, err
	}
	winui.ShowWindowNoActivate(hwnd)
	// Explorer OLE 拖动时不保证发送普通 WM_MOUSEMOVE；可见宠物以所属线程采样悬停。
	winui.SetTimer(hwnd, petPointerTimerID, 30)
	p.pointerTimer = true
	winui.Invalidate(hwnd)
	return p, nil
}

func petDisplays() []desktoppet.Display {
	monitors := winui.Monitors()
	result := make([]desktoppet.Display, 0, len(monitors))
	for _, m := range monitors {
		result = append(result, desktoppet.Display{Name: m.Name, Work: m.Work, DPI: m.DPI, Primary: m.Primary})
	}
	return result
}

func (p *desktopPet) round() error {
	r := winui.GetClientRect(p.hwnd)
	return winui.SetRoundedRegion(p.hwnd, r.Width(), r.Height(), winui.PetCornerRadius(p.dpi))
}

func (p *desktopPet) nowMS() uint64 { return uint64(time.Since(p.started).Milliseconds()) }

func (p *desktopPet) pointer(region desktoppet.Region, inside bool) {
	if p.hidden || p.popup.petHidden.Load() {
		return
	}
	// DOM/native leave events may arrive in different orders at the shared edge.
	// Sample the union of both actual visible regions atomically.
	_ = region
	_ = inside
	p.syncPointer(false)
}

// present 只管理 UI；固定只锁展开、不锁位置。菜单/外部拖入期间不安排误收起。
func (p *desktopPet) present() {
	p.popup.pinned.Store(p.state.IsPinned())
	if p.hwnd == 0 {
		return
	}
	if p.hidden || p.popup.petHidden.Load() {
		p.cancelUITimers()
		p.popup.hideImmediatelyOnOwnThread()
		return
	}
	guarded := p.popup.menuGuarded()
	if p.wantsPanel() {
		p.popup.showOnOwnThread()
	} else {
		p.popup.hideOnOwnThread()
	}
	if p.state.WantsHide() && !guarded {
		if !p.hidePending {
			winui.SetTimer(p.hwnd, hoverHideTimerID, hoverHideDelayMS)
			p.hidePending = true
		}
	} else {
		winui.KillTimer(p.hwnd, hoverHideTimerID)
		p.hidePending = false
	}
	// 收起但宠物仍可见时也要采样，OLE 拖入宠物不能依赖被系统抑制的 mousemove。
	if !p.pointerTimer {
		winui.SetTimer(p.hwnd, petPointerTimerID, 30)
		p.pointerTimer = true
	}
	p.invalidateVisualChanges()
}

func (p *desktopPet) wantsPanel() bool {
	return !p.hidden && !p.popup.petHidden.Load() && p.state.Visible() &&
		(!p.state.Dragging || p.state.IsPinned() || p.state.ExternalDrag || p.popup.menuGuarded())
}

func (p *desktopPet) syncPointer(hideDue bool) {
	if p.hidden || p.popup.petHidden.Load() {
		return
	}
	cursor := winui.GetCursorPos()
	inPet := winui.WindowRegionContains(p.hwnd, cursor)
	inPanel := p.popup.visible && winui.WindowRegionContains(p.popup.hwnd, cursor)
	if hideDue {
		winui.KillTimer(p.hwnd, hoverHideTimerID)
		p.hidePending = false
		if p.popup.menuGuarded() {
			p.state.Pointer(desktoppet.Pet, inPet)
			p.state.Pointer(desktoppet.Panel, inPanel)
		} else {
			p.state.HideDue(inPet, inPanel)
		}
	} else {
		p.state.Pointer(desktoppet.Pet, inPet)
		p.state.Pointer(desktoppet.Panel, inPanel)
	}
	p.present()
}

func (p *desktopPet) togglePin() {
	if p.hidden || p.popup.petHidden.Load() {
		return
	}
	p.state.TogglePin()
	p.popup.applyPinnedToPage(p.state.IsPinned())
	p.present()
}

func (p *desktopPet) down() {
	if p.hidden || p.popup.petHidden.Load() {
		return
	}
	now := p.nowMS()
	p.resolveClicks(now) // 第二击超过 deadline+1 后到来的新按下先消费旧决议。
	winui.KillTimer(p.hwnd, petClickTimerID)
	cursor := winui.GetCursorPos()
	_, _, doubleX, doubleY, doubleMS := winui.MouseMetrics(p.dpi)
	p.state.Press(cursor.X, cursor.Y, now, doubleMS, doubleX, doubleY)
	r := winui.GetWindowRect(p.hwnd)
	p.dragOffset = winui.Point{X: cursor.X - r.Left, Y: cursor.Y - r.Top}
	winui.CaptureMouse(p.hwnd)
	p.present()
}

func (p *desktopPet) move() {
	if p.hidden || p.popup.petHidden.Load() {
		return
	}
	winui.TrackMouseLeave(p.hwnd)
	p.state.Pointer(desktoppet.Pet, true)
	cursor := winui.GetCursorPos()
	dragX, dragY, _, _, _ := winui.MouseMetrics(p.dpi)
	if p.state.Move(cursor.X, cursor.Y, dragX, dragY) {
		// Move 已越过阈值；显式取消待决连击但保留本次实际拖动。
		p.state.CancelPress()
		p.state.Dragging = true
		winui.KillTimer(p.hwnd, petClickTimerID)
	}
	if p.state.Dragging {
		r := winui.GetWindowRect(p.hwnd)
		x, y := cursor.X-p.dragOffset.X, cursor.Y-p.dragOffset.Y
		work := winui.WorkAreaForRect(winui.Rect{Left: x, Top: y, Right: x + r.Width(), Bottom: y + r.Height()})
		x, y = max(work.Left, min(x, work.Right-r.Width())), max(work.Top, min(y, work.Bottom-r.Height()))
		winui.MoveWindowNoActivate(p.hwnd, winui.Rect{Left: x, Top: y, Right: x + r.Width(), Bottom: y + r.Height()})
	}
	p.present()
}

func (p *desktopPet) up() {
	if p.hidden || p.popup.petHidden.Load() {
		p.cancelInteraction()
		return
	}
	cursor := winui.GetCursorPos()
	wasDragging := p.state.Dragging
	now := p.nowMS()
	if wasDragging {
		p.state.CancelPress()
	} else {
		p.state.Release(cursor.X, cursor.Y, now)
	}
	// 正常 ReleaseCapture 也会同步发 WM_CAPTURECHANGED，不能因此取消刚挂起的双击。
	p.releaseMouse()
	p.resolveClicks(now)
	if wasDragging {
		p.savePosition()
	}
	p.syncPointer(false)
}

// resolveClicks 在 down/up/WM_TIMER 共用；三击只走既有 onOpenSpace 回调。
func (p *desktopPet) resolveClicks(now uint64) {
	switch p.state.ResolveClicks(now) {
	case desktoppet.PanelPinToggled:
		p.popup.applyPinnedToPage(p.state.IsPinned())
		p.present()
	case desktoppet.OpenCurrentSpace:
		if p.popup.onOpenSpace != nil {
			go p.popup.onOpenSpace()
		}
	}
	p.scheduleClicks(now)
}

// petClickTimerDelay 在包含 deadline 的系统窗口外一个毫秒决议，避免边界第三击误固定。
func petClickTimerDelay(now, deadline uint64) uint32 {
	if now > deadline {
		return 1
	}
	return uint32(min(uint64(^uint32(0))-1, deadline-now) + 1)
}

func (p *desktopPet) scheduleClicks(now uint64) {
	if p.hwnd == 0 {
		return
	}
	winui.KillTimer(p.hwnd, petClickTimerID)
	if deadline, pending := p.state.ClickDeadline(); pending && !p.state.Pressed() && !p.hidden && !p.popup.petHidden.Load() {
		winui.SetTimer(p.hwnd, petClickTimerID, petClickTimerDelay(now, deadline))
	}
}

func (p *desktopPet) releaseMouse() {
	p.releasing = true
	winui.ReleaseMouse(p.hwnd)
	p.releasing = false
}

func (p *desktopPet) cancelInteraction() {
	p.state.CancelPress()
	if p.hwnd != 0 {
		winui.KillTimer(p.hwnd, petClickTimerID)
		p.releaseMouse()
	}
}

func (p *desktopPet) cancelUITimers() {
	if p.hwnd != 0 {
		winui.KillTimer(p.hwnd, hoverHideTimerID)
		winui.KillTimer(p.hwnd, petPointerTimerID)
		winui.KillTimer(p.hwnd, petClickTimerID)
	}
	p.hidePending, p.pointerTimer = false, false
}

// setVisibleOnOwnThread 隐藏整只宠物但不销毁窗口/COM/任务；显示时恢复原位置与固定态。
func (p *desktopPet) setVisibleOnOwnThread(visible bool) {
	p.hidden = !visible
	p.cancelInteraction()
	p.state.InPet, p.state.InPanel, p.state.ExternalDrag = false, false, false
	if !visible {
		p.state.Collapse() // 保留 Pinned；隐藏不是解除固定。
		p.cancelUITimers()
		p.popup.hideImmediatelyOnOwnThread()
		if p.hwnd != 0 {
			winui.HideWindow(p.hwnd)
		}
		return
	}
	if p.hwnd != 0 {
		winui.ShowWindowNoActivate(p.hwnd)
		p.syncPointer(false)
	}
}

// showMenu 只保留打开客户端/隐藏宠物；菜单跨窗口期间不收面板，右键取消待决连击。
func (p *desktopPet) showMenu() {
	if p.hidden || p.popup.petHidden.Load() || p.menuOpen {
		return
	}
	p.cancelInteraction()
	menu, err := winui.NewMenu()
	if err != nil {
		p.popup.logf("pet menu: %v", err)
		return
	}
	defer menu.Destroy()
	p.menuOpen = true
	p.present()
	defer func() {
		p.menuOpen = false
		p.syncPointer(false)
	}()
	_ = menu.AddItem(petCmdOpenMain, "打开客户端", true)
	_ = menu.AddItem(petCmdHide, "隐藏宠物", true)
	pos := winui.GetCursorPos()
	switch menu.Track(p.hwnd, pos.X, pos.Y) {
	case petCmdOpenMain:
		if p.popup.onOpenMain != nil {
			p.popup.onOpenMain()
		}
	case petCmdHide:
		p.popup.SetPetVisible(false)
		p.setVisibleOnOwnThread(false)
	}
}

func (p *desktopPet) savePosition() {
	if p.positionPath == "" {
		return
	}
	r := winui.GetWindowRect(p.hwnd)
	displays := petDisplays()
	_, display := desktoppet.RestorePlacement(desktoppet.Placement{Version: 1, Window: r}, displays)
	if display.Work.Width() <= 0 {
		return
	}
	if err := desktoppet.WritePlacement(p.positionPath, desktoppet.CapturePlacement(r, display)); err != nil {
		p.popup.logf("pet: 保存位置失败: %v", err)
	}
}

// correctLayout 响应显示器拔插、DPI 与任务栏工作区变化，窗口不能停留在屏幕外。
func (p *desktopPet) correctLayout(suggested *winui.Rect, dpi int32) {
	r := winui.GetWindowRect(p.hwnd)
	if suggested != nil {
		r = *suggested
	}
	if dpi > 0 {
		p.dpi = dpi
	}
	displays := petDisplays()
	if dpi > 0 {
		for i := range displays {
			if displays[i].Work.Contains(r.Left, r.Top) {
				displays[i].DPI = dpi
			}
		}
	}
	corrected, display := desktoppet.RestorePlacement(desktoppet.Placement{Version: 1, Window: r}, displays)
	if corrected.Width() <= 0 {
		return
	}
	p.dpi = display.DPI
	winui.MoveWindowNoActivate(p.hwnd, corrected)
	if err := p.round(); err != nil {
		p.popup.logf("pet: 更新圆角失败: %v", err)
	}
	p.present()
}

func (p *desktopPet) destroy() {
	if p.hwnd == 0 {
		return
	}
	p.savePosition()
	p.cancelInteraction()
	p.cancelUITimers()
	winui.ReleasePetPaintCache(p.hwnd)
	petInstanceMu.Lock()
	delete(petInstances, p.hwnd)
	petInstanceMu.Unlock()
	winui.DestroyWindow(p.hwnd)
	p.hwnd = 0
}

func ensurePetClass() error {
	petClassOnce.Do(func() {
		name, err := windows.UTF16PtrFromString(petWindowClass)
		if err != nil {
			petClassErr = err
			return
		}
		petClassErr = winui.RegisterClass(&winui.WndClassEx{Style: winui.CSHRedraw | winui.CSVRedraw | winui.CSDblClks,
			WndProc: windows.NewCallback(petWndProc), Instance: winui.GetModuleHandle(), Cursor: winui.ArrowCursor(), ClassName: name})
	})
	return petClassErr
}

func petWndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	petInstanceMu.RLock()
	p := petInstances[hwnd]
	petInstanceMu.RUnlock()
	if p == nil {
		return winui.DefWindowProc(hwnd, msg, wParam, lParam)
	}
	switch msg {
	case winui.WMPaint:
		// 原生镜像：--bg-chrome / --border / --text / --blue（COLORREF 为 BGR）。
		winui.PaintPet(hwnd, p.dpi, p.state.IsPinned(), petPalette())
		return 0
	case winui.WMSize:
		if err := p.round(); err != nil {
			p.popup.logf("pet: 更新圆角失败: %v", err)
		}
		p.invalidateVisualChanges()
		return 0
	case winui.WMEraseBkgnd:
		return 1
	case winui.WMNCHitTest:
		return winui.HTClient // 不用 HTCAPTION，双击不进入系统最大化/拖窗。
	case winui.WMLButtonDown, winui.WMLButtonDblClk:
		p.down()
		return 0
	case winui.WMMouseMove:
		p.move()
		return 0
	case winui.WMLButtonUp:
		p.up()
		return 0
	case winui.WMMouseLeave:
		p.pointer(desktoppet.Pet, false)
		return 0
	case winui.WMRButtonDown:
		p.cancelInteraction()
		return 0
	case winui.WMRButtonUp, winui.WMContextMenu:
		p.showMenu()
		return 0
	case winui.WMCancelMode:
		p.cancelInteraction()
		p.syncPointer(false)
		return 0
	case winui.WMCaptureChanged:
		if lParam != hwnd && !p.releasing {
			p.cancelInteraction()
			p.syncPointer(false)
		}
		return 0
	case winui.WMTimer:
		if wParam == petClickTimerID {
			winui.KillTimer(p.hwnd, petClickTimerID)
			if !p.hidden && !p.popup.petHidden.Load() {
				p.resolveClicks(p.nowMS())
				p.syncPointer(false)
			}
			return 0
		}
		if wParam == hoverHideTimerID {
			p.syncPointer(true)
			return 0
		}
		if wParam == petPointerTimerID {
			p.syncPointer(false)
			return 0
		}
	case winui.WMDPIChanged:
		var suggested *winui.Rect
		if lParam != 0 {
			// lParam是本次WM_DPICHANGED期间有效的系统RECT指针，先经变量地址转换。
			copy := *(*winui.Rect)(*(*unsafe.Pointer)(unsafe.Pointer(&lParam)))
			suggested = &copy
		}
		oldDPI := max(96, p.dpi)
		newDPI := int32(wParam & 0xffff)
		if p.state.Dragging {
			p.dragOffset.X = p.dragOffset.X * newDPI / oldDPI
			p.dragOffset.Y = p.dragOffset.Y * newDPI / oldDPI
		}
		p.correctLayout(suggested, newDPI)
		return 0
	case winui.WMDisplayChange, winui.WMSettingChange:
		// 显示/系统设置变化不能关闭用户明确要求的桌宠 reveal。
		p.popup.reducedMotion = false
		p.correctLayout(nil, 0)
		return 0
	case winui.WMClose:
		return 0 // 宠物不因 Alt-F4 消失；正常退出由托盘统一释放。
	}
	return winui.DefWindowProc(hwnd, msg, wParam, lParam)
}
