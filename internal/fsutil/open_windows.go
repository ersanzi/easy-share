//go:build windows

package fsutil

import (
	"os/exec"
	"path/filepath"
)

// OpenFile 在资源管理器中选中并高亮指定文件。
func OpenFile(path string) error {
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
