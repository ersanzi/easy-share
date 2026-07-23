//go:build darwin

package main

/*
#cgo LDFLAGS: -framework Cocoa

#include <stdlib.h>
#include "tray_native_darwin.h"
*/
import "C"

import (
	_ "embed"
	"sync"
	"unsafe"
)

// macOS 菜单栏图标使用 PNG。原生 bridge 只创建 NSStatusItem，不能设置或
// 替换 NSApplicationDelegate，否则会破坏 Wails 自己的窗口生命周期。
//
//go:embed build/darwin/trayicon.png
var trayIcon []byte

var (
	darwinTrayApp     *App
	darwinTrayAppLock sync.RWMutex
)

// startTray 把菜单栏挂到 Wails 已有的 AppKit 事件循环。macOS 不能使用
// getlantern/systray：该库与 Wails 都定义 AppDelegate，会在链接阶段产生重复符号。
func startTray(app *App) {
	app.trayOnce.Do(func() {
		darwinTrayAppLock.Lock()
		darwinTrayApp = app
		darwinTrayAppLock.Unlock()

		go syncDarwinTrayStatus(app)

		var icon unsafe.Pointer
		if len(trayIcon) > 0 {
			icon = unsafe.Pointer(&trayIcon[0])
		}
		// 原生层会在返回前复制图标字节，再把 UI 创建调度到 AppKit 主队列。
		C.easyshare_tray_start(icon, C.size_t(len(trayIcon)))
	})
}

func syncDarwinTrayStatus(app *App) {
	for status := range app.trayStatusCh {
		text := C.CString("服务状态：" + status)
		C.easyshare_tray_set_status(text)
		C.free(unsafe.Pointer(text))
	}
}

func currentDarwinTrayApp() *App {
	darwinTrayAppLock.RLock()
	defer darwinTrayAppLock.RUnlock()
	return darwinTrayApp
}

//export easyshareTrayReady
func easyshareTrayReady() {
	if app := currentDarwinTrayApp(); app != nil {
		// trayReady 会探测 Core，不能阻塞 AppKit 主线程。
		go app.trayReady()
	}
}

//export easyshareTrayOpen
func easyshareTrayOpen() {
	if app := currentDarwinTrayApp(); app != nil {
		go app.showWindow()
	}
}

//export easyshareTrayQuit
func easyshareTrayQuit() {
	if app := currentDarwinTrayApp(); app != nil {
		// 优雅停止 Core 涉及 HTTP 调用，放到 Go 协程避免卡住菜单栏。
		go app.quitFromTray()
	}
}
