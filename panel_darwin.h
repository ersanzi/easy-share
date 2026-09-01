#pragma once

// macOS 快捷面板：NSPanel + WKWebView + Carbon 全局热键（⌘⇧V）。
//
// 函数分两组：
//   easyshare_panel_*   —— Go 侧调用、panel_darwin.m 实现（经 dispatch 挂主队列执行）；
//   easysharePanel*     —— panel_darwin.m 调用、panel_darwin.go //export 实现
//                          （面板 → Go 的消息桥与事件脚本供给）。

// 启动面板（异步：内部 dispatch 到主队列建窗、注册热键、加载 url）。
void easyshare_panel_start(const char* url);

// 停掉面板：注销热键、收窗并释放控制器（插件卸载/禁用时调用；重装可再 start）。
void easyshare_panel_stop(void);

// 在面板页执行一段 JS（面板未就绪时丢弃）。
void easyshare_panel_eval(const char* js);

// 收起面板（Esc 等插件侧关闭请求）。
void easyshare_panel_schedule_hide(void);

// 收起面板并回贴：与 Windows 语义一致——选中条目后切回之前的应用合成 ⌘V
// （需辅助功能授权，未授权时降级为仅复制）。
void easyshare_panel_paste_after_hide(void);

// 处理面板页发来的一条消息（JSON 信封），返回要在页面执行的 JS；
// 无需回复返回 NULL。返回串由调用方 free。
const char* easysharePanelMessage(const char* json);

// 返回「面板已弹出」事件的分发脚本（SDK 协议的 Go 侧单一事实源）；调用方 free。
const char* easysharePanelShownScript(void);
