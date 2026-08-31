//go:build darwin

package fsutil

import "os/exec"

// OpenFile 使用 macOS 默认应用打开文件（等效于 Finder 双击）。
func OpenFile(path string) error {
	return exec.Command("open", path).Start()
}

// OpenFolder 在 Finder 中打开指定目录。
func OpenFolder(dir string) error {
	return exec.Command("open", dir).Start()
}

// OpenShellLocation 在 macOS 上等同于 OpenFolder。
//
// shell:::{GUID} 是 Windows 命名空间的寻址方式，macOS 无对应概念；
// 保留同名函数只为让调用方不必分平台写代码，实参在 macOS 侧应是挂载点路径。
func OpenShellLocation(location string) error {
	return OpenFolder(location)
}
