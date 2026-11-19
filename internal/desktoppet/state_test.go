package desktoppet

import "testing"

func TestHoverPinnedCollapsed(t *testing.T) {
	var s State
	if s.Visible() {
		t.Fatal("启动面板应收起")
	}
	s.Pointer(Pet, true)
	if s.Mode != Hover {
		t.Fatal("hover 未展开")
	}
	s.TogglePin()
	s.Pointer(Pet, false)
	if s.HideDue(false, false) || s.Mode != Pinned {
		t.Fatal("固定态被隐藏")
	}
	s.TogglePin()
	if !s.HideDue(false, false) || s.Mode != Collapsed {
		t.Fatal("解除固定后未收起")
	}
}

func TestCrossRegionAndExternalDropDoNotCollapse(t *testing.T) {
	var s State
	s.Pointer(Pet, true)
	if !s.Pointer(Pet, false) {
		t.Fatal("离开应安排短延迟")
	}
	s.Pointer(Panel, true)
	if s.HideDue(false, true) {
		t.Fatal("跨到面板被隐藏")
	}
	s.Pointer(Panel, false)
	s.SetExternalDrag(true)
	if s.HideDue(false, false) {
		t.Fatal("外部 drop 期间被隐藏")
	}
	s.SetExternalDrag(false)
	if !s.HideDue(false, false) {
		t.Fatal("离开两区未收起")
	}
}

func TestDragThresholdAndDoubleClick(t *testing.T) {
	var s State
	s.Pointer(Pet, true)
	s.Press(10, 10, 100, 500, 8, 8)
	if s.Move(12, 12, 8, 8) {
		t.Fatal("抖动误触拖动")
	}
	if s.Release(12, 12, 110) {
		t.Fatal("单击误触固定")
	}
	s.Press(12, 12, 200, 500, 8, 8)
	if s.Release(12, 12, 210) || s.IsPinned() {
		t.Fatal("第二击释放不能先固定，必须留出三击窗口")
	}
	if deadline, pending := s.ClickDeadline(); !pending || deadline != 700 {
		t.Fatalf("系统连击截止时间错误：%d pending=%v", deadline, pending)
	}
	if s.ResolveClicks(700) != NoClickAction || s.IsPinned() {
		t.Fatal("连击窗口边界仍应允许第三击")
	}
	if s.ResolveClicks(701) != PanelPinToggled || !s.IsPinned() {
		t.Fatal("双击到期未固定")
	}
	if s.ResolveClicks(702) != NoClickAction {
		t.Fatal("双击重复决议")
	}
	// 固定只控制展开，不锁位置。
	s.Press(12, 12, 800, 500, 8, 8)
	if !s.Move(30, 12, 8, 8) {
		t.Fatal("固定态不能拖")
	}
	if s.Release(30, 12, 820) || !s.IsPinned() {
		t.Fatal("拖动误解除固定")
	}
}

func TestSecondClickCanBecomeDragAndCaptureLossCancels(t *testing.T) {
	var s State
	s.Press(0, 0, 100, 500, 8, 8)
	s.Release(0, 0, 110)
	s.Press(0, 0, 200, 500, 8, 8)
	if !s.Move(20, 0, 8, 8) {
		t.Fatal("第二次按下不能拖")
	}
	if s.Release(20, 0, 230) || s.IsPinned() {
		t.Fatal("拖动后仍执行了双击")
	}
	s.Press(20, 0, 300, 500, 8, 8)
	s.CancelPress()
	if s.Release(20, 0, 310) || s.IsPinned() {
		t.Fatal("丢失捕获仍触发双击")
	}
}

func TestTripleClickWinsWithoutIntermediatePin(t *testing.T) {
	for _, mode := range []Mode{Hover, Pinned} {
		s := State{Mode: mode}
		for _, now := range []uint64{100, 200} {
			s.Press(10, 10, now, 500, 8, 8)
			if s.Release(10, 10, now+10) || s.Mode != mode {
				t.Fatalf("三击前两击已改变固定态：%v", s.Mode)
			}
		}
		s.Press(10, 10, 300, 500, 8, 8)
		// 第三击按住越过定时器截止也不允许先固定。
		if s.ResolveClicks(800) != NoClickAction || s.Mode != mode {
			t.Fatal("第三击尚未释放就决议双击")
		}
		s.Release(10, 10, 810)
		if s.ResolveClicks(810) != OpenCurrentSpace || s.Mode != mode {
			t.Fatal("三击未优先打开，或改变了原有固定态")
		}
		if s.ResolveClicks(2000) != NoClickAction || s.Mode != mode {
			t.Fatal("三击后的旧定时器仍固定面板")
		}
		if _, pending := s.ClickDeadline(); pending {
			t.Fatal("三击动作消费后仍有待决定时器")
		}
	}
}

