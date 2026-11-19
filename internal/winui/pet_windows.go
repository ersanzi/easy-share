//go:build windows

package winui

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// 宠物宿主只补充线程 DPI、鼠标捕获、原生绘制与真实 window region，不引入框架。
var (
	gdi32                      = windows.NewLazySystemDLL("gdi32.dll")
	procSetThreadDPIContext    = user32.NewProc("SetThreadDpiAwarenessContext")
	procSetCapture             = user32.NewProc("SetCapture")
	procGetCapture             = user32.NewProc("GetCapture")
	procTrackMouseEvent        = user32.NewProc("TrackMouseEvent")
	procGetSystemMetrics       = user32.NewProc("GetSystemMetrics")
	procGetSystemMetricsForDPI = user32.NewProc("GetSystemMetricsForDpi")
	procGetDoubleClickTime     = user32.NewProc("GetDoubleClickTime")
	procBeginPaint             = user32.NewProc("BeginPaint")
	procEndPaint               = user32.NewProc("EndPaint")
	procInvalidateRect         = user32.NewProc("InvalidateRect")
	procDrawTextW              = user32.NewProc("DrawTextW")
	procLoadCursorW            = user32.NewProc("LoadCursorW")
	procSetWindowRgn           = user32.NewProc("SetWindowRgn")
	procCreateRoundRectRgn     = gdi32.NewProc("CreateRoundRectRgn")
	procCreateSolidBrush       = gdi32.NewProc("CreateSolidBrush")
	procCreatePen              = gdi32.NewProc("CreatePen")
	procSelectObject           = gdi32.NewProc("SelectObject")
	procDeleteObject           = gdi32.NewProc("DeleteObject")
	procRoundRect              = gdi32.NewProc("RoundRect")
	procEllipse                = gdi32.NewProc("Ellipse")
	procMoveToEx               = gdi32.NewProc("MoveToEx")
	procLineTo                 = gdi32.NewProc("LineTo")
	procSetBkMode              = gdi32.NewProc("SetBkMode")
	procSetTextColor           = gdi32.NewProc("SetTextColor")
	procCreateFontW            = gdi32.NewProc("CreateFontW")
	procCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	procCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	procDeleteDC               = gdi32.NewProc("DeleteDC")
	procBitBlt                 = gdi32.NewProc("BitBlt")
	procFillRect               = user32.NewProc("FillRect")
	procSaveDC                 = gdi32.NewProc("SaveDC")
	procRestoreDC              = gdi32.NewProc("RestoreDC")
)

// SRCCOPY 是 BitBlt 的默认光栅操作。
const SRCCOPY = 0x00CC0020

const (
	WMSize           = 0x0005
	WMPaint          = 0x000F
	WMEraseBkgnd     = 0x0014
	WMDisplayChange  = 0x007E
	WMSettingChange  = 0x001A
	WMNCHitTest      = 0x0084
	WMMouseMove      = 0x0200
	WMLButtonDown    = 0x0201
	WMLButtonUp      = 0x0202
	WMLButtonDblClk  = 0x0203
	WMCaptureChanged = 0x0215
	WMMouseLeave     = 0x02A3
	WMDPIChanged     = 0x02E0
	CSDblClks        = 0x0008
	HTClient         = 1
	HTNowhere        = 0
)

// PaintStruct 与 PAINTSTRUCT 一致；BOOL 必须用 int32，不能使用 Go bool。
type PaintStruct struct {
	DC        uintptr
	Erase     int32
	Paint     Rect
	Restore   int32
	IncUpdate int32
	Reserved  [32]byte
}

type trackMouse struct {
	Size      uint32
	Flags     uint32
	HWnd      uintptr
	HoverTime uint32
}

// AcquireCOMSTA 成功必须成对释放（含 S_FALSE）；不同套间不伪装成成功。
func AcquireCOMSTA() (func(), error) {
	err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED)
	if err != nil && err != syscall.Errno(windows.S_FALSE) {
		return nil, fmt.Errorf("pet CoInitializeEx(STA): %w", err)
	}
	return windows.CoUninitialize, nil
}

