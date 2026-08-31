//go:build windows

package update

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// IsInstalledMode 判断当前进程是否运行在 NSIS 安装目录下——只有安装版能
// 「重启并更新」（静默安装会写回注册表记录的安装目录），绿色便携版只引导手动升级。
// 判据：HKCU\Software\EasyShare\EasyShare\InstallDir 与当前 exe 所在目录一致。
func IsInstalledMode() bool {
	dir := installedDir()
	if dir == "" {
		return false
	}
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	// Windows 路径大小写不敏感；双方都 Clean 消除尾部斜杠差异
	return strings.EqualFold(filepath.Dir(filepath.Clean(exe)), filepath.Clean(dir))
}

func installedDir() string {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\EasyShare\EasyShare`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer key.Close()
	dir, _, err := key.GetStringValue("InstallDir")
	if err != nil {
		return ""
	}
	return dir
}
