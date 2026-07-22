//go:build windows

package desktop

import (
	"os/exec"
	"syscall"
)

// coreBinaryName 返回 Windows 上 Core 可执行文件名。
func coreBinaryName() string { return "easyshare-core.exe" }

// configureProcess 在 Windows 上隐藏 Core 的控制台窗口。
func configureProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
