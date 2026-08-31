//go:build !windows

package update

import "fmt"

// LaunchInstaller 非 Windows 平台不支持静默安装（macOS 引导用户手动替换，见迭代记录）。
func LaunchInstaller(installerPath string) error {
	return fmt.Errorf("此平台不支持自动安装，请手动运行 %s", installerPath)
}