func TestDoubleClickUnpinsOnlyAfterSystemWindow(t *testing.T) {
	s := State{Mode: Pinned}
	s.Press(0, 0, 100, 250, 8, 8)
	s.Release(0, 0, 110)
	s.Press(0, 0, 200, 250, 8, 8)
	s.Release(0, 0, 210)
	if s.ResolveClicks(450) != NoClickAction || !s.IsPinned() {
		t.Fatal("解除固定早于系统连击窗口结束")
	}
	if s.ResolveClicks(451) != PanelPinToggled || s.Mode != Hover {
		t.Fatal("延迟双击未解除固定")
	}
}

func TestTripleClickAtSystemTimeAndSpatialBoundary(t *testing.T) {
	var s State
	s.Press(0, 0, 100, 500, 8, 8)
	s.Release(0, 0, 110)
	s.Press(4, -4, 600, 500, 8, 8)
	s.Release(4, -4, 610)
	if s.ResolveClicks(1100) != NoClickAction {
		t.Fatal("包含边界提前决议")
	}
	s.Press(8, -8, 1100, 500, 8, 8)
	s.Release(8, -8, 1110)
	if s.ResolveClicks(1110) != OpenCurrentSpace || s.IsPinned() {
		t.Fatal("系统时间/矩形边界的三击未优先打开")
	}
}

func TestClickUsesDownTimeNotExtendedByLongRelease(t *testing.T) {
	var s State
	s.Press(0, 0, 100, 500, 8, 8)
	s.Release(0, 0, 700)
	if s.ResolveClicks(700) != NoClickAction {
		t.Fatal("长单击产生动作")
	}
	s.Press(0, 0, 710, 500, 8, 8)
	s.Release(0, 0, 720)
	if s.ResolveClicks(1211) != NoClickAction || s.IsPinned() {
		t.Fatal("释放时间人为延长了系统双击窗口")
	}
}

func TestClickOutsideSystemTimeOrRectangleIsNotDouble(t *testing.T) {
	cases := []struct {
		name string
		x, y int32
		now  uint64
	}{
		{"超时", 0, 0, 601},
		{"横向超界", 5, 0, 200},
		{"纵向超界", 0, -5, 200},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var s State
			s.Press(0, 0, 100, 500, 8, 8)
			s.Release(0, 0, 110)
			s.ResolveClicks(test.now)
			s.Press(test.x, test.y, test.now, 500, 8, 8)
			s.Release(test.x, test.y, test.now+10)
			if s.ResolveClicks(test.now+501) != NoClickAction || s.IsPinned() {
				t.Fatal("不符合系统阈值却固定")
			}
		})
	}
}

func TestPendingDoubleCanceledByDragCaptureLossOrRightClick(t *testing.T) {
	cases := []string{"drag", "capture-loss", "right-click"}
	for _, kind := range cases {
		t.Run(kind, func(t *testing.T) {
			var s State
			s.Press(0, 0, 100, 500, 8, 8)
			s.Release(0, 0, 110)
			s.Press(0, 0, 200, 500, 8, 8)
			s.Release(0, 0, 210)
			if kind == "drag" {
				s.Press(0, 0, 300, 500, 8, 8)
				if !s.Move(20, 0, 8, 8) {
					t.Fatal("第三击不能变为拖动")
				}
				s.Release(20, 0, 310)
			} else {
				// 失捕获/右键共用取消接口，待决双击已释放也必须取消。
				s.CancelPress()
			}
			if s.ResolveClicks(1000) != NoClickAction || s.IsPinned() {
				t.Fatal("取消后仍有固定或打开副作用")
			}
			if _, pending := s.ClickDeadline(); pending {
				t.Fatal("取消后仍要求定时器")
			}
		})
	}
}

func TestThirdClickCaptureLossAndPendingOpenAreCancelable(t *testing.T) {
	for _, releaseThird := range []bool{false, true} {
		var s State
		for _, now := range []uint64{100, 200, 300} {
			s.Press(0, 0, now, 500, 8, 8)
			if now != 300 || releaseThird {
				s.Release(0, 0, now+10)
			}
		}
		s.CancelPress()
		s.Release(0, 0, 320)
		if s.ResolveClicks(1000) != NoClickAction || s.IsPinned() {
			t.Fatal("取消三击后仍打开或固定")
		}
	}
}

func TestClickThresholdHandlesNegativeScreensWithoutOverflow(t *testing.T) {
	var s State
	s.Press(-2147483648, 0, 100, 500, 8, 8)
	s.Release(-2147483648, 0, 110)
	s.Press(2147483647, 0, 200, 500, 8, 8)
	s.Release(2147483647, 0, 210)
	if s.ResolveClicks(701) != NoClickAction || s.IsPinned() {
		t.Fatal("跨坐标极值溢出造成误双击")
	}
	s.Press(-2147483648, 0, 800, 500, 8, 8)
	if !s.Move(2147483647, 0, 8, 8) {
		t.Fatal("跨坐标极值溢出吞掉拖动")
	}
	s.CancelPress()
}
