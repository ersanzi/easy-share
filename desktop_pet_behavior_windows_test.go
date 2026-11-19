//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"easyshare/internal/desktoppet"
)

func TestPetClickTimerUsesDeadlinePlusOne(t *testing.T) {
	cases := []struct {
		now, deadline uint64
		want          uint32
	}{
		{210, 700, 491}, {700, 700, 1}, {701, 700, 1},
		{0, ^uint64(0), ^uint32(0)},
	}
	for _, test := range cases {
		if got := petClickTimerDelay(test.now, test.deadline); got != test.want {
			t.Fatalf("delay(%d,%d)=%d want=%d", test.now, test.deadline, got, test.want)
		}
	}
}

func TestDesktopPetClickResolutionCallsOriginalOpenSpaceOnce(t *testing.T) {
	opened := make(chan struct{}, 2)
	popup := &hoverPopup{onOpenSpace: func() { opened <- struct{}{} }}
	pet := &desktopPet{popup: popup}
	popup.pet = pet
	pet.state.Pointer(desktoppet.Pet, true)
	for _, now := range []uint64{100, 200, 300} {
		pet.state.Press(0, 0, now, 500, 8, 8)
		pet.state.Release(0, 0, now+10)
		pet.resolveClicks(now + 10)
		if pet.state.IsPinned() {
			t.Fatal("三击在回调前先固定")
		}
	}
	select {
	case <-opened:
	case <-time.After(time.Second):
		t.Fatal("三击没有调用原 onOpenSpace")
	}
	pet.resolveClicks(1000)
	select {
	case <-opened:
		t.Fatal("三击回调重复调用")
	default:
	}
}

func TestDesktopPetDoubleResolutionUpdatesPinnedOnlyAfterDeadline(t *testing.T) {
	popup := &hoverPopup{}
	pet := &desktopPet{popup: popup}
	popup.pet = pet
	pet.state.Pointer(desktoppet.Pet, true)
	for _, now := range []uint64{100, 200} {
		pet.state.Press(0, 0, now, 500, 8, 8)
		pet.state.Release(0, 0, now+10)
		pet.resolveClicks(now + 10)
	}
	pet.resolveClicks(700)
	if pet.state.IsPinned() || popup.IsPinned() {
		t.Fatal("deadline 边界提前固定")
	}
	pet.resolveClicks(701)
	if !pet.state.IsPinned() || !popup.IsPinned() {
		t.Fatal("deadline+1 未同步固定态")
	}
}

func TestDesktopPetHiddenRetainsBackendAndDropSnapshot(t *testing.T) {
	popup := &hoverPopup{done: make(chan struct{}), onFilesDropped: func([]string, string) {}}
	pet := &desktopPet{popup: popup, started: time.Now()}
	popup.pet = pet
	pet.state.Mode = desktoppet.Pinned
	pet.state.InPet, pet.state.InPanel, pet.state.ExternalDrag = true, true, true
	pet.state.Press(0, 0, 100, 500, 8, 8)
	pet.state.Release(0, 0, 110)
	popup.dropStatus = [3]string{"正在上传", "不因隐藏停止", "busy"}
	popup.animation.SetExpanded(true, 0, false)
	popup.animationTimer = true
	popup.SetPetVisible(false)
	pet.setVisibleOnOwnThread(false)
	if popup.closed.Load() || popup.pet != pet || popup.onFilesDropped == nil || !pet.state.IsPinned() {
		t.Fatal("隐藏销毁 backend/回调或解除固定")
	}
	if popup.animation.Running() || popup.animationTimer || pet.hidePending || pet.pointerTimer || pet.state.Pressed() || pet.state.ExternalDrag {
		t.Fatal("隐藏未取消 UI 计时器/交互")
	}
	if _, pending := pet.state.ClickDeadline(); pending {
		t.Fatal("隐藏保留了待决连击")
	}
	if popup.dropStatus != ([3]string{"正在上传", "不因隐藏停止", "busy"}) {
		t.Fatal("隐藏清空上传状态")
	}
	select {
	case <-popup.done:
		t.Fatal("隐藏结束了 backend 生命周期")
	default:
	}
	popup.SetPetVisible(true)
	pet.setVisibleOnOwnThread(true)
	if pet.hidden || !popup.IsPetVisible() || !pet.state.IsPinned() {
		t.Fatal("显示未保留固定态")
	}
}