// EnterPerMonitorDPI 只修改当前 UI 线程，绝不改变 Wails 主窗的进程 DPI 模式。
func EnterPerMonitorDPI() func() {
	if procSetThreadDPIContext.Find() != nil {
		return func() {}
	}
	previous, _, _ := procSetThreadDPIContext.Call(^uintptr(3)) // PER_MONITOR_AWARE_V2 (-4)
	if previous == 0 {
		previous, _, _ = procSetThreadDPIContext.Call(^uintptr(2))
	}
	return func() {
		if previous != 0 {
			procSetThreadDPIContext.Call(previous)
		}
	}
}

func CaptureMouse(hwnd uintptr) { procSetCapture.Call(hwnd) }
func ReleaseMouse(hwnd uintptr) {
	current, _, _ := procGetCapture.Call()
	if current == hwnd {
		procReleaseCapture.Call()
	}
}
func TrackMouseLeave(hwnd uintptr) {
	t := trackMouse{Size: uint32(unsafe.Sizeof(trackMouse{})), Flags: 2, HWnd: hwnd}
	procTrackMouseEvent.Call(uintptr(unsafe.Pointer(&t)))
}
func MouseMetrics(dpi int32) (dragX, dragY, doubleX, doubleY int32, doubleMS uint32) {
	metric := func(id uintptr) int32 {
		var v uintptr
		if procGetSystemMetricsForDPI.Find() == nil {
			v, _, _ = procGetSystemMetricsForDPI.Call(id, uintptr(dpi))
		} else {
			v, _, _ = procGetSystemMetrics.Call(id)
		}
		return max(1, int32(v))
	}
	dragX, dragY = metric(68), metric(69)
	doubleX, doubleY = metric(36), metric(37)
	v, _, _ := procGetDoubleClickTime.Call()
	return dragX, dragY, doubleX, doubleY, uint32(v)
}
func ArrowCursor() windows.Handle {
	h, _, _ := procLoadCursorW.Call(0, 32512)
	return windows.Handle(h)
}
func Invalidate(hwnd uintptr) { procInvalidateRect.Call(hwnd, 0, 0) }

// SetRoundedRegion 在 Server 2022/Windows 10 也真正裁剪窗口，而非依赖 Win11 DWM 属性。
// SetWindowRgn 成功后区域归系统所有；失败时由本线程释放。
func SetRoundedRegion(hwnd uintptr, width, height, radius int32) error {
	rgn, _, err := procCreateRoundRectRgn.Call(0, 0, uintptr(width+1), uintptr(height+1), uintptr(radius*2), uintptr(radius*2))
	if rgn == 0 {
		return fmt.Errorf("CreateRoundRectRgn: %w", err)
	}
	ok, _, err := procSetWindowRgn.Call(hwnd, rgn, 1)
	if ok == 0 {
		procDeleteObject.Call(rgn)
		return fmt.Errorf("SetWindowRgn: %w", err)
	}
	return nil
}

// PetPalette 是主设计 token 的原生镜像，由调用方传入 COLORREF。
type PetPalette struct{ Surface, Border, Ink, Accent uint32 }

// petPaintCache 缓存宠物位图：悬停期间反复 WM_PAINT 不再逐次重建字体与整幅 GDI，
// 避免「EasyShare」文字因整窗重绘/合成时序出现视觉抖动。
type petPaintCache struct {
	width   int32
	height  int32
	dpi     int32
	pinned  bool
	palette PetPalette
	bitmap  uintptr
	oldBmp  uintptr
	dc      uintptr
}

var (
	petPaintMu     sync.Mutex
	petPaintCaches = map[uintptr]*petPaintCache{}
)

// PaintPet 画静态圆角宠物：尺寸/DPI/固定态/配色未变时直接 BitBlt 缓存位图。
// 不动画、不新建 WebView；GDI 对象在缓存替换或清理时释放。
func PaintPet(hwnd uintptr, dpi int32, pinned bool, palette PetPalette) {
	var paint PaintStruct
	dc, _, _ := procBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&paint)))
	if dc == 0 {
		return
	}
	defer procEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&paint)))
	r := GetClientRect(hwnd)
	if r.Width() <= 0 || r.Height() <= 0 {
		return
	}
	cache := takePetPaintCache(hwnd, dc, r.Width(), r.Height(), dpi, pinned, palette)
	if cache == 0 {
		paintPetDirect(dc, r, dpi, pinned, palette)
		return
	}
	if ok, _, _ := procBitBlt.Call(dc, 0, 0, uintptr(r.Width()), uintptr(r.Height()), cache, 0, 0, SRCCOPY); ok == 0 {
		paintPetDirect(dc, r, dpi, pinned, palette)
	}
}

