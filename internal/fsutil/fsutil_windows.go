//go:build windows

package fsutil

import (
	"syscall"
	"unsafe"
)

var (
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	getLogicalDrives     = kernel32.NewProc("GetLogicalDrives")
	getDriveType         = kernel32.NewProc("GetDriveTypeW")
	getVolumeInformation = kernel32.NewProc("GetVolumeInformationW")
	getDiskFreeSpaceEx   = kernel32.NewProc("GetDiskFreeSpaceExW")
)

// ListDrives returns all available logical drives with capacity info.
func ListDrives() ([]DriveInfo, error) {
	ret, _, _ := getLogicalDrives.Call()
	mask := uint32(ret)

	var drives []DriveInfo
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		letter := string(rune('A'+i)) + ":"
		root := letter + "\\"

		driveType := driveTypeName(uint32(mustCall(getDriveType, root)))

		// Skip CD-ROM drives with no media (GetVolumeInformation would fail).
		label := volumeLabel(root)

		total, free := diskSpace(root)
		var usedPct float64
		if total > 0 {
			usedPct = float64(total-free) / float64(total) * 100
		}

		drives = append(drives, DriveInfo{
			Letter:     letter,
			Label:      label,
			Type:       driveType,
			TotalBytes: total,
			FreeBytes:  free,
			UsedPct:    usedPct,
		})
	}
	return drives, nil
}

func mustCall(proc *syscall.LazyProc, arg string) uintptr {
	ptr, _ := syscall.UTF16PtrFromString(arg)
	ret, _, _ := proc.Call(uintptr(unsafe.Pointer(ptr)))
	return ret
}

func driveTypeName(t uint32) string {
	switch t {
	case 2:
		return "removable"
	case 3:
		return "fixed"
	case 4:
		return "network"
	case 5:
		return "cdrom"
	case 6:
		return "ramdisk"
	default:
		return "unknown"
	}
}

func volumeLabel(root string) string {
	rootPtr, _ := syscall.UTF16PtrFromString(root)
	var labelBuf [261]uint16
	ret, _, _ := getVolumeInformation.Call(
		uintptr(unsafe.Pointer(rootPtr)),
		uintptr(unsafe.Pointer(&labelBuf[0])),
		uintptr(len(labelBuf)),
		0, 0, 0, 0, 0,
	)
	if ret == 0 {
		return ""
	}
	return syscall.UTF16ToString(labelBuf[:])
}

func diskSpace(root string) (total, free int64) {
	rootPtr, _ := syscall.UTF16PtrFromString(root)
	var freeAvailable, totalBytes, totalFree uint64
	ret, _, _ := getDiskFreeSpaceEx.Call(
		uintptr(unsafe.Pointer(rootPtr)),
		uintptr(unsafe.Pointer(&freeAvailable)),
		uintptr(unsafe.Pointer(&totalBytes)),
		uintptr(unsafe.Pointer(&totalFree)),
	)
	if ret == 0 {
		return 0, 0
	}
	return int64(totalBytes), int64(freeAvailable)
}
