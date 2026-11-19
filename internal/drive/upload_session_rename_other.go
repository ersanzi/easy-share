//go:build !windows

package drive

import "os"

// 非 Windows 保持既有单次原子 rename，不扩大其他平台的错误重试范围。
func renameUploadSession(src, dst string) error { return os.Rename(src, dst) }
