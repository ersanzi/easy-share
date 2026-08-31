package winui

import "testing"

// 1920x1080 屏幕、40px 任务栏的常见布局，四个停靠边缘各一组。
var (
	workBottom = Rect{Left: 0, Top: 0, Right: 1920, Bottom: 1040}
	iconBottom = Rect{Left: 1700, Top: 1048, Right: 1724, Bottom: 1072}

	workTop = Rect{Left: 0, Top: 40, Right: 1920, Bottom: 1080}
	iconTop = Rect{Left: 1700, Top: 8, Right: 1724, Bottom: 32}

	workLeft = Rect{Left: 40, Top: 0, Right: 1920, Bottom: 1080}
	iconLeft = Rect{Left: 8, Top: 900, Right: 32, Bottom: 924}

	workRight = Rect{Left: 0, Top: 0, Right: 1880, Bottom: 1080}
	iconRight = Rect{Left: 1888, Top: 900, Right: 1912, Bottom: 924}
)

func TestDetectTaskbarEdge(t *testing.T) {
	cases := []struct {
		name string
		icon Rect
		work Rect
		want TaskbarEdge
	}{
		{"任务栏在下", iconBottom, workBottom, EdgeBottom},
		{"任务栏在上", iconTop, workTop, EdgeTop},
		{"任务栏在左", iconLeft, workLeft, EdgeLeft},
		{"任务栏在右", iconRight, workRight, EdgeRight},
		{"自动隐藏时图标落在工作区内", Rect{100, 500, 124, 524}, workBottom, EdgeUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DetectTaskbarEdge(c.icon, c.work); got != c.want {
				t.Fatalf("DetectTaskbarEdge = %v, want %v", got, c.want)
			}
		})
	}
}

// 浮窗必须完整落在工作区内，这是「不被任务栏遮挡、不跑出屏幕」的硬约束。
func assertInsideWork(t *testing.T, x, y, w, h int32, work Rect) {
	t.Helper()
	if x < work.Left || y < work.Top || x+w > work.Right || y+h > work.Bottom {
		t.Fatalf("浮窗 (%d,%d,%dx%d) 超出工作区 %+v", x, y, w, h, work)
	}
}

func TestPopupPositionStaysInsideWorkArea(t *testing.T) {
	const w, h, gap int32 = 320, 56, 8
	cases := []struct {
		name string
		icon Rect
		work Rect
	}{
		{"任务栏在下", iconBottom, workBottom},
		{"任务栏在上", iconTop, workTop},
		{"任务栏在左", iconLeft, workLeft},
		{"任务栏在右", iconRight, workRight},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			x, y := PopupPosition(c.icon, c.work, w, h, gap)
			assertInsideWork(t, x, y, w, h, c.work)
		})
	}
}

func TestPopupPositionClampsAtScreenCorner(t *testing.T) {
	const w, h, gap int32 = 320, 56, 8
	// 图标贴着屏幕最右侧，居中对齐会让浮窗右边越界，必须被拉回。
	icon := Rect{Left: 1900, Top: 1048, Right: 1920, Bottom: 1072}
	x, y := PopupPosition(icon, workBottom, w, h, gap)
	assertInsideWork(t, x, y, w, h, workBottom)
	if x != workBottom.Right-w {
		t.Fatalf("x = %d, want 贴紧右边界 %d", x, workBottom.Right-w)
	}
}

func TestPopupPositionOnNegativeCoordinateMonitor(t *testing.T) {
	// 副显示器位于主屏左侧时工作区坐标为负，不能假设非负。
	const w, h, gap int32 = 320, 56, 8
	work := Rect{Left: -1920, Top: 0, Right: 0, Bottom: 1040}
	icon := Rect{Left: -220, Top: 1048, Right: -196, Bottom: 1072}
	x, y := PopupPosition(icon, work, w, h, gap)
	assertInsideWork(t, x, y, w, h, work)
}

func TestPopupPositionWhenPopupLargerThanWorkArea(t *testing.T) {
	// 浮窗比工作区还大时不应把左上角推到屏幕外，保证仍可见。
	work := Rect{Left: 0, Top: 0, Right: 200, Bottom: 200}
	icon := Rect{Left: 100, Top: 208, Right: 124, Bottom: 232}
	x, y := PopupPosition(icon, work, 320, 56, 8)
	if x != work.Left || y < work.Top {
		t.Fatalf("PopupPosition = (%d,%d), want 左上角兜底在 (%d,>=%d)", x, y, work.Left, work.Top)
	}
}

func TestRectContainsUsesHalfOpenBounds(t *testing.T) {
	r := Rect{Left: 10, Top: 20, Right: 30, Bottom: 40}
	if !r.Contains(10, 20) {
		t.Fatal("左上边界应算命中")
	}
	if r.Contains(30, 40) {
		t.Fatal("右下边界不应算命中")
	}
	if r.Contains(5, 25) {
		t.Fatal("左侧外部不应算命中")
	}
}

func TestBottomRightPosition(t *testing.T) {
	const w, h, gap int32 = 320, 360, 12
	work := Rect{Left: 0, Top: 0, Right: 1920, Bottom: 1040}
	x, y := BottomRightPosition(work, w, h, gap)
	if x != 1920-320-12 || y != 1040-360-12 {
		t.Fatalf("BottomRightPosition = (%d,%d), want (%d,%d)", x, y, 1920-320-12, 1040-360-12)
	}
	assertInsideWork(t, x, y, w, h, work)
}

func TestBottomRightPositionOnNegativeMonitor(t *testing.T) {
	const w, h, gap int32 = 320, 360, 12
	// 副屏在主屏左侧，工作区坐标为负，右下角定位不能假设非负。
	work := Rect{Left: -1920, Top: 0, Right: 0, Bottom: 1040}
	x, y := BottomRightPosition(work, w, h, gap)
	assertInsideWork(t, x, y, w, h, work)
}

func TestBottomRightPositionClampsWhenWindowTallerThanWork(t *testing.T) {
	// 窗口比工作区还高时，左上角兜底在工作区内而非被推出屏幕。
	work := Rect{Left: 0, Top: 0, Right: 300, Bottom: 200}
	x, y := BottomRightPosition(work, 320, 360, 12)
	if x != work.Left || y != work.Top {
		t.Fatalf("BottomRightPosition = (%d,%d), want 左上角兜底 (%d,%d)", x, y, work.Left, work.Top)
	}
}
