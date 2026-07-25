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
