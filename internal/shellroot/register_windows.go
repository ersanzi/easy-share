//go:build windows

// Package shellroot 通过WinRT登记CFAPI提供方的Shell展示元数据，不注销用户同步根。
package shellroot

import (
	"fmt"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type object struct{ table *[48]uintptr }

var (
	combase          = windows.NewLazySystemDLL("combase.dll")
	roInitialize     = combase.NewProc("RoInitialize")
	roUninitialize   = combase.NewProc("RoUninitialize")
	newString        = combase.NewProc("WindowsCreateString")
	deleteString     = combase.NewProc("WindowsDeleteString")
	activation       = combase.NewProc("RoActivateInstance")
	factoryProc      = combase.NewProc("RoGetActivationFactory")
	iidInfo          = windows.GUID{Data1: 0x7c1305c4, Data2: 0x99f9, Data3: 0x41ac, Data4: [8]byte{0x89, 0x04, 0xab, 0x05, 0x5d, 0x65, 0x49, 0x26}}
	iidManager       = windows.GUID{Data1: 0x3e99fbbf, Data2: 0x8fe3, Data3: 0x4b40, Data4: [8]byte{0xab, 0xc7, 0xf6, 0xfc, 0x3d, 0x74, 0xc9, 0x8e}}
	iidFolder        = windows.GUID{Data1: 0x08f327ff, Data2: 0x85d5, Data3: 0x48b9, Data4: [8]byte{0xae, 0xe9, 0x28, 0x51, 0x1e, 0x33, 0x9f, 0x9f}}
	iidAsyncInfo     = windows.GUID{Data1: 0x00000036, Data2: 0, Data3: 0, Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidBufferFactory = windows.GUID{Data1: 0x71af914d, Data2: 0xc10f, Data3: 0x484b, Data4: [8]byte{0xbc, 0x50, 0x14, 0xbc, 0x62, 0x3b, 0x3a, 0x27}}
	iidBufferByte    = windows.GUID{Data1: 0x905a0fef, Data2: 0xbc53, Data3: 0x11df, Data4: [8]byte{0x8c, 0x49, 0, 0x1e, 0x4f, 0xc6, 0x86, 0xda}}
)

func check(hr uintptr, operation string) error {
	if int32(uint32(hr)) < 0 {
		return fmt.Errorf("%s HRESULT=0x%08X", operation, uint32(hr))
	}
	return nil
}
func call(o *object, slot int, args ...uintptr) uintptr {
	hr, _, _ := syscall.SyscallN(o.table[slot], append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)...)
	return hr
}
func release(o *object) {
	if o != nil {
		call(o, 2)
	}
}
func query(o *object, id *windows.GUID) (*object, error) {
	var next *object
	hr := call(o, 0, uintptr(unsafe.Pointer(id)), uintptr(unsafe.Pointer(&next)))
	return next, check(hr, "获取WinRT接口")
}
func hstring(text string) (uintptr, error) {
	units, err := windows.UTF16FromString(text)
	if err != nil {
		return 0, err
	}
	var value uintptr
	hr, _, _ := newString.Call(uintptr(unsafe.Pointer(&units[0])), uintptr(len(units)-1), uintptr(unsafe.Pointer(&value)))
	return value, check(hr, "创建WinRT字符串")
}
func factory(name string, id *windows.GUID) (*object, error) {
	text, err := hstring(name)
	if err != nil {
		return nil, err
	}
	defer deleteString.Call(text)
	var obj *object
	hr, _, _ := factoryProc.Call(text, uintptr(unsafe.Pointer(id)), uintptr(unsafe.Pointer(&obj)))
	return obj, check(hr, "获取WinRT factory")
}
func activate(name string) (*object, error) {
	text, err := hstring(name)
	if err != nil {
		return nil, err
	}
	defer deleteString.Call(text)
	var obj *object
	hr, _, _ := activation.Call(text, uintptr(unsafe.Pointer(&obj)))
	return obj, check(hr, "激活WinRT对象")
}
func setString(o *object, slot int, value string) error {
	text, err := hstring(value)
	if err != nil {
		return err
	}
	defer deleteString.Call(text)
	return check(call(o, slot, text), "设置Shell根字符串")
}

func folder(path string) (*object, error) {
	f, err := factory("Windows.Storage.StorageFolder", &iidFolder)
	if err != nil {
		return nil, err
	}
	defer release(f)
	text, err := hstring(path)
	if err != nil {
		return nil, err
	}
	defer deleteString.Call(text)
	var async *object
	if err := check(call(f, 6, text, uintptr(unsafe.Pointer(&async))), "读取同步根StorageFolder"); err != nil {
		return nil, err
	}
	defer release(async)
	info, err := query(async, &iidAsyncInfo)
	if err != nil {
		return nil, err
	}
	defer release(info)
	deadline := time.Now().Add(5 * time.Second)
	for {
		var status int32
		if err := check(call(info, 7, uintptr(unsafe.Pointer(&status))), "查询StorageFolder请求"); err != nil {
			return nil, err
		}
		if status == 1 {
			break
		}
		if status != 0 || time.Now().After(deadline) {
			return nil, fmt.Errorf("StorageFolder请求失败或超时: %d", status)
		}
		time.Sleep(20 * time.Millisecond)
	}
	var result *object
	if err := check(call(async, 8, uintptr(unsafe.Pointer(&result))), "取得StorageFolder"); err != nil {
		return nil, err
	}
	return result, nil
}
func contextBuffer(identity []byte) (*object, error) {
	f, err := factory("Windows.Storage.Streams.Buffer", &iidBufferFactory)
	if err != nil {
		return nil, err
	}
	defer release(f)
	var buffer *object
	if err := check(call(f, 6, uintptr(len(identity)), uintptr(unsafe.Pointer(&buffer))), "创建根身份buffer"); err != nil {
		return nil, err
	}
	bytes, err := query(buffer, &iidBufferByte)
	if err != nil {
		release(buffer)
		return nil, err
	}
	defer release(bytes)
	var data *byte
	if err := check(call(bytes, 3, uintptr(unsafe.Pointer(&data))), "取得根身份buffer地址"); err != nil {
		release(buffer)
		return nil, err
	}
	copy(unsafe.Slice(data, len(identity)), identity)
	if err := check(call(buffer, 8, uintptr(len(identity))), "设置根身份长度"); err != nil {
		release(buffer)
		return nil, err
	}
	return buffer, nil
}

// Register 在挂载前补充Shell根展示；ProviderName必须与已有CFAPI登记一致。
// id采用 provider!WindowsSID!空间稳定身份；普通退出不得调用Unregister。
func Register(path, id, label, icon, version string, identity []byte) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := roInitialize.Call(1)
	if err := check(hr, "初始化WinRT"); err != nil {
		return err
	}
	defer roUninitialize.Call()
	base, err := activate("Windows.Storage.Provider.StorageProviderSyncRootInfo")
	if err != nil {
		return err
	}
	defer release(base)
	info, err := query(base, &iidInfo)
	if err != nil {
		return err
	}
	defer release(info)
	for _, value := range []struct {
		slot  int
		value string
	}{{7, id}, {13, label}, {15, icon}, {29, version}} {
		if err := setString(info, value.slot, value.value); err != nil {
			return err
		}
	}
	root, err := folder(path)
	if err != nil {
		return err
	}
	defer release(root)
	if err := check(call(info, 11, uintptr(unsafe.Pointer(root))), "设置同步根路径"); err != nil {
		return err
	}
	buffer, err := contextBuffer(identity)
	if err != nil {
		return err
	}
	defer release(buffer)
	if err := check(call(info, 9, uintptr(unsafe.Pointer(buffer))), "设置同步根身份"); err != nil {
		return err
	}
	for _, value := range []struct {
		slot  int
		value uintptr
	}{{17, 1}, {19, 0}, {21, 3}, {23, 0}, {25, 0}, {27, 0}, {33, 1}} {
		if err := check(call(info, value.slot, value.value), "设置同步根策略"); err != nil {
			return err
		}
	}
	manager, err := factory("Windows.Storage.Provider.StorageProviderSyncRootManager", &iidManager)
	if err != nil {
		return err
	}
	defer release(manager)
	return check(call(manager, 6, uintptr(unsafe.Pointer(info))), "登记Shell云盘提供方")
}

// UnregisterIsolated 仅供调用方已明确证明自有的空实验根清理；不能作正常退出。
func UnregisterIsolated(id string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := roInitialize.Call(1)
	if err := check(hr, "初始化WinRT"); err != nil {
		return err
	}
	defer roUninitialize.Call()
	manager, err := factory("Windows.Storage.Provider.StorageProviderSyncRootManager", &iidManager)
	if err != nil {
		return err
	}
	defer release(manager)
	text, err := hstring(id)
	if err != nil {
		return err
	}
	defer deleteString.Call(text)
	return check(call(manager, 7, text), "清理自有实验Shell根")
}
