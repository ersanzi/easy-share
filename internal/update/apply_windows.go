package update

import (
	"os/exec"
	"path/filepath"
	"syscall"
)

// LaunchInstaller 以静默升级模式启动 NSIS 安装包（/S 静默 + /update 安装完自动重启应用）。
// 只启动不等待：安装包自身会先 taskkill 残留进程再覆盖安装；调用方随后应退出自己，
// 让安装包完成替换与重启。
func LaunchInstaller(installerPath string) error {
	cmd := exec.Command(installerPath, "/S", "/update")
	cmd.Dir = filepath.Dir(installerPath)
	// 独立进程组 + 隐藏窗口：安装包与桌面端生命周期解耦，且不闪控制台
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
	return cmd.Start()
}
