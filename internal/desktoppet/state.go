// Package desktoppet 保存桌面宠物的纯交互状态；不依赖窗口、COM 或传输生命周期。
package desktoppet

// Mode 只控制面板；宠物宿主始终可见。
type Mode uint8

const (
	Collapsed Mode = iota
	Hover
	Pinned
)

// Region 区分宠物与面板，跨区移动不能把面板收走。
type Region uint8

const (
	Pet Region = iota
	Panel
)

// ClickAction 是宿主应在所属 UI 线程处理的决议；状态机不打开窗口或访问后端。
type ClickAction uint8

const (
	NoClickAction ClickAction = iota
	PanelPinToggled
	OpenCurrentSpace
)

// State 仅由 UI 线程写。HideDue / ResolveClicks 由所属线程的定时器调用。
type State struct {
	Mode         Mode
	InPet        bool
	InPanel      bool
	ExternalDrag bool
	Dragging     bool
	pressed      bool
	originX      int32
	originY      int32
	pressMS      uint64
	pressCount   uint8
	clickCount   uint8
	lastClickX   int32
	lastClickY   int32
	lastClickMS  uint64
	clickWindow  uint32
	clickDueMS   uint64
	openPending  bool
}

func (s *State) Visible() bool  { return s.Mode != Collapsed }
func (s *State) IsPinned() bool { return s.Mode == Pinned }
func (s *State) Pressed() bool  { return s.pressed }

// Pointer 返回是否应安排隐藏。离开时不立即改 Mode，留出跨两窗的时间。
func (s *State) Pointer(region Region, inside bool) bool {
	if region == Pet {
		s.InPet = inside
	} else {
		s.InPanel = inside
	}
	if inside && !s.Dragging && s.Mode == Collapsed {
		s.Mode = Hover
	}
	return s.WantsHide()
}

func (s *State) WantsHide() bool {
	return s.Mode == Hover && !s.InPet && !s.InPanel && !s.Dragging && !s.ExternalDrag && !s.pressed
}

// HideDue 再用原生坐标采样结果校正 DOM 进出事件；过期事件不能误收跨区后的面板。
func (s *State) HideDue(inPet, inPanel bool) bool {
	s.InPet, s.InPanel = inPet, inPanel
	if s.WantsHide() {
		s.Mode = Collapsed
		return true
	}
	return false
}

func (s *State) TogglePin() {
	if s.Mode == Pinned {
		s.Mode = Hover
	} else {
		s.Mode = Pinned
	}
}

// Collapse 只收面板，不销毁宠物、任务或后端；固定态不受普通隐藏请求影响。
func (s *State) Collapse() {
	if s.Mode != Pinned {
		s.Mode = Collapsed
	}
}

func (s *State) SetExternalDrag(active bool) bool {
	s.ExternalDrag = active
	if active && s.Mode == Collapsed {
		s.Mode = Hover
	}
	return s.WantsHide()
}

// Press 不在按下时切换固定态；连击以系统按下间隔与双击矩形判定。
// 宿主应先调用 ResolveClicks(nowMS) 消费过期待决动作，再处理新的按下。
func (s *State) Press(x, y int32, nowMS uint64, doubleClickMS uint32, doubleX, doubleY int32) {
	if s.pressed {
		s.CancelPress()
	}
	count := uint8(1)
	if s.clickCount > 0 && !s.openPending && nowMS >= s.lastClickMS && nowMS <= s.clickDueMS &&
		nowMS-s.lastClickMS <= uint64(doubleClickMS) &&
		withinClick(x, s.lastClickX, doubleX) && withinClick(y, s.lastClickY, doubleY) {
		count = s.clickCount + 1
	} else {
		s.clearClicks()
	}
	s.pressed = true
	s.Dragging = false
	s.originX, s.originY, s.pressMS = x, y, nowMS
	s.pressCount, s.clickWindow = count, doubleClickMS
}

// Move 采用系统 SM_CXDRAG/SM_CYDRAG 阈值；微小抖动与双击不能触发移动。
func (s *State) Move(x, y, thresholdX, thresholdY int32) bool {
	if !s.pressed || s.Dragging {
		return false
	}
	if withinClick(x, s.originX, thresholdX) && withinClick(y, s.originY, thresholdY) {
		return false
	}
	s.Dragging = true
	s.clearClicks()
	return true
}

// Release 保留既有 bool 签名，但不再即时固定，因此返回 false。
// 第二击只挂起双击；第三击释放后由 ResolveClicks 返回 OpenCurrentSpace，绝不先固定。
func (s *State) Release(x, y int32, nowMS uint64) bool {
	if !s.pressed {
		return false
	}
	s.pressed = false
	if s.Dragging || nowMS < s.pressMS {
		s.Dragging = false
		s.clearClicks()
		return false
	}
	s.clickCount = s.pressCount
	// 系统双击时间与矩形以按下为基准，不能被长按后的释放重新延长。
	s.lastClickX, s.lastClickY, s.lastClickMS = s.originX, s.originY, s.pressMS
	s.clickDueMS = clickDeadline(s.pressMS, s.clickWindow)
	s.pressCount = 0
	if s.clickCount == 3 {
		s.openPending = true
	}
	return false
}

// ResolveClicks 由 UI 的连击 WM_TIMER，以及每次 Press 前/Release 后调用。
// 第三击按下尚未释放时暂不决议，允许它变为拖动或被失捕获/右键取消。
// 时间边界包含在连击窗口中，因此 deadline 本帧先不固定，下一个毫秒才决议。
func (s *State) ResolveClicks(nowMS uint64) ClickAction {
	if s.pressed || s.Dragging {
		return NoClickAction
	}
	if s.openPending {
		s.clearClicks()
		return OpenCurrentSpace
	}
	if s.clickCount == 0 || nowMS <= s.clickDueMS {
		return NoClickAction
	}
	count := s.clickCount
	s.clearClicks()
	if count == 2 {
		s.TogglePin()
		return PanelPinToggled
	}
	return NoClickAction
}

// ClickDeadline 供宿主安排一次 UI 定时器；第三击释放后已可立即消费。
// 返回的 deadline 是包含边界，ResolveClicks 在 deadline+1 决议双击。
func (s *State) ClickDeadline() (deadlineMS uint64, pending bool) {
	return s.clickDueMS, s.clickCount != 0 || s.openPending
}

// CancelPress 用于拖动取消、失捕获与右键；连已经释放的待决双击也一并清除。
func (s *State) CancelPress() {
	s.pressed, s.Dragging = false, false
	s.clearClicks()
}

func (s *State) clearClicks() {
	s.pressCount, s.clickCount = 0, 0
	s.clickDueMS = 0
	s.openPending = false
}

func withinClick(value, origin, systemSize int32) bool {
	distance := int64(value) - int64(origin)
	if distance < 0 {
		distance = -distance
	}
	return distance <= int64(max(0, systemSize))/2
}

func clickDeadline(nowMS uint64, window uint32) uint64 {
	if ^uint64(0)-nowMS < uint64(window) {
		return ^uint64(0)
	}
	return nowMS + uint64(window)
}
