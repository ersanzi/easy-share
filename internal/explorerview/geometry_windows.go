//go:build windows

package explorerview

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var viewUser32 = windows.NewLazySystemDLL("user32.dll")

func resizePortrait(hwnd uintptr) {
	// 只为新独立Explorer窗口调用；已有窗口和未确认标签页形态不改尺寸。
	getMonitor := viewUser32.NewProc("MonitorFromWindow")
	getMonitorInfo := viewUser32.NewProc("GetMonitorInfoW")
	getDPI := viewUser32.NewProc("GetDpiForWindow")
	setPosition := viewUser32.NewProc("SetWindowPos")
	type rect struct{ Left, Top, Right, Bottom int32 }
	var info struct {
		Size          uint32
		Monitor, Work rect
		Flags         uint32
	}
	info.Size = uint32(unsafe.Sizeof(info))
	monitor, _, _ := getMonitor.Call(hwnd, 2)
	ok, _, _ := getMonitorInfo.Call(monitor, uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		return
	}
	dpi, _, _ := getDPI.Call(hwnd)
	width, height := PortraitSize(int32(dpi), info.Work.Right-info.Work.Left, info.Work.Bottom-info.Work.Top)
	x := info.Work.Left + (info.Work.Right-info.Work.Left-width)/2
	y := info.Work.Top + (info.Work.Bottom-info.Work.Top-height)/2
	setPosition.Call(hwnd, 0, uintptr(x), uintptr(y), uintptr(width), uintptr(height), 0x4|0x10) // NOZORDER|NOACTIVATE
}
