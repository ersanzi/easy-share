//go:build !windows

package main

// 非 Windows 平台的 CfApi 空壳：CfApi 是 Windows Cloud Files API，
// macOS 的等价能力是 FileProvider（阶段 5 另一片）。这里保持类型与函数签名一致，
// 让 app.go / spacemount.go 的调用点无需平台判断。

// cfapiMounts 是 Windows 版 cfapiMounts 的空壳（无字段无行为）。
type cfapiMounts struct{}

// mountCfapiSpaces 挂载个人空间 CfApi 占位符同步根（Windows 专属，此处为空实现）。
func (a *App) mountCfapiSpaces() {}

// unmountCfapiSpaces 卸载 CfApi 同步根（Windows 专属，此处为空实现）。
func (a *App) unmountCfapiSpaces() {}

// CfapiMountPath 返回个人空间 CfApi 同步根路径（非 Windows 恒为空串，前端隐藏入口）。
func (a *App) CfapiMountPath() string { return "" }

// OpenCfapiFolder 打开占位符目录（非 Windows 恒报未挂载）。
func (a *App) OpenCfapiFolder() error { return nil }
