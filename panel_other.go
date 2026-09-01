//go:build !windows && !darwin

package main

// startPanel / stopPanel 其余平台暂无快捷面板实现（当前目标平台仅 Windows 与
// macOS）。注意：静态服务（startPanelServer）仍会启动，但不注册热键、不建窗口。
func startPanel(a *App) {}

func stopPanel(a *App) {}
