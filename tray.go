package main

import (
	"github.com/getlantern/systray"
)

// trayIcon 由平台文件提供：Windows 嵌入 ICO（tray_windows.go），
// macOS 嵌入 PNG（tray_darwin.go）。
//
// startTray launches the system tray icon and menu in a background goroutine.
// It communicates with the App through channels to show/hide the window or quit.
func startTray(app *App) {
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

		// Notify the app that the tray is ready
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
		// onExit: tray icon removed
	})
}
