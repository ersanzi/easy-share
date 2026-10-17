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

// 下面两个函数由 panel_darwin.go 的 //export 生成，声明必须**与 cgo 生成的签名
// 逐字一致**：cgo 会把本头文件塞进 _cgo_export.c 的 prolog，手写成
// `const char* f(const char*)` 会与生成的 `char* f(char*)` 判为
// conflicting types，macOS 直接编不过（tray_native_darwin.h 里的 void 版本
// 不带 const，所以那边从来没冲突）。不要用 const。

// 处理面板页发来的一条消息（JSON 信封），返回要在页面执行的 JS；
// 无需回复返回 NULL。返回串由调用方 free。
char* easysharePanelMessage(char* json);

// 返回「面板已弹出」事件的分发脚本（SDK 协议的 Go 侧单一事实源）；调用方 free。
char* easysharePanelShownScript(void);
