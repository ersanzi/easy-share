//go:build darwin

package main

/*
#cgo LDFLAGS: -framework Cocoa -framework Carbon -framework WebKit
#include <stdlib.h>
#include "panel_darwin.h"
*/
import "C"

import "unsafe"

// macOS 快捷面板的 Go 侧桥：窗口与热键动作在 panel_darwin.m（主队列执行），
// 这里做启动接线与「面板 → Go」的导出回调。协议解析复用 panel_surface.go，
// 与 Windows 实现共享同一套消息语义（clipboard.write=选中、dismiss=关窗）。

// panelDarwinApp 面板依赖的宿主上下文（进程内只有一个面板）。
var panelDarwinApp *App

// startPanel 拉起快捷面板：NSPanel + WKWebView + ⌘⇧V 全局热键。
func startPanel(a *App) {
	if a.panelURL == "" {
		a.logger.Printf("panel: 静态服务未就绪，跳过面板启动")
		return
	}
	panelDarwinApp = a

	// 事件通道：剪切板变化 / panel:shown 经 evaluateJavaScript 推给面板页。
	a.panelEmitMu.Lock()
	a.panelEmit = emitPanelEvent
	a.panelEmitMu.Unlock()

	cURL := C.CString(a.panelURL)
	C.easyshare_panel_start(cURL)
	C.free(unsafe.Pointer(cURL))
}

// stopPanel 停掉面板（插件卸载/禁用时调用；ObjC 侧幂等，未启动时 no-op）。
func stopPanel(a *App) {
	C.easyshare_panel_stop()
	a.panelEmitMu.Lock()
	a.panelEmit = nil
	a.panelEmitMu.Unlock()
}

// emitPanelEvent 向面板页推事件（可从任意线程调用，ObjC 侧转主队列）。
func emitPanelEvent(event string, payload any) {
	s := panelEventScript(event, payload)
	if s == "" {
		return
	}
	c := C.CString(s)
	C.easyshare_panel_eval(c)
	C.free(unsafe.Pointer(c))
}

// easysharePanelMessage 面板页消息入口：ObjC 把 webkit.messageHandlers.espanel
// 的消息序列化为 JSON 送进来，Go 处理后回传要在页面执行的 JS（调用方 free）。
//
//export easysharePanelMessage
func easysharePanelMessage(raw *C.char) *C.char {
	a := panelDarwinApp
	if a == nil {
		return nil
	}
	replyJS, dismiss, paste := panelProcessMessage(a, C.GoString(raw))
	if paste {
		C.easyshare_panel_paste_after_hide()
	} else if dismiss {
		C.easyshare_panel_schedule_hide()
	}
	if replyJS == "" {
		return nil
	}
	return C.CString(replyJS)
}

// easysharePanelShownScript 供给「面板已弹出」事件的分发脚本。
//
//export easysharePanelShownScript
func easysharePanelShownScript() *C.char {
	s := panelEventScript("panel:shown", nil)
	if s == "" {
		return nil
	}
	return C.CString(s)
}

// startSearchPanel 全局搜索面板 darwin 侧暂未实现（NSPanel 面板待真机批次）。
func startSearchPanel(a *App) {
	a.logger.Printf("search panel: darwin 待真机批次")
}

// stopSearchPanel darwin 侧暂无搜索面板可停。
func stopSearchPanel(a *App) {}