func TestDesktopPetMenuAndExternalDragKeepSamePanel(t *testing.T) {
	popup := &hoverPopup{}
	pet := &desktopPet{popup: popup}
	popup.pet = pet
	pet.state.Mode = desktoppet.Hover
	pet.state.Dragging = true
	if pet.wantsPanel() {
		t.Fatal("普通未固定拖宠物时仍请求展开")
	}
	pet.state.ExternalDrag = true
	if !pet.wantsPanel() {
		t.Fatal("外部拖入期间误收面板")
	}
	pet.state.ExternalDrag = false
	pet.menuOpen = true
	if !pet.wantsPanel() {
		t.Fatal("宠物菜单期间误收面板")
	}
	pet.menuOpen = false
	popup.SetMenuActive(true)
	if !pet.wantsPanel() {
		t.Fatal("托盘菜单期间误收面板")
	}
	popup.SetPetVisible(false)
	if pet.wantsPanel() {
		t.Fatal("菜单保护覆盖了显式隐藏")
	}
}

func TestTrayPetVisibilityMenuIsDynamicAndNonDestructive(t *testing.T) {
	popup := &hoverPopup{}
	text, enabled, show := trayPetMenuItem(popup)
	if text != "隐藏宠物" || !enabled || show {
		t.Fatalf("显示态菜单错误：%q %v %v", text, enabled, show)
	}
	popup.SetPetVisible(false)
	text, enabled, show = trayPetMenuItem(popup)
	if text != "显示宠物" || !enabled || !show || popup.closed.Load() {
		t.Fatalf("隐藏态菜单错误或销毁 popup：%q %v %v", text, enabled, show)
	}
	popup.SetPetVisible(true)
	if text, _, show := trayPetMenuItem(popup); text != "隐藏宠物" || show {
		t.Fatal("重新显示未更新菜单")
	}
	popup.closed.Store(true)
	if _, enabled, _ := trayPetMenuItem(popup); enabled {
		t.Fatal("已关闭 popup 的菜单仍启用")
	}
	if _, enabled, _ := trayPetMenuItem(nil); enabled {
		t.Fatal("初始化失败的宠物菜单仍启用")
	}
}

func TestDesktopPetMechanicalWiringDoesNotReplaceDropOrAddNoActivate(t *testing.T) {
	root := testPackageDir(t)
	read := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	pet := read("desktop_pet_windows.go")
	popup := read("tray_hover_windows.go")
	if !strings.Contains(pet, "p.resolveClicks(now)") || !strings.Contains(pet, "p.resolveClicks(p.nowMS())") || !strings.Contains(pet, "go p.popup.onOpenSpace()") {
		t.Fatal("down/up/timer 或原打开空间回调未接线")
	}
	if !strings.Contains(popup, "p.animation.SetExpanded") || !strings.Contains(popup, "p.animation.Tick") || !strings.Contains(popup, "SetClippedRoundedRegion") {
		t.Fatal("真实位置/裁剪动画未接线")
	}
	if strings.Contains(pet, "time.Sleep(") || strings.Contains(popup, "time.Sleep(") {
		t.Fatal("Win32 动画使用了 sleep 循环")
	}
	if strings.Contains(popup, "SetNoActivate(") || strings.Contains(popup, "winui.WSExNoActivate") {
		t.Fatal("上传面板被加了不可激活扩展样式")
	}
	for _, required := range []string{"ShowWindowNoActivate(p.hwnd)", "MessageWithAdditionalObjectsCallback = p.handleDroppedObjects", "args.GetAdditionalObjects()", "file.GetPath()", "go p.onFilesDropped(paths, space)"} {
		if !strings.Contains(popup, required) {
			t.Fatalf("原拖放通道缺失：%s", required)
		}
	}
}

func TestDesktopPetHiddenIgnoresLatePointerDragAndPinMessages(t *testing.T) {
	popup := &hoverPopup{}
	pet := &desktopPet{popup: popup, hidden: true}
	popup.pet = pet
	popup.SetPetVisible(false)
	for _, message := range []string{"pointer-enter", "pointer-leave", "external-drag-enter", "external-drag-leave", "pin-toggle"} {
		popup.handleWebMessage(message, nil, nil)
	}
	if pet.state.Visible() || pet.state.ExternalDrag || pet.state.InPet || pet.state.InPanel || popup.animation.Running() {
		t.Fatal("隐藏后的排队 DOM 事件重新开启 UI")
	}
}
