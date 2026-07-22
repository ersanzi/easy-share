//go:build darwin

package desktop

import (
	"os/exec"
	"syscall"
)

// coreBinaryName 返回 macOS 上 Core 可执行文件名（无 .exe 后缀）。
func coreBinaryName() string { return "easyshare-core" }

// configureProcess 让 Core 在独立会话中运行，不随父进程终端信号退出。
func configureProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
