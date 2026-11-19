//go:build windows

package main

import wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

// openDesktopPetView 只走既有主窗显示与轻量导航事件，不新增 Wails 导出绑定。
// 主窗 App.vue 消费 desktop:open-view（settings / transfers）；账号/权限仍由原页面处理。
func openDesktopPetView(a *App, view string) {
	a.showWindow()
	wailsruntime.EventsEmit(a.ctx, "desktop:open-view", view)
}
