//go:build windows

package cfmount

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// 完整填充的 Cloud 目录只枚举元数据；普通目录仍保护整棵本地子树。
// Cloud tag 只证明提供方类型，不能证明目录已填充，故还要拒绝 recall 属性。
func writebackScanEntry(p string, info os.FileInfo) (readable, cloudDirectory bool, err error) {
	u, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return false, false, err
	}
	data := windows.Win32finddata{}
	h, err := windows.FindFirstFile(u, &data)
	if err != nil {
		return false, false, err
	}
	windows.FindClose(h)
	return writebackScanAttributes(data.FileAttributes, data.Reserved0, info),
		info.IsDir() && data.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 &&
			data.Reserved0&0xffff0fff == 0x9000001a, nil
}

func writebackScanAttributes(a, tag uint32, info os.FileInfo) bool {
	const unavailable = 0x1000 | 0x40000 | 0x400000 // OFFLINE / RECALL_ON_OPEN / RECALL_ON_DATA_ACCESS
	if a&unavailable != 0 || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	// 只允许 Cloud 及 Cloud_1..Cloud_F，不允许 junction/symlink/未知 tag。
	if a&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 && tag&0xffff0fff != 0x9000001a {
		return false
	}
	// Go 对 Cloud 文件可报告 ModeIrregular（WalkDir.Info/Lstat 路径），
	// 已验证 Cloud tag 且无 recall 属性时不能再用 IsRegular 拒绝它。
	return info.IsDir() || info.Mode().IsRegular() ||
		a&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 && tag&0xffff0fff == 0x9000001a
}

func writebackReadable(p string, info os.FileInfo) (bool, error) {
	safe, _, err := writebackScanEntry(p, info)
	return safe, err
}

// 禁止并发写入/替换，且 OPEN_NO_RECALL 防止读取时隐式下载。
func openWritebackLocal(p string) (*os.File, error) {
	u, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(u, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_NO_RECALL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(h), p)
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	safe, err := writebackReadable(p, info)
	if err != nil || !safe {
		f.Close()
		if err == nil {
			err = os.ErrPermission
		}
		return nil, err
	}
	return f, nil
}
func replaceWritebackState(src, dst string) error {
	a, err := windows.UTF16PtrFromString(src)
	if err != nil {
		return err
	}
	b, err := windows.UTF16PtrFromString(dst)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(a, b, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

// 元数据句柄捕获身份，不读取或水合正文。
func writebackFileIdentity(p string) (LocalFileIdentity, error) {
	u, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return LocalFileIdentity{}, err
	}
	h, err := windows.CreateFile(u, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_NO_RECALL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return LocalFileIdentity{}, err
	}
	defer windows.CloseHandle(h)
	var id LocalFileIdentity
	err = windows.GetFileInformationByHandleEx(h, windows.FileIdInfo, (*byte)(unsafe.Pointer(&id)), uint32(unsafe.Sizeof(id)))
	return id, err
}

func writebackHandleIdentity(f *os.File) (LocalFileIdentity, error) {
	var id LocalFileIdentity
	err := windows.GetFileInformationByHandleEx(windows.Handle(f.Fd()), windows.FileIdInfo, (*byte)(unsafe.Pointer(&id)), uint32(unsafe.Sizeof(id)))
	return id, err
}
