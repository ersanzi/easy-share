//go:build darwin

package fsutil

import (
	"os"
	"path/filepath"
	"syscall"
)

// ListDrives 返回 macOS 上已挂载的卷：根卷（/）加上 /Volumes 下的挂载点。
// macOS 无盘符概念，DriveInfo.Letter 字段复用为挂载路径，可直接传给 ListDir。
func ListDrives() ([]DriveInfo, error) {
	var drives []DriveInfo

	// 根卷（启动磁盘）
	if d, ok := volumeInfo("/", "Macintosh HD"); ok {
		drives = append(drives, d)
	}

	// /Volumes 下的其他挂载卷（外置磁盘、网络挂载、磁盘镜像等）
	entries, err := os.ReadDir("/Volumes")
	if err == nil {
		for _, entry := range entries {
			name := entry.Name()
			path := filepath.Join("/Volumes", name)
			if d, ok := volumeInfo(path, name); ok {
				drives = append(drives, d)
			}
		}
	}
	return drives, nil
}

func volumeInfo(path, label string) (DriveInfo, bool) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return DriveInfo{}, false
	}
	total := int64(stat.Blocks) * int64(stat.Bsize)
	free := int64(stat.Bfree) * int64(stat.Bsize)
	var usedPct float64
	if total > 0 {
		usedPct = float64(total-free) / float64(total) * 100
	}
	return DriveInfo{
		Letter:     path,
		Label:      label,
		Type:       "fixed",
		TotalBytes: total,
		FreeBytes:  free,
		UsedPct:    usedPct,
	}, true
}
