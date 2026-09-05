//go:build windows

// 「开机自动记录」Windows 实现：HKCU Software\Microsoft\Windows\CurrentVersion\Run。
package clipboard

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

var autoStartSupported = func() bool { return true }

// openRunKey 打开（必要时创建）HKCU Run 键。
func openRunKey() (registry.Key, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err == nil {
		return k, nil
	}
	// Run 键在正常系统上必存在；保险起见失败时创建
	k, _, err = registry.CreateKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	return k, err
}

func autoStartEnabled() (bool, error) {
	k, err := openRunKey()
	if err != nil {
		return false, fmt.Errorf("打开 Run 键: %w", err)
	}
	return readAutoStart(k)
}

func setAutoStart(enable bool) error {
	if !enable {
		k, err := openRunKey()
		if err != nil {
			return fmt.Errorf("打开 Run 键: %w", err)
		}
		if err := removeAutoStart(k); err != nil {
			// 值本就不存在 = 已是关闭状态，不算故障
			if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
				return nil
			}
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("取可执行文件路径: %w", err)
	}
	k, err := openRunKey()
	if err != nil {
		return fmt.Errorf("打开 Run 键: %w", err)
	}
	return writeAutoStart(k, exe)
}
