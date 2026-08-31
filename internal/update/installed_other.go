//go:build !windows

package update

// IsInstalledMode 非 Windows 平台暂无安装模式概念（macOS 为手动替换），恒为 false。
func IsInstalledMode() bool {
	return false
}
