//go:build windows

package winui

import (
	"fmt"
	"unsafe"
)

const (
	WMCancelMode  = 0x001F
	WMRButtonDown = 0x0204
	WMRButtonUp   = 0x0205
)

var (
	procGetWindowRgn  = user32.NewProc("GetWindowRgn")
	procCreateRectRgn = gdi32.NewProc("CreateRectRgn")
	procPtInRegion    = gdi32.NewProc("PtInRegion")
)

// SetClippedRoundedRegion 支持滑出帧的非零客户区起点，区域成功后转交系统拥有。
// 宿主仍保持完整 WebView 尺寸，仅改变其真实可见/可接收鼠标的区域。
func SetClippedRoundedRegion(hwnd uintptr, clip Rect, radius int32) error {
	var rgn uintptr
	var callErr error
	if clip.Width() <= 0 || clip.Height() <= 0 {
		rgn, _, callErr = procCreateRectRgn.Call(0, 0, 0, 0)
	} else {
		radius = min(max(0, radius), min(clip.Width(), clip.Height())/2)
		rgn, _, callErr = procCreateRoundRectRgn.Call(uintptr(clip.Left), uintptr(clip.Top),
			uintptr(clip.Right+1), uintptr(clip.Bottom+1), uintptr(radius*2), uintptr(radius*2))
	}
	if rgn == 0 {
		return fmt.Errorf("pet panel create clip: %w", callErr)
	}
	if ok, _, err := procSetWindowRgn.Call(hwnd, rgn, 1); ok == 0 {
		procDeleteObject.Call(rgn)
		return fmt.Errorf("pet panel SetWindowRgn: %w", err)
	}
	return nil
}

// WindowRegionContains 以真实裁剪区域命中，不能把动画期间不可见的整窗矩形当面板。
func WindowRegionContains(hwnd uintptr, point Point) bool {
	r := GetWindowRect(hwnd)
	if !r.Contains(point.X, point.Y) {
		return false
	}
	rgn, _, _ := procCreateRectRgn.Call(0, 0, 0, 0)
	if rgn == 0 {
		return true
	}
	defer procDeleteObject.Call(rgn)
	kind, _, _ := procGetWindowRgn.Call(hwnd, rgn)
	if kind == 0 { // ERROR：窗口未设置 region，按正常客户区命中。
		return true
	}
	inside, _, _ := procPtInRegion.Call(rgn, uintptr(point.X-r.Left), uintptr(point.Y-r.Top))
	return inside != 0
}

// ClientAreaAnimationsEnabled 读取系统减少动画选项，不修改进程或桌面的全局设置。
func ClientAreaAnimationsEnabled() bool {
	var enabled int32 = 1
	ok, _, _ := procSystemParametersIW.Call(0x1042, 0, uintptr(unsafe.Pointer(&enabled)), 0) // SPI_GETCLIENTAREAANIMATION
	return ok == 0 || enabled != 0
}

// WorkAreaAndDPIForRect 从同一显示器取得工作区与 DPI，避免跨屏后套用主屏缩放。
func WorkAreaAndDPIForRect(rect Rect, fallbackDPI int32) (Rect, int32) {
	dpi := max(96, fallbackDPI)
	monitor, _, _ := procMonitorFromRect.Call(uintptr(unsafe.Pointer(&rect)), MonitorDefaultToNearest)
	if monitor == 0 {
		return Rect{}, dpi
	}
	info := MonitorInfo{Size: uint32(unsafe.Sizeof(MonitorInfo{}))}
	if ok, _, _ := procGetMonitorInfoW.Call(monitor, uintptr(unsafe.Pointer(&info))); ok == 0 {
		return Rect{}, dpi
	}
	// 动画/hover 热路径直接取目标显示器，不能反复 EnumDisplayMonitors 分配永久回调。
	if procGetDPIForMonitor.Find() == nil {
		x, y := uint32(dpi), uint32(dpi)
		if hr, _, _ := procGetDPIForMonitor.Call(monitor, 0, uintptr(unsafe.Pointer(&x)), uintptr(unsafe.Pointer(&y))); hr == 0 && x > 0 {
			dpi = int32(x)
		}
	}
	return info.Work, dpi
}

// RaiseWindowNoActivate 仅调 Z 序，不移动用户宠物、不改变焦点或显示状态。
// SWP_NOSENDCHANGING 减少快速悬停时对宠物 WM_PAINT 的额外扰动。
func RaiseWindowNoActivate(hwnd uintptr) {
	if hwnd != 0 {
		procSetWindowPos.Call(hwnd, HWNDTopMost, 0, 0, 0, 0, SWPNoActivate|SWPNoMove|SWPNoSize|SWPNoSendChanging)
	}
}