func takePetPaintCache(hwnd, referenceDC uintptr, width, height, dpi int32, pinned bool, palette PetPalette) uintptr {
	petPaintMu.Lock()
	defer petPaintMu.Unlock()
	c, ok := petPaintCaches[hwnd]
	if ok && c.width == width && c.height == height && c.dpi == dpi && c.pinned == pinned && c.palette == palette && c.bitmap != 0 && c.dc != 0 {
		return c.dc
	}
	releasePetPaintCacheLocked(hwnd)
	memDC, _, _ := procCreateCompatibleDC.Call(referenceDC)
	if memDC == 0 {
		return 0
	}
	bmp, _, _ := procCreateCompatibleBitmap.Call(referenceDC, uintptr(width), uintptr(height))
	if bmp == 0 {
		procDeleteDC.Call(memDC)
		return 0
	}
	oldBmp, _, _ := procSelectObject.Call(memDC, bmp)
	if oldBmp == 0 {
		procDeleteObject.Call(bmp)
		procDeleteDC.Call(memDC)
		return 0
	}
	paintPetDirect(memDC, Rect{Right: width, Bottom: height}, dpi, pinned, palette)
	// 保持新位图留在 memDC，BitBlt 才能读到宠物画面；oldBmp 留到释放时选回。
	petPaintCaches[hwnd] = &petPaintCache{
		width: width, height: height, dpi: dpi, pinned: pinned, palette: palette,
		bitmap: bmp, oldBmp: oldBmp, dc: memDC,
	}
	return memDC
}

func releasePetPaintCacheLocked(hwnd uintptr) {
	c, ok := petPaintCaches[hwnd]
	if !ok {
		return
	}
	if c.dc != 0 {
		if c.oldBmp != 0 {
			procSelectObject.Call(c.dc, c.oldBmp)
		}
		procDeleteDC.Call(c.dc)
	}
	if c.bitmap != 0 {
		procDeleteObject.Call(c.bitmap)
	}
	delete(petPaintCaches, hwnd)
}

// ReleasePetPaintCache 在窗口销毁/换 DPI 时释放该 hwnd 的缓存位图。
func ReleasePetPaintCache(hwnd uintptr) {
	petPaintMu.Lock()
	releasePetPaintCacheLocked(hwnd)
	petPaintMu.Unlock()
}

// PetCornerRadius is shared by the window region and its painted outline.
func PetCornerRadius(dpi int32) int32 { return max(1, (14*dpi+48)/96) }

