//go:build windows

package main

import (
	"fmt"
	"os"
	"unsafe"

	"easyshare/internal/shellundo"
	"golang.org/x/sys/windows"
)

// 此入口必须先于 NewApp；菜单小进程只转发，不启动第二套 Wails 或 Core。
func platformShellCommandHandled() bool {
	handled, err := handleShellCommand(os.Args[1:], func(paths []string) error {
		return shellundo.Forward(paths, shellundo.DefaultForwardTimeout)
	})
	if handled && err != nil {
		fmt.Fprintln(os.Stderr, "EasyShare 可撤销删除:", err)
		showShellCommandError(err)
	}
	return handled
}

func handleShellCommand(args []string, forward func([]string) error) (bool, error) {
	// 正常客户端参数保持原行为；只要出现专用 flag，就绝不能回落启动 App。
	found := false
	for _, arg := range args {
		if arg == shellundo.CommandFlag {
			found = true
			break
		}
	}
	if !found {
		return false, nil
	}
	if len(args) != 2 || args[0] != shellundo.CommandFlag || args[1] == "" {
		return true, fmt.Errorf("可撤销删除需要且仅接受一个本机选择路径")
	}
	if forward == nil {
		return true, fmt.Errorf("Shell 转发入口不可用")
	}
	return true, forward([]string{args[1]})
}

func showShellCommandError(err error) {
	// 只显示结果，不模拟点击任何 Explorer/原生删除确认对话框。
	message, _ := windows.UTF16PtrFromString("未能完成删除（可撤销）。\n" + err.Error())
	title, _ := windows.UTF16PtrFromString("EasyShare")
	windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(message)), uintptr(unsafe.Pointer(title)), 0x10 /* MB_ICONERROR */)
}
