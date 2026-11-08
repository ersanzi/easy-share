//go:build !windows

package cfmount

import "os"

// 非 Windows 用于纯 Go 引擎单测；正式 CFAPI 挂载只在 Windows 启用。
func writebackReadable(_ string, info os.FileInfo) (bool, error) {
	return info.Mode().IsRegular() || info.IsDir(), nil
}
func writebackScanEntry(p string, info os.FileInfo) (bool, bool, error) {
	safe, err := writebackReadable(p, info)
	return safe, false, err
}
func openWritebackLocal(p string) (*os.File, error) { return os.Open(p) }
func replaceWritebackState(src, dst string) error   { return os.Rename(src, dst) }

// 非 Windows 仅运行纯引擎测试，没有 CFAPI 回收适配器。
func writebackFileIdentity(string) (LocalFileIdentity, error)     { return LocalFileIdentity{}, nil }
func writebackHandleIdentity(*os.File) (LocalFileIdentity, error) { return LocalFileIdentity{}, nil }
