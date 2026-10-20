//go:build windows

package fsutil

import (
	"os/exec"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// OpenFile 用系统关联程序打开文件（资源管理器双击语义）。
func OpenFile(path string) error {
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	target, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, target, nil, nil, windows.SW_SHOWNORMAL)
}

// ShowInFolder 在资源管理器中打开所在目录并选中该文件。
func ShowInFolder(path string) error {
	return exec.Command("explorer", "/select,", filepath.ToSlash(path)).Start()
}

// OpenFolder 在资源管理器中打开指定目录。
func OpenFolder(dir string) error {
	return exec.Command("explorer", filepath.ToSlash(dir)).Start()
}

// OpenShellLocation 打开一个 shell 位置，如 "shell:::{GUID}"。
//
// 与 OpenFolder 分开：shell 位置不是文件系统路径，不能过 filepath 处理
// （现在 ToSlash 恰好是空操作，但依赖这一点很脆弱）。
// 用它打开命名空间条目，得到的就是「此电脑」里那一项本身——带正确的名称与图标。
func OpenShellLocation(location string) error {
	return exec.Command("explorer", location).Start()
}
