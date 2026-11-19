//go:build windows

package shellundo

import (
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

type fileAttributeTag struct {
	Attributes uint32
	ReparseTag uint32
}

// CFAPI 占位符使用 CLOUD/CLOUD_1..F；只识别这些不重定向路径的 tag。
// symlink、junction、挂载点、AppExecLink 及其它未知 reparse 均拒绝。
func allowedCloudTag(tag uint32) bool { return tag&^uint32(0x0000f000) == 0x9000001a }

func inspectLocalPath(path string, requireDirectory bool) error {
	rootText, err := windows.UTF16PtrFromString(path[:3])
	if err != nil {
		return err
	}
	if windows.GetDriveType(rootText) != windows.DRIVE_FIXED {
		return fmt.Errorf("仅支持本机固定 NTFS 卷")
	}
	var filesystem [32]uint16
	if err := windows.GetVolumeInformation(rootText, nil, 0, nil, nil, nil, &filesystem[0], uint32(len(filesystem))); err != nil {
		return fmt.Errorf("读取卷类型: %w", err)
	}
	if !strings.EqualFold(windows.UTF16ToString(filesystem[:]), "NTFS") {
		return fmt.Errorf("可撤销删除只支持 NTFS 卷")
	}
	parts := strings.Split(path[3:], `\`)
	prefix := path[:3]
	if err := inspectComponent(prefix, true); err != nil {
		return err
	}
	for i, part := range parts {
		if part == "" {
			continue
		}
		if len(prefix) > 3 {
			prefix += `\`
		}
		prefix += part
		if err := inspectComponent(prefix, i < len(parts)-1 || requireDirectory); err != nil {
			return err
		}
	}
	return nil
}

func inspectComponent(path string, requireDirectory bool) error {
	text, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	// 仅打开元数据，不读取正文，不追随 reparse，不使用 EvalSymlinks。
	handle, err := windows.CreateFile(text, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return fmt.Errorf("打开路径元数据 %q: %w", path, err)
	}
	defer windows.CloseHandle(handle)
	var info fileAttributeTag
	if err := windows.GetFileInformationByHandleEx(handle, windows.FileAttributeTagInfo, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		return fmt.Errorf("读取 reparse 属性 %q: %w", path, err)
	}
	if info.Attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 && !allowedCloudTag(info.ReparseTag) {
		return fmt.Errorf("拒绝未知或重定向 reparse 路径 %q (tag=0x%08X)", path, info.ReparseTag)
	}
	if requireDirectory && info.Attributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return fmt.Errorf("路径组件不是目录: %q", path)
	}
	buffer := make([]uint16, 32768)
	n, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
	if err != nil {
		return fmt.Errorf("确认本机真实路径 %q: %w", path, err)
	}
	if n == 0 || n >= uint32(len(buffer)) {
		return fmt.Errorf("真实路径长度无效: %q", path)
	}
	final := strings.TrimPrefix(windows.UTF16ToString(buffer[:n]), `\\?\`)
	if !strings.EqualFold(strings.TrimSuffix(final, `\`), strings.TrimSuffix(path, `\`)) {
		return fmt.Errorf("路径存在别名或重定向，拒绝执行: %q -> %q", path, final)
	}
	return nil
}

// ValidatePaths 同时检查当前 root 边界和每级元数据；不把选择路径清理到另一目录。
// 主线仍须在 hydration 后、NativeRecycle 前再次深层检查目录树和 provider 身份，避免 TOCTOU。
func ValidatePaths(root string, paths []string) ([]string, error) {
	normalRoot, err := normalizeLocalPath(root)
	if err != nil {
		return nil, fmt.Errorf("云根校验: %w", err)
	}
	if len(normalRoot) == 3 {
		return nil, fmt.Errorf("云根不得为盘符根")
	}
	normal, err := selectionInRoot(normalRoot, paths)
	if err != nil {
		return nil, err
	}
	if err := inspectLocalPath(normalRoot, true); err != nil {
		return nil, fmt.Errorf("当前云根元数据校验: %w", err)
	}
	for _, path := range normal {
		if err := inspectLocalPath(path, false); err != nil {
			return nil, err
		}
	}
	return normal, nil
}
