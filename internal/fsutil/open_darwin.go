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
