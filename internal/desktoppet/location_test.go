package desktoppet

import (
	"math"
	"testing"

	"easyshare/internal/winui"
)

func TestPanelRectPrefersBelowWithoutMovingPet(t *testing.T) {
	pet := winui.Rect{Left: 500, Top: 100, Right: 596, Bottom: 164}
	before := pet
	work := winui.Rect{Left: 0, Top: 0, Right: 1920, Bottom: 1040}
	got := PanelRect(pet, work, 320, 300, 6)
	want := winui.Rect{Left: 388, Top: 170, Right: 708, Bottom: 470}
	if got != want || pet != before {
		t.Fatalf("下方展开布局错误或移动宠物：panel=%+v pet=%+v", got, pet)
	}
}

func TestPanelRectBottomFallbackStaysInWorkArea(t *testing.T) {
	pet := winui.Rect{Left: 1810, Top: 950, Right: 1906, Bottom: 1014}
	before := pet
	work := winui.Rect{Right: 1920, Bottom: 1040}
	got := PanelRect(pet, work, 320, 300, 6)
	if got.Top != 644 || got.Bottom != 944 || got.Right != work.Right || pet != before {
		t.Fatalf("底部不足未在工作区适配或移动宠物：%+v", got)
	}
}

func TestPanelRectNegativeMonitorSmallScreenAndInvalidWork(t *testing.T) {
	cases := []struct {
		name      string
		pet, work winui.Rect
		w, h, gap int32
	}{
		{"负坐标显示器", winui.Rect{Left: -1000, Top: -200, Right: -856, Bottom: -104}, winui.Rect{Left: -1920, Top: -500, Right: 0, Bottom: 580}, 480, 450, 9},
		{"窄小工作区", winui.Rect{Left: 90, Top: 40, Right: 186, Bottom: 104}, winui.Rect{Left: 10, Top: 20, Right: 210, Bottom: 140}, 320, 300, 6},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := PanelRect(test.pet, test.work, test.w, test.h, test.gap)
			if got.Left < test.work.Left || got.Top < test.work.Top || got.Right > test.work.Right || got.Bottom > test.work.Bottom || got.Width() <= 0 || got.Height() <= 0 {
				t.Fatalf("面板越过工作区：%+v work=%+v", got, test.work)
			}
		})
	}
	if got := PanelRect(winui.Rect{}, winui.Rect{}, 320, 300, 6); got != (winui.Rect{}) {
		t.Fatalf("无效工作区未拒绝：%+v", got)
	}
}

func TestPanelFrameSlidesDownAndClipsWithinFinalWork(t *testing.T) {
	final := winui.Rect{Left: -500, Top: 100, Right: -180, Bottom: 400}
	previousTop, previousBottom := int32(-1<<31), final.Top
	for _, progress := range []float64{0, .1, .25, .5, .75, 1} {
		window, clip := PanelFrame(final, progress, 18)
		if window.Width() != final.Width() || window.Height() != final.Height() {
			t.Fatal("动画缩放宿主导致 WebView 逐帧重排")
		}
		if progress == 0 {
			if clip != (winui.Rect{}) {
				t.Fatal("收起端点裁剪区应为空")
			}
			continue
		}
		top, bottom := window.Top+clip.Top, window.Top+clip.Bottom
		if top != final.Top || bottom > final.Bottom || bottom < previousBottom || window.Top < previousTop {
			t.Fatalf("未向下滑出或可见区越界：window=%+v clip=%+v", window, clip)
		}
		if clip.Top < 0 || clip.Bottom > window.Height() || clip.Width() != final.Width() {
			t.Fatalf("客户区裁剪越界：%+v", clip)
		}
		previousTop, previousBottom = window.Top, bottom
	}
	window, clip := PanelFrame(final, 1, 18)
	if window != final || clip != (winui.Rect{Right: 320, Bottom: 300}) {
		t.Fatalf("展开终点不是完整原面板：%+v %+v", window, clip)
	}
}

func TestPanelFrameReversalProjectsCurrentAnimationValue(t *testing.T) {
	final := winui.Rect{Right: 320, Bottom: 300}
	var animation Animation
	animation.SetExpanded(true, 0, false)
	value := animation.Tick(70)
	beforeWindow, beforeClip := PanelFrame(final, value, 18)
	afterValue := animation.SetExpanded(false, 70, false)
	afterWindow, afterClip := PanelFrame(final, afterValue, 18)
	if beforeWindow != afterWindow || beforeClip != afterClip {
		t.Fatal("反向衔接发生位置/裁剪跳跃")
	}
}

func TestPanelFrameClampsProgressAndTravel(t *testing.T) {
	final := winui.Rect{Left: 2, Top: 3, Right: 8, Bottom: 7}
	for _, progress := range []float64{-1, 0, .01, .5, 1, 2, math.Inf(1)} {
		_, clip := PanelFrame(final, progress, 100)
		if clip.Top < 0 || clip.Bottom > final.Height() || clip.Right > final.Width() {
			t.Fatalf("极小面板的动画裁剪越界：%+v", clip)
		}
	}
}
