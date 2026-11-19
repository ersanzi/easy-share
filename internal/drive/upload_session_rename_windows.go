//go:build windows

package drive

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// renameUploadSession 复用 Go 的 Windows 原子替换，保持同一临时正文及目标指纹。
func renameUploadSession(src, dst string) error {
	return renameUploadSessionWithRetry(func() error { return os.Rename(src, dst) })
}

// 仅处理扫描/共享句柄造成的短暂拒绝；最多六次，累计退避 310ms。
// 持续拒绝及其他错误照常返回，绝不删除目标、重写正文或回退无条件上传。
func renameUploadSessionWithRetry(rename func() error) error {
	var err error
	for attempt := 0; attempt < 6; attempt++ {
		err = rename()
		if err == nil || !(errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_SHARING_VIOLATION)) {
			return err
		}
		if attempt < 5 {
			time.Sleep(time.Duration(10<<attempt) * time.Millisecond)
		}
	}
	return err
}
