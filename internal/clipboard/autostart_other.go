//go:build !windows

// 非 Windows 平台：自启暂无实现（macOS LaunchAgent 留待 darwin 真机批次；
// 与 listener_other.go 的平台口径对齐）。UI 按 AutoStartSupported=false 隐藏开关。
package clipboard

import "fmt"

var autoStartSupported = func() bool { return false }

func autoStartEnabled() (bool, error) { return false, ErrUnsupportedPlatform }

func setAutoStart(enable bool) error {
	return fmt.Errorf("当前平台不支持开机自启: %w", ErrUnsupportedPlatform)
}
