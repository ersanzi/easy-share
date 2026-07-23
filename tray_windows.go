//go:build windows

package main

import (
	_ "embed"

	"github.com/getlantern/systray"
)

//go:embed build/windows/icon.ico
var trayIcon []byte

// startTray 启动 Windows 通知区域图标。systray 的 Windows 实现不接管
// Wails 的窗口消息循环，因此保留现有实现。
func startTray(app *App) {
	app.trayOnce.Do(func() {
		go systray.Run(func() {
			systray.SetIcon(trayIcon)
			systray.SetTitle("EasyShare")
			systray.SetTooltip("EasyShare - 局域网文件传输")

			mOpen := systray.AddMenuItem("打开主窗口", "显示 EasyShare 主界面")
			systray.AddSeparator()
			mStatus := systray.AddMenuItem("服务状态：启动中…", "Core 服务运行状态")
			mStatus.Disable()
			systray.AddSeparator()
			mQuit := systray.AddMenuItem("退出 EasyShare", "停止所有服务并退出")

			app.trayReady()

			for {
				select {
				case <-mOpen.ClickedCh:
					app.showWindow()
				case <-mQuit.ClickedCh:
					app.quitFromTray()
					return
				case status := <-app.trayStatusCh:
					mStatus.SetTitle("服务状态：" + status)
				}
			}
		}, func() {
			// systray 退出时会自行移除通知区域图标。
		})
	})
}
