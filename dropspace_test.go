package main

import (
	"log"
	"os"
	"testing"

	"easyshare/internal/drive"
	"easyshare/internal/namespace"
)

// newSpaceTestApp 造一个只够测「目标空间」逻辑的 App。
func newSpaceTestApp() *App {
	return &App{logger: log.New(os.Stderr, "", 0)}
}

func TestTargetSpaceDefaultsToPersonal(t *testing.T) {
	app := newSpaceTestApp()
	// 没选过时必须回落个人空间：任何开通了空间的账号都有它，比报错合理
	if got := app.targetSpace(); got != drive.SpacePersonal {
		t.Errorf("未选择时应回落个人空间，实得 %q", got)
	}
}

func TestTargetSpaceFollowsSelection(t *testing.T) {
	app := newSpaceTestApp()

	app.SetDropSpace(drive.SpaceShared)
	if got := app.targetSpace(); got != drive.SpaceShared {
		t.Errorf("切到共享后应为 shared，实得 %q", got)
	}

	app.SetDropSpace(drive.SpacePersonal)
	if got := app.targetSpace(); got != drive.SpacePersonal {
		t.Errorf("切回个人后应为 personal，实得 %q", got)
	}

	// 清空选择（退出登录等）后同样回落个人空间，不能留着上个账号的选择
	app.SetDropSpace("")
	if got := app.targetSpace(); got != drive.SpacePersonal {
		t.Errorf("清空选择后应回落个人空间，实得 %q", got)
	}
}

// 切换器档位与拖放区提示共用 spaceLabel。两处对不上时，界面会说「共享」
// 而文件实际进了个人空间——这种错用户察觉不到，所以钉住它。
func TestSpaceLabel(t *testing.T) {
	if got := spaceLabel(drive.SpaceShared); got != "共享" {
		t.Errorf("共享空间标签 = %q，应为「共享」", got)
	}
	if got := spaceLabel(drive.SpacePersonal); got != "网盘" {
		t.Errorf("个人空间标签 = %q，应为「网盘」", got)
	}
	// 未知取值回落个人空间的文案，与 targetSpace 的回落方向一致
	if got := spaceLabel(""); got != "网盘" {
		t.Errorf("空取值应回落「网盘」，实得 %q", got)
	}
}

// 开关档位 → 打开哪个盘 → 上传进哪个空间，三者必须一一对应。
//
// 这是产品的核心约定：滑到「共享」就该打开共享盘、文件也进共享空间。
// 错位的后果很隐蔽——用户以为传进了共享，实际进了个人空间，界面上看不出来。
func TestSwitcherMapsToSameDriveAndSpace(t *testing.T) {
	cases := []struct {
		selected  string
		wantSpace string
		wantCLSID string
		wantLabel string
	}{
		{drive.SpacePersonal, drive.SpacePersonal, namespace.PersonalCLSID(), "网盘"},
		{drive.SpaceShared, drive.SpaceShared, namespace.SharedCLSID(), "共享"},
	}
	for _, tc := range cases {
		app := newSpaceTestApp()
		app.SetDropSpace(tc.selected)

		// 上传的目标空间
		if got := app.targetSpace(); got != tc.wantSpace {
			t.Errorf("选中 %q 后上传空间 = %q，应为 %q", tc.selected, got, tc.wantSpace)
		}
		// 界面文案
		if got := spaceLabel(app.targetSpace()); got != tc.wantLabel {
			t.Errorf("选中 %q 后文案 = %q，应为 %q", tc.selected, got, tc.wantLabel)
		}
		// 「打开」按钮要打开的那个盘。调的是 OpenCurrentSpace 用的同一个函数，
		// 不在测试里重抄一遍逻辑——重抄的测试永远不会失败，等于没测。
		if clsid := spaceCLSID(app.targetSpace()); clsid != tc.wantCLSID {
			t.Errorf("选中 %q 后要打开的盘 = %q，应为 %q", tc.selected, clsid, tc.wantCLSID)
		}
	}
}
