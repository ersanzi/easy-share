package desktoppet

import (
	"math"
	"testing"
)

func closeAnimationValue(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("动画位置=%v，期望=%v", got, want)
	}
}

func TestAnimationDefaultElapsedDurations(t *testing.T) {
	var a Animation
	closeAnimationValue(t, a.Tick(0), 0)
	if a.Running() || a.Expanded() {
		t.Fatal("零值应为停止且收起")
	}
	closeAnimationValue(t, a.SetExpanded(true, 100, false), 0)
	if !a.Running() || !a.Expanded() {
		t.Fatal("展开未启动")
	}
	closeAnimationValue(t, a.Tick(275), RevealEase(.5))
	if !a.Running() {
		t.Fatal("展开尚未到220ms就停止")
	}
	closeAnimationValue(t, a.Tick(449), RevealEase(349.0/350))
	closeAnimationValue(t, a.Tick(450), 1)
	if a.Running() {
		t.Fatal("展开终帧未停止")
	}
	closeAnimationValue(t, a.SetExpanded(false, 500, false), 1)
	closeAnimationValue(t, a.Tick(675), 1-RevealEase(.5))
	if !a.Running() || a.Expanded() {
		t.Fatal("收起中状态错误")
	}
	closeAnimationValue(t, a.Tick(850), 0)
	if a.Running() {
		t.Fatal("收起160ms后未停止")
	}
}

func TestAnimationReverseContinuesAtCurrentPosition(t *testing.T) {
	var a Animation
	a.SetExpanded(true, 0, false)
	before := a.Tick(70)
	closeAnimationValue(t, a.SetExpanded(false, 70, false), before)
	closeAnimationValue(t, a.Tick(70), before)
	after := a.Tick(90)
	if after >= before || after <= 0 {
		t.Fatalf("反向未连续向收起移动：%v -> %v", before, after)
	}
	closeAnimationValue(t, a.SetExpanded(true, 90, false), after)
	if got := a.Tick(100); got <= after || got >= 1 {
		t.Fatalf("再次反向未继续展开：%v -> %v", after, got)
	}
	closeAnimationValue(t, a.Tick(1000), 1)
	if a.Running() {
		t.Fatal("迟到终帧仍运行")
	}
}

func TestAnimationRepeatTargetDoesNotRestart(t *testing.T) {
	var a Animation
	a.SetExpanded(true, 10, false)
	a.SetExpanded(true, 100, false)
	closeAnimationValue(t, a.Tick(360), 1)
	if a.Running() {
		t.Fatal("重复展开重置了220ms终点")
	}
	a.SetExpanded(false, 400, false)
	a.SetExpanded(false, 480, false)
	closeAnimationValue(t, a.Tick(750), 0)
	if a.Running() {
		t.Fatal("重复收起重置了160ms终点")
	}
}

func TestAnimationElapsedIsIndependentOfTimerFrequency(t *testing.T) {
	var frequent, delayed Animation
	frequent.SetExpanded(true, 50, false)
	delayed.SetExpanded(true, 50, false)
	for now := uint64(50); now <= 170; now += 10 {
		frequent.Tick(now)
	}
	closeAnimationValue(t, frequent.Tick(170), delayed.Tick(170))
	closeAnimationValue(t, delayed.Tick(500), 1)
}

func TestAnimationReducedMotionIsInstantIncludingReversal(t *testing.T) {
	var a Animation
	closeAnimationValue(t, a.SetExpanded(true, 0, true), 1)
	if a.Running() {
		t.Fatal("减少动画仍启动定时器")
	}
	a.SetExpanded(false, 10, false)
	a.Tick(50)
	closeAnimationValue(t, a.SetExpanded(true, 50, true), 1)
	closeAnimationValue(t, a.SetExpanded(false, 51, true), 0)
	if a.Running() || a.Expanded() {
		t.Fatal("减少动画未立即收起")
	}
}

func TestAnimationBackwardsTimeAndBounds(t *testing.T) {
	var a Animation
	a.SetExpanded(true, 100, false)
	closeAnimationValue(t, a.Tick(99), 0)
	before := a.Tick(180)
	closeAnimationValue(t, a.Tick(150), before)
	closeAnimationValue(t, a.Tick(^uint64(0)), 1)
	a.SetExpanded(false, 1000, false)
	before = a.Tick(1050)
	closeAnimationValue(t, a.Tick(1020), before)
	closeAnimationValue(t, a.Tick(2000), 0)
}

func TestAnimationRepeatedReversalStaysBounded(t *testing.T) {
	var a Animation
	for now := uint64(0); now < 500; now += 7 {
		value := a.SetExpanded(now%2 == 0, now, false)
		if value < 0 || value > 1 || math.IsNaN(value) {
			t.Fatalf("反向位置越界：%v", value)
		}
	}
	a.SetExpanded(false, 500, false)
	closeAnimationValue(t, a.Tick(1000), 0)
}
