//go:build windows

package explorerview

import (
	"fmt"
	"syscall"
	"unsafe"

	"github.com/go-ole/go-ole"
	"golang.org/x/sys/windows"
)

type shellObject struct{ table *[40]uintptr }
type propertyKey struct {
	ID  windows.GUID
	PID uint32
}
type columnInfo struct {
	Size, Mask, State, Width, DefaultWidth, IdealWidth uint32
	Name                                               [80]uint16
}

var (
	iidServiceProvider = ole.NewGUID("{6D5140C1-7436-11CE-8034-00AA006009FA}")
	sidTopLevelBrowser = ole.NewGUID("{4C96BE40-915C-11CF-99D3-00AA004AE837}")
	iidShellBrowser    = ole.NewGUID("{000214E2-0000-0000-C000-000000000046}")
	iidFolderView      = ole.NewGUID("{CDE725B0-CCC9-4519-917E-325D72FAB4CE}")
	iidColumnManager   = ole.NewGUID("{D8EC27BB-3F3B-4042-B10A-4ACFD924D453}")
	psGetName          = windows.NewLazySystemDLL("propsys.dll").NewProc("PSGetNameFromPropertyKey")
	psGetKey           = windows.NewLazySystemDLL("propsys.dll").NewProc("PSGetPropertyKeyFromName")
)

func call(object *shellObject, slot int, args ...uintptr) uintptr {
	hr, _, _ := syscall.SyscallN(object.table[slot], append([]uintptr{uintptr(unsafe.Pointer(object))}, args...)...)
	return hr
}
func result(hr uintptr, op string) error {
	if int32(uint32(hr)) < 0 {
		return fmt.Errorf("%s HRESULT=0x%08X", op, uint32(hr))
	}
	return nil
}
func release(object *shellObject) {
	if object != nil {
		call(object, 2)
	}
}
func query(object *shellObject, iid *ole.GUID) (*shellObject, error) {
	if object == nil {
		return nil, fmt.Errorf("Explorer 导航尚未发布当前视图")
	}
	var next *shellObject
	hr := call(object, 0, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&next)))
	if err := result(hr, "获取Shell视图接口"); err != nil {
		return nil, err
	}
	if next == nil {
		return nil, fmt.Errorf("Explorer 尚未提供请求的视图接口")
	}
	return next, nil
}

// applyDetails 使用公开Shell接口，只对调用方已经验证scope的窗口调用。
func applyDetails(window *ole.IDispatch) error {
	folder, columns, close, err := layoutInterfaces(window)
	if err != nil {
		return err
	}
	defer close()
	if err := result(call(folder, 4, 4), "设置本云盘详细视图"); err != nil {
		return err
	} // FVM_DETAILS=4
	var count uint32
	if err := result(call(columns, 5, 1, uintptr(unsafe.Pointer(&count))), "查询可用列"); err != nil {
		return err
	}
	if count == 0 || count > 512 {
		return fmt.Errorf("可用列数量不可信: %d", count)
	}
	keys := make([]propertyKey, count)
	if err := result(call(columns, 6, 1, uintptr(unsafe.Pointer(&keys[0])), uintptr(count)), "枚举可用列"); err != nil {
		return err
	}
	byName := map[string]propertyKey{}
	for _, key := range keys {
		var text *uint16
		hr, _, _ := psGetName.Call(uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(&text)))
		if int32(uint32(hr)) >= 0 && text != nil {
			name := windows.UTF16PtrToString(text)
			windows.CoTaskMemFree(unsafe.Pointer(text))
			byName[name] = key
		}
	}
	// StorageProviderUIStatus 是Windows原生合成图标列，可能不在普通可枚举列清单中。
	// 只有GetColumnInfo证实本view支持时使用，不能用可用性文字列冒充图标。
	text, _ := windows.UTF16PtrFromString("System.StorageProviderUIStatus")
	var uiStatus propertyKey
	hr, _, _ := psGetKey.Call(uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(&uiStatus)))
	if int32(uint32(hr)) >= 0 {
		info := columnInfo{Size: uint32(unsafe.Sizeof(columnInfo{})), Mask: 0x1 | 0x10}
		if result(call(columns, 4, uintptr(unsafe.Pointer(&uiStatus)), uintptr(unsafe.Pointer(&info))), "查询原生状态图标列") == nil {
			byName["System.StorageProviderUIStatus"] = uiStatus
		}
	}
	statusName := ""
	for _, name := range []string{"System.StorageProviderUIStatus", "System.StorageProviderStatus", "System.StorageProviderState", "System.FilePlaceholderStatus", "System.SyncTransferStatus"} {
		if _, ok := byName[name]; ok {
			statusName = name
			break
		}
	}
	if statusName == "" {
		return fmt.Errorf("当前Shell未提供云文件状态列")
	}
	ordered := []propertyKey{}
	for _, name := range []string{"System.ItemNameDisplay", statusName, "System.DateModified", "System.ItemTypeText", "System.Size"} {
		if key, ok := byName[name]; ok {
			ordered = append(ordered, key)
		}
	}
	if len(ordered) < 2 {
		return fmt.Errorf("文件夹列不完整")
	}
	states := make([]columnInfo, len(ordered))
	for i, key := range ordered {
		states[i] = columnInfo{Size: uint32(unsafe.Sizeof(columnInfo{})), Mask: 0x1 | 0x10}
		if err := result(call(columns, 4, uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(&states[i]))), "保留原列状态"); err != nil {
			return err
		}
	}
	if err := result(call(columns, 7, uintptr(unsafe.Pointer(&ordered[0])), uintptr(len(ordered))), "设置本云盘列顺序"); err != nil {
		return err
	}
	// SetColumns清除列状态，必须恢复SetColumnInfo，避免变成零宽列。
	for i, key := range ordered {
		if states[i].Width == 0 {
			states[i].Width = 120
		}
		states[i].State |= 1 // CM_STATE_VISIBLE
		if err := result(call(columns, 3, uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(&states[i]))), "恢复列状态/宽度"); err != nil {
			return err
		}
	}
	return nil
}
