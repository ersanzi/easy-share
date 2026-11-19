//go:build windows

package shellroot

import (
	"fmt"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// SuppressGeneratedNavigation 只撤本提供方自动生成的第二个导航节点，保留WinRT/CFAPI登记。
// 品牌namespace由上层统一发布两处入口，避免用户看到同一空间两份网盘。
func SuppressGeneratedNavigation(id, root string) error {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Explorer\SyncRootManager\`+id, registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	guid, _, err := key.GetStringValue("NamespaceCLSID")
	key.Close()
	if err != nil {
		return err
	}
	if _, err := windows.GUIDFromString(guid); err != nil {
		return fmt.Errorf("生成的namespace标识无效")
	}
	if guid == "{8327AC46-BCC2-45D2-A46F-0737E2CC6557}" || guid == "{E5A1F2B3-C4D5-6E7F-8A9B-0C1D2E3F4A5B}" {
		return fmt.Errorf("拒绝覆盖品牌CLSID")
	}
	bag, err := registry.OpenKey(registry.CURRENT_USER, `Software\Classes\CLSID\`+guid+`\Instance\InitPropertyBag`, registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	path, _, err := bag.GetStringValue("TargetFolderPath")
	bag.Close()
	if err != nil || !strings.EqualFold(canonicalRoot(path), canonicalRoot(root)) {
		return fmt.Errorf("自动导航目标不属于当前云根: target=%q root=%q err=%v", path, root, err)
	}
	class, err := registry.OpenKey(registry.CURRENT_USER, `Software\Classes\CLSID\`+guid, registry.SET_VALUE)
	if err != nil {
		return err
	}
	err = class.SetDWordValue("System.IsPinnedToNameSpaceTree", 0)
	class.Close()
	if err != nil {
		return err
	}
	err = registry.DeleteKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Explorer\Desktop\NameSpace\`+guid)
	if err == registry.ErrNotExist {
		return nil
	}
	return err
}

// Shell WinRT将8.3路径展开，按系统GetLongPathName规范化后精确比较。
func canonicalRoot(path string) string {
	text, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return filepath.Clean(path)
	}
	buffer := make([]uint16, 32768)
	get := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetLongPathNameW")
	n, _, _ := get.Call(uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	if n > 0 && n < uintptr(len(buffer)) {
		return filepath.Clean(windows.UTF16ToString(buffer[:n]))
	}
	return filepath.Clean(path)
}
