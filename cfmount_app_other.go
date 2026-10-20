//go:build !windows

package main

import "errors"

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

// CloudKeepOnDevice 「始终保留在这台电脑上」（Windows CfApi 专属）。
//
// 前端在 `CfapiMountPath()` 为空时会隐藏这两个入口，因此正常路径不会走到这里；
// 返回错误而不是 nil，是为了让任何**意外的**调用暴露出来而不是静默成功。
func (a *App) CloudKeepOnDevice(string) error {
	return errors.New("此平台不支持本地保留")
}

// CloudFreeUpSpace 「释放空间」（Windows CfApi 专属，macOS 等价能力是 FileProvider）。
func (a *App) CloudFreeUpSpace(string) error {
	return errors.New("此平台不支持释放本地空间")
}
