package main

// 快捷面板（宿主新表面）：全局热键唤起的独立小窗，加载剪切板插件的 ?panel=1
// 紧凑形态。本文件是平台无关的共享部分——静态资源监听、消息协议解析、事件脚本；
// 窗口与热键的平台实现见 panel_windows.go（Win32+WebView2）与
// panel_darwin.go（NSPanel+WKWebView），其余平台 no-op（panel_other.go）。
//
// 面板运行时约定（与 SDK eshare.js 的 eshare.window 注释一致）：
//   - 页面发 {__eshare:1,id,api,args} 走能力 RPC（同一套权限鉴权，插件身份固定为
//     panelPluginID）；回包经 window.__eshareNative.deliver 分发；
//   - 页面发 {__esharePanel:1,cmd:"dismiss"} 请求关窗（Esc）；
//   - 面板内插件成功执行 clipboard.write = 用户选中条目：宿主收起面板并自动粘贴
//     回之前的前台窗口（Win+V 语义），无需新增能力 API；
//   - 宿主在每次弹出面板时向页面推 {__eshare:1,event:"panel:shown"}，插件借此
//     重置搜索/选中并聚焦输入框。

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
)

// panelPluginID 面板固定服务的插件（首发剪切板插件；它是普通插件，可卸载——
// 卸载后面板随之销毁、热键释放，重装后自动重建。多插件面板调度是后续扩展）。
const panelPluginID = "clipboard"

// 面板窗口逻辑尺寸（96 DPI 下的像素），平台创建窗口时按显示器 DPI 缩放。
const (
	panelWidth  = 384
	panelHeight = 520
)

// startPanelServer 启动面板静态资源监听：仅回环、随机端口、随进程存活。
// 之所以不复用 Wails AssetServer——它的虚拟主机只在主窗口 WebView2 内可达，
// 独立面板窗口够不到；而插件页引用 /plugins/_sdk/eshare.js、/clipboard-files/*.png
// 等绝对路径，复用同一 mux 换个 listener 全部兼容，插件零改动。
func startPanelServer(a *App) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		a.logger.Printf("panel server: %v", err)
		return
	}
	a.panelListener = ln
	a.panelURL = fmt.Sprintf("http://127.0.0.1:%d/plugins/%s/index.html?panel=1",
		ln.Addr().(*net.TCPAddr).Port, panelPluginID)
	go func() {
		if err := http.Serve(ln, a.AssetHandler()); err != nil && err != http.ErrServerClosed {
			a.logger.Printf("panel server: %v", err)
		}
	}()
	a.logger.Printf("panel server ready: %s", a.panelURL)
}

// syncClipboardSurface 让「剪切板录制 + 快捷面板」与剪切板插件的在场状态对齐：
// 插件已装且启用 → 开始记录、按需起 loopback 静态服务并拉起面板；
// 被卸载/禁用 → 停止录制并销毁面板（热键随之释放）。安装/卸载/启停后都会调用，
// 因此卸载剪切板插件后无需重启应用（这是普通插件，不是内置）。
func (a *App) syncClipboardSurface() {
	active := false
	if a.pluginManager != nil {
		if info, ok := a.pluginManager.Get(panelPluginID); ok && !info.Disabled {
			active = true
		}
	}
	if !active {
		if a.clipboardService != nil {
			a.clipboardService.Stop()
		}
		stopPanel(a)
		return
	}
	if a.clipboardService != nil {
		if err := a.clipboardService.Start(); err != nil {
			a.logger.Printf("clipboard listener: %v", err)
		}
	}
	if a.panelURL == "" {
		startPanelServer(a)
	}
	startPanel(a)
}

// panelEmitEvent 向面板页推事件（窗口未就绪或已销毁时静默丢弃）。
func (a *App) panelEmitEvent(event string, payload any) {
	a.panelEmitMu.RLock()
	emit := a.panelEmit
	a.panelEmitMu.RUnlock()
	if emit != nil {
		emit(event, payload)
	}
}

// panelEnvelope 是面板页发来消息的信封。ID 用指针区分「缺省」与 0。
type panelEnvelope struct {
	Eshare int             `json:"__eshare"`
	ID     *int            `json:"id"`
	API    string          `json:"api"`
	Args   json.RawMessage `json:"args"`
	Panel  int             `json:"__esharePanel"`
	Cmd    string          `json:"cmd"`
}

// panelReply 是能力 RPC 的回包（与 SDK handleInbound 的协议对应）。
type panelReply struct {
	Eshare int             `json:"__eshare"`
	ID     int             `json:"id"`
	OK     bool            `json:"ok"`
	Data   json.RawMessage `json:"data,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// panelProcessMessage 解析面板页发来的一条消息并执行。
// 返回 replyJS 非空时平台实现须在页面里执行该脚本（RPC 回包）；
// dismiss 为插件请求关窗；paste 为「选中条目」（收起并回贴）。
// 平台实现须保证本函数只在面板自己的线程上调用（Eval/Show 等操作依赖消息循环）。
func panelProcessMessage(a *App, raw string) (replyJS string, dismiss bool, paste bool) {
	var env panelEnvelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return "", false, false
	}
	if env.Panel == 1 {
		return "", env.Cmd == "dismiss", false
	}
	if env.Eshare != 1 || env.ID == nil || env.API == "" {
		return "", false, false
	}
	res := a.PluginInvoke(panelPluginID, env.API, string(env.Args))
	data, err := json.Marshal(panelReply{
		Eshare: 1, ID: *env.ID, OK: res.OK, Data: res.Data, Error: res.Error,
	})
	if err != nil {
		return "", false, false
	}
	replyJS = "window.__eshareNative&&window.__eshareNative.deliver(" + string(data) + ")"
	// 面板语义：成功的回写即「用户选了这条」——收起面板并粘贴回之前的焦点窗。
	if env.API == "clipboard.write" && res.OK {
		paste = true
	}
	return replyJS, false, paste
}

// panelEventScript 构造向面板页分发事件的 Eval/executeJavaScript 脚本。
// 平台窗口须在收到面板就绪事件后（页面脚本已注册 __eshareNative）才调用。
func panelEventScript(event string, payload any) string {
	data, err := json.Marshal(map[string]any{"__eshare": 1, "event": event, "payload": payload})
	if err != nil {
		return ""
	}
	return "window.__eshareNative&&window.__eshareNative.deliver(" + string(data) + ")"
}
