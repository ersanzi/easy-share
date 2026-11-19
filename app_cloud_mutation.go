package main

import "github.com/wailsapp/wails/v2/pkg/runtime"

// cloudMutationChanged只发布真实操作后的刷新信号，容量/文件列表仍从服务器读取。
func (a *App) cloudMutationChanged() {
	a.publishTrayUser()
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "cloud:changed")
	}
}
