//go:build windows

package shellstatus

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// 常量与 ABI 来源为 propsys.h、shobjidl_core.h 及 Microsoft CloudMirror Utilities.cpp。
// System.SyncTransferStatus: FCEFF153-E839-4CF3-A9E7-EA22832094B8, pid103。
type propertyKey struct {
	Format windows.GUID
	ID     uint32
}

var (
	iidShellItem2       = windows.GUID{Data1: 0x7e9fb0d3, Data2: 0x919f, Data3: 0x4307, Data4: [8]byte{0xab, 0x2e, 0x9b, 0x18, 0x60, 0x31, 0x0c, 0x93}}
	iidPropertyStore    = windows.GUID{Data1: 0x886d8eeb, Data2: 0x8cf2, Data3: 0x4446, Data4: [8]byte{0x8d, 0x02, 0xcd, 0xba, 0x1d, 0xbd, 0xcf, 0x99}}
	keyTransferStatus   = propertyKey{Format: windows.GUID{Data1: 0xfceff153, Data2: 0xe839, Data3: 0x4cf3, Data4: [8]byte{0xa9, 0xe7, 0xea, 0x22, 0x83, 0x20, 0x94, 0xb8}}, ID: 103}
	keyTransferProgress = propertyKey{Format: windows.GUID{Data1: 0xe77e90df, Data2: 0x6271, Data3: 0x4f5b, Data4: [8]byte{0x83, 0x4f, 0x2d, 0xd1, 0xf2, 0x45, 0xdd, 0xa4}}, ID: 4}
	shell32             = windows.NewLazySystemDLL("shell32.dll")
	createShellItem     = shell32.NewProc("SHCreateItemFromParsingName")
	changeNotify        = shell32.NewProc("SHChangeNotify")
	clearVariant        = windows.NewLazySystemDLL("ole32.dll").NewProc("PropVariantClear")
)

type comObject struct {
	vtable *[32]uintptr
}

// PROPVARIANT 64位的union占16字节。向量为 count@8、data@16。
type propertyVariant struct {
	Type     uint16
	Reserved [3]uint16
	Count    uint32
	Padding  uint32
	Data     uintptr
}

func hresult(hr uintptr, operation string) error {
	if int32(uint32(hr)) < 0 {
		return fmt.Errorf("%s HRESULT=0x%08X", operation, uint32(hr))
	}
	return nil
}

func comCall(object *comObject, slot int, args ...uintptr) uintptr {
	values := append([]uintptr{uintptr(unsafe.Pointer(object))}, args...)
	hr, _, _ := syscall.SyscallN(object.vtable[slot], values...)
	return hr
}

func release(object *comObject) {
	if object != nil {
		comCall(object, 2)
	}
}

func openVolatileStore(path string, flags uint32) (*comObject, error) {
	text, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	var item *comObject
	hr, _, _ := createShellItem.Call(uintptr(unsafe.Pointer(text)), 0, uintptr(unsafe.Pointer(&iidShellItem2)), uintptr(unsafe.Pointer(&item)))
	if err := hresult(hr, "创建 Shell 文件对象"); err != nil {
		return nil, err
	}
	defer release(item)
	var store *comObject
	// IShellItem slots0–7，IShellItem2::GetPropertyStore位于slot8。
	hr = comCall(item, 8, uintptr(flags), uintptr(unsafe.Pointer(&iidPropertyStore)), uintptr(unsafe.Pointer(&store)))
	if err := hresult(hr, "获取 volatile 属性存储"); err != nil {
		return nil, err
	}
	return store, nil
}

// Apply 必须在已初始化COM的同一线程调用，不访问属性handler或召回正文。
func Apply(update Update) error {
	if unsafe.Sizeof(propertyVariant{}) != 24 {
		return fmt.Errorf("当前架构的 PROPVARIANT 布局尚未验证")
	}
	store, err := openVolatileStore(update.Path, 0x2|0x1000)
	if err != nil {
		return err
	}
	defer release(store)
	// 本机Cloud文件volatile存储实测接受CloudMirror的UI8[completed,total]；UI4 schema写法会返回E_INVALIDARG。
	values := [2]uint64{uint64(max(update.Completed, 0)), uint64(max(update.Total, 0))}
	progress := propertyVariant{Type: 0x1000 | 21, Count: 2, Data: uintptr(unsafe.Pointer(&values[0]))}
	if update.Phase == PhaseSynced || update.Phase == PhaseFailed || update.Phase == PhaseConflict || update.Phase == PhaseUnknown {
		values = [2]uint64{}
	}
	hr := comCall(store, 6, uintptr(unsafe.Pointer(&keyTransferProgress)), uintptr(unsafe.Pointer(&progress)))
	runtime.KeepAlive(values)
	if err := hresult(hr, "写入传输字节进度"); err != nil {
		return err
	}
	// 进度属性会派生系统传输位，最后写活动状态才能正确结束传输。
	status := propertyVariant{Type: 19, Count: TransferFlags(update.Phase)}
	hr = comCall(store, 6, uintptr(unsafe.Pointer(&keyTransferStatus)), uintptr(unsafe.Pointer(&status)))
	if err := hresult(hr, "写入同步活动状态"); err != nil {
		return err
	}
	if err := hresult(comCall(store, 7), "提交同步属性"); err != nil {
		return err
	}
	text, err := windows.UTF16PtrFromString(update.Path)
	if err != nil {
		return err
	}
	changeNotify.Call(0x2000, 0x5, uintptr(unsafe.Pointer(text)), 0) // SHCNE_UPDATEITEM / SHCNF_PATHW
	return nil
}

// ReadTransferFlags 读取同一volatile属性供隔离回归与状态诊断，不读文件正文。
func ReadTransferFlags(path string) (uint32, error) {
	store, err := openVolatileStore(path, 0x1000)
	if err != nil {
		return 0, err
	}
	defer release(store)
	var value propertyVariant
	hr := comCall(store, 5, uintptr(unsafe.Pointer(&keyTransferStatus)), uintptr(unsafe.Pointer(&value)))
	if err := hresult(hr, "读取同步活动状态"); err != nil {
		return 0, err
	}
	defer clearVariant.Call(uintptr(unsafe.Pointer(&value)))
	if value.Type != 19 {
		return 0, fmt.Errorf("同步状态类型不是VT_UI4: %d", value.Type)
	}
	return value.Count, nil
}
