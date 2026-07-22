// Package namespace 在各平台为 EasyShare 提供系统级文件空间入口：
// Windows 注册「此电脑」命名空间条目（namespace_windows.go），
// macOS 通过 Finder 挂载 WebDAV 卷（namespace_darwin.go）。
package namespace

// Log 是可由宿主注入的日志函数，用于输出平台集成的诊断信息。
// 默认 no-op；桌面端在注册入口前会把它接到自己的日志（desktop.log），
// 便于在真机上排查挂载/注册是否成功、走了哪条策略、为何失败。
var Log = func(format string, args ...any) {}