func paintPetDirect(dc uintptr, r Rect, dpi int32, pinned bool, palette PetPalette) {
	if saved, _, _ := procSaveDC.Call(dc); saved != 0 {
		defer procRestoreDC.Call(dc, saved)
	}
	oldMode, _, _ := procSetBkMode.Call(dc, 1)
	oldColor, _, _ := procSetTextColor.Call(dc, uintptr(palette.Ink))
	defer procSetBkMode.Call(dc, oldMode)
	defer procSetTextColor.Call(dc, oldColor)
	scale := func(v int32) int32 { return max(1, (v*r.Width()+48)/96) }
	brush, _, _ := procCreateSolidBrush.Call(uintptr(palette.Surface))
	pen, _, _ := procCreatePen.Call(0, uintptr(scale(1)), uintptr(palette.Border))
	oldBrush, _, _ := procSelectObject.Call(dc, brush)
	oldPen, _, _ := procSelectObject.Call(dc, pen)
	defer func() {
		procSelectObject.Call(dc, oldBrush)
		procSelectObject.Call(dc, oldPen)
		procDeleteObject.Call(brush)
		procDeleteObject.Call(pen)
	}()
	// SRCCOPY has no transparency: initialize even the pixels outside the outline.
	procFillRect.Call(dc, uintptr(unsafe.Pointer(&r)), brush)
	diameter := uintptr(2 * PetCornerRadius(dpi))
	procRoundRect.Call(dc, 0, 0, uintptr(r.Right), uintptr(r.Bottom), diameter, diameter)
	eye, _, _ := procCreateSolidBrush.Call(uintptr(palette.Ink))
	procSelectObject.Call(dc, eye)
	for _, x := range []int32{34, 58} {
		procEllipse.Call(dc, uintptr(scale(x)), uintptr(scale(22)), uintptr(scale(x+4)), uintptr(scale(28)))
	}
	procMoveToEx.Call(dc, uintptr(scale(42)), uintptr(scale(32)), 0)
	procLineTo.Call(dc, uintptr(scale(48)), uintptr(scale(35)))
	procLineTo.Call(dc, uintptr(scale(54)), uintptr(scale(32)))
	procSelectObject.Call(dc, brush)
	procDeleteObject.Call(eye)
	if pinned {
		accent, _, _ := procCreateSolidBrush.Call(uintptr(palette.Accent))
		procSelectObject.Call(dc, accent)
		procEllipse.Call(dc, uintptr(scale(80)), uintptr(scale(9)), uintptr(scale(86)), uintptr(scale(15)))
		procSelectObject.Call(dc, brush)
		procDeleteObject.Call(accent)
	}
	face, _ := windows.UTF16PtrFromString("Segoe UI")
	font, _, _ := procCreateFontW.Call(uintptr(-scale(10)), 0, 0, 0, 400, 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(face)))
	oldFont, _, _ := procSelectObject.Call(dc, font)
	defer func() { procSelectObject.Call(dc, oldFont); procDeleteObject.Call(font) }()
	procSetBkMode.Call(dc, 1)
	procSetTextColor.Call(dc, uintptr(palette.Ink))
	text, _ := windows.UTF16PtrFromString("EasyShare")
	label := Rect{Left: 0, Top: scale(43), Right: r.Right, Bottom: r.Bottom - scale(4)}
	procDrawTextW.Call(dc, uintptr(unsafe.Pointer(text)), ^uintptr(0), uintptr(unsafe.Pointer(&label)), 1|4|32)
}

var (
	shcore                  = windows.NewLazySystemDLL("shcore.dll")
	procEnumDisplayMonitors = user32.NewProc("EnumDisplayMonitors")
	procGetDPIForMonitor    = shcore.NewProc("GetDpiForMonitor")
)

// MonitorSnapshot 不含运行态句柄，便于传给位置纯函数。
type MonitorSnapshot struct {
	Name    string
	Work    Rect
	DPI     int32
	Primary bool
}

type monitorInfoEx struct {
	MonitorInfo
	Device [32]uint16
}

// Monitors 的枚举回调仅同步使用，不留下 Go 指针给系统。
func Monitors() []MonitorSnapshot {
	var result []MonitorSnapshot
	callback := windows.NewCallback(func(mon, _ uintptr, _ uintptr, _ uintptr) uintptr {
		info := monitorInfoEx{}
		info.Size = uint32(unsafe.Sizeof(info))
		ok, _, _ := procGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&info)))
		if ok != 0 {
			x, y := uint32(96), uint32(96)
			if procGetDPIForMonitor.Find() == nil {
				procGetDPIForMonitor.Call(mon, 0, uintptr(unsafe.Pointer(&x)), uintptr(unsafe.Pointer(&y)))
			}
			result = append(result, MonitorSnapshot{Name: windows.UTF16ToString(info.Device[:]), Work: info.Work, DPI: int32(x), Primary: info.Flags&1 != 0})
		}
		return 1
	})
	procEnumDisplayMonitors.Call(0, 0, callback, 0)
	return result
}

// MoveWindowNoActivate 改位置与尺寸但不改变显示态，隐藏面板重排不会闪现。
func MoveWindowNoActivate(hwnd uintptr, rect Rect) {
	procSetWindowPos.Call(hwnd, HWNDTopMost, uintptr(rect.Left), uintptr(rect.Top), uintptr(rect.Width()), uintptr(rect.Height()), SWPNoActivate)
}
