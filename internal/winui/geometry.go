// Package winui 封装托盘浮窗需要的 Win32 调用与纯几何计算。
//
// 几何计算部分不带平台约束，便于在任意平台上单元测试；实际的 user32/shell32
// 调用集中在 win32_windows.go，遵循项目既有的 LazyDLL + NewProc 风格
// （参见 internal/fsutil/fsutil_windows.go、internal/namespace/namespace_windows.go）。
package winui

// Rect 是屏幕坐标下的矩形，字段顺序与 Win32 RECT 一致，可直接参与系统调用。
// 多显示器场景下坐标可能为负值，计算时不得假设非负。
type Rect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

// Width 返回矩形宽度。
func (r Rect) Width() int32 { return r.Right - r.Left }

// Height 返回矩形高度。
func (r Rect) Height() int32 { return r.Bottom - r.Top }

// Contains 判断点是否落在矩形内（含左上边界，不含右下边界）。
// 用于「光标是否仍在浮窗内」的命中判定，因此传入的必须是屏幕坐标。
func (r Rect) Contains(x, y int32) bool {
	return x >= r.Left && x < r.Right && y >= r.Top && y < r.Bottom
}

// TaskbarEdge 表示任务栏停靠的屏幕边缘。
type TaskbarEdge int

const (
	// EdgeUnknown 表示无法从图标与工作区的相对位置判定边缘，
	// 常见于任务栏自动隐藏时图标矩形落在工作区内部。
	EdgeUnknown TaskbarEdge = iota
	EdgeBottom
	EdgeTop
	EdgeLeft
	EdgeRight
)

// DetectTaskbarEdge 由通知区域图标矩形与工作区的相对位置推断任务栏边缘。
//
// 依据：工作区（work area）不含任务栏，而通知区域图标位于任务栏之上，
// 因此图标中心必然落在工作区之外的那一侧。这比读取任务栏窗口位置更稳定，
// 也天然适配多显示器。
func DetectTaskbarEdge(icon, work Rect) TaskbarEdge {
	cx := icon.Left + icon.Width()/2
	cy := icon.Top + icon.Height()/2
	switch {
	case cy >= work.Bottom:
		return EdgeBottom
	case cy < work.Top:
		return EdgeTop
	case cx >= work.Right:
		return EdgeRight
	case cx < work.Left:
		return EdgeLeft
	default:
		return EdgeUnknown
	}
}

// PopupPosition 计算浮窗左上角的屏幕坐标。
//
// icon 是通知区域图标的屏幕矩形（由 Shell_NotifyIconGetRect 取得），
// work 是该显示器的工作区，w/h 是浮窗尺寸，gap 是浮窗与任务栏之间的间距。
//
// 浮窗贴合任务栏所在边缘，并在另一个轴向上与图标中心对齐；
// 返回值保证浮窗完全落在 work 内（浮窗大于工作区时以左上角对齐兜底）。
func PopupPosition(icon, work Rect, w, h, gap int32) (int32, int32) {
	cx := icon.Left + icon.Width()/2
	cy := icon.Top + icon.Height()/2

	var x, y int32
	switch DetectTaskbarEdge(icon, work) {
	case EdgeBottom:
		x, y = cx-w/2, work.Bottom-h-gap
	case EdgeTop:
		x, y = cx-w/2, work.Top+gap
	case EdgeRight:
		x, y = work.Right-w-gap, cy-h/2
	case EdgeLeft:
		x, y = work.Left+gap, cy-h/2
	default:
		// 边缘不可判定时退回「图标上方」，再由下面的钳制拉回可见区域。
		x, y = cx-w/2, icon.Top-h-gap
	}

	return clamp(x, work.Left, work.Right-w), clamp(y, work.Top, work.Bottom-h)
}

// clamp 把 v 限制在 [lo, hi] 内。hi < lo 时（浮窗比工作区还大）返回 lo，
// 保证浮窗左上角可见而不是被推到屏幕外。
func clamp(v, lo, hi int32) int32 {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// BottomRightPosition 计算把 w×h 的窗口贴在工作区右下角时左上角的坐标，
// 与右下边缘各留 gap。用于「启动即固定」形态——窗口常驻在桌面右下角，
// 位置不依赖托盘图标或鼠标。返回值保证窗口完全落在 work 内。
func BottomRightPosition(work Rect, w, h, gap int32) (int32, int32) {
	x := work.Right - w - gap
	y := work.Bottom - h - gap
	return clamp(x, work.Left, work.Right-w), clamp(y, work.Top, work.Bottom-h)
}
