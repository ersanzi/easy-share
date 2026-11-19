//go:build windows

package shellundo

import (
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	fileOpSetFlags = 5
	fileOpSetOwner = 9
	fileOpDelete   = 18
	fileOpPerform  = 21
	fileOpAborted  = 22
	// 不设置 SILENT/NOCONFIRMATION/NOERRORUI，不自动确认系统复杂对话框。
	fofxRecycleOnDelete = 0x00080000
	fofxAddUndoRecord   = 0x20000000
	fofWantNukeWarning  = 0x00004000
	recycleFlags        = fofxRecycleOnDelete | fofxAddUndoRecord | fofWantNukeWarning
)

var (
	ole32Undo          = windows.NewLazySystemDLL("ole32.dll")
	procUndoInitialize = ole32Undo.NewProc("CoInitializeEx")
	procUndoCreate     = ole32Undo.NewProc("CoCreateInstance")
	procUndoShellItem  = windows.NewLazySystemDLL("shell32.dll").NewProc("SHCreateItemFromParsingName")
	clsidFileOperation = windows.GUID{Data1: 0x3ad05575, Data2: 0x8857, Data3: 0x4850, Data4: [8]byte{0x92, 0x77, 0x11, 0xb8, 0x5b, 0xdb, 0x8e, 0x09}}
	iidFileOperation   = windows.GUID{Data1: 0x947aab5f, Data2: 0x0a5c, Data3: 0x4c13, Data4: [8]byte{0xb4, 0xd6, 0x4b, 0xf7, 0x83, 0x6f, 0xc9, 0xf8}}
	iidShellItem       = windows.GUID{Data1: 0x43826d1e, Data2: 0xe718, Data3: 0x42ee, Data4: [8]byte{0xbc, 0x55, 0xa1, 0xe2, 0x61, 0xc3, 0x7b, 0xfe}}
)

type comObject struct{ table *[23]uintptr }

// HRESULTError 保留原始失败码，便于调用方区分权限、取消和 provider 失败。
type HRESULTError struct {
	Operation string
	Code      uint32
}

func (e *HRESULTError) Error() string { return fmt.Sprintf("%s HRESULT=0x%08X", e.Operation, e.Code) }

func checkHRESULT(hr uintptr, operation string) error {
	if int32(uint32(hr)) < 0 {
		return &HRESULTError{Operation: operation, Code: uint32(hr)}
	}
	return nil
}

func callCOM(object *comObject, slot int, args ...uintptr) uintptr {
	hr, _, _ := syscall.SyscallN(object.table[slot], append([]uintptr{uintptr(unsafe.Pointer(object))}, args...)...)
	return hr
}

func releaseCOM(object *comObject) {
	if object != nil {
		callCOM(object, 2) // IUnknown::Release 返回引用数，不是 HRESULT。
	}
}

// NativeRecycle 仅调用原生 IFileOperation；调用前必须由主线授权并完成临时 hydration。
// 不负责云端凭据、server delete 或 undo tracking，也不承诺用本地占位元数据代替文件正文。
func NativeRecycle(paths []string) error { return NativeRecycleWithOwner(paths, 0) }

// NativeRecycleWithOwner 可把原生确认/错误对话框归属给现有 EasyShare 窗口。
// 使用独立 STA 线程，不要求调用方线程已经初始化 COM，也不会新启动 Wails/Core。
func NativeRecycleWithOwner(paths []string, owner uintptr) error {
	normal, err := normalizeSelection(paths)
	if err != nil {
		return err
	}
	for _, path := range normal {
		if err := inspectLocalPath(path, false); err != nil {
			return fmt.Errorf("原生删除路径校验 %q: %w", path, err)
		}
	}
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		hr, _, _ := procUndoInitialize.Call(0, windows.COINIT_APARTMENTTHREADED)
		if err := checkHRESULT(hr, "CoInitializeEx(STA)"); err != nil {
			done <- err
			return
		}
		// S_FALSE 同样表示成功初始化，必须成对 CoUninitialize。
		defer windows.CoUninitialize()
		done <- recycleSTA(normal, owner, createFileOperation, createShellItem)
	}()
	return <-done
}

func createFileOperation() (*comObject, error) {
	var object *comObject
	hr, _, _ := procUndoCreate.Call(uintptr(unsafe.Pointer(&clsidFileOperation)), 0, 1 /* CLSCTX_INPROC_SERVER */, uintptr(unsafe.Pointer(&iidFileOperation)), uintptr(unsafe.Pointer(&object)))
	if err := checkHRESULT(hr, "CoCreateInstance(IFileOperation)"); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, fmt.Errorf("CoCreateInstance(IFileOperation) 成功但返回空对象")
	}
	return object, nil
}

func createShellItem(path string) (*comObject, error) {
	text, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, fmt.Errorf("Shell 路径编码: %w", err)
	}
	var item *comObject
	hr, _, _ := procUndoShellItem.Call(uintptr(unsafe.Pointer(text)), 0, uintptr(unsafe.Pointer(&iidShellItem)), uintptr(unsafe.Pointer(&item)))
	if err := checkHRESULT(hr, "SHCreateItemFromParsingName(IShellItem)"); err != nil {
		return nil, err
	}
	if item == nil {
		return nil, fmt.Errorf("SHCreateItemFromParsingName 成功但返回空对象")
	}
	return item, nil
}

// 注入对象构造器只用于 ABI 单测，不把替代实现暴露给产品调用方。
func recycleSTA(paths []string, owner uintptr, newOperation func() (*comObject, error), newItem func(string) (*comObject, error)) error {
	operation, err := newOperation()
	if err != nil {
		return err
	}
	if operation == nil {
		return fmt.Errorf("IFileOperation 对象为空")
	}
	defer releaseCOM(operation)
	if err := checkHRESULT(callCOM(operation, fileOpSetFlags, recycleFlags), "IFileOperation.SetOperationFlags"); err != nil {
		return err
	}
	if err := checkHRESULT(callCOM(operation, fileOpSetOwner, owner), "IFileOperation.SetOwnerWindow"); err != nil {
		return err
	}
	// 为每个 DeleteItem 提供原生 progress sink，传播逐项 HRESULT，避免 Perform 的 S_OK 掩盖跳过。
	sink := newDeleteSink()
	defer runtime.KeepAlive(sink)
	for _, path := range paths {
		item, err := newItem(path)
		if err != nil {
			return fmt.Errorf("准备删除 %q: %w", path, err)
		}
		if item == nil {
			return fmt.Errorf("准备删除 %q: Shell 对象为空", path)
		}
		hr := callCOM(operation, fileOpDelete, uintptr(unsafe.Pointer(item)), uintptr(unsafe.Pointer(sink)))
		releaseCOM(item)
		if err := checkHRESULT(hr, "IFileOperation.DeleteItem "+path); err != nil {
			return err // 尚未 Perform，不产生半套排队操作。
		}
	}
	performErr := checkHRESULT(callCOM(operation, fileOpPerform), "IFileOperation.PerformOperations")
	var aborted int32 // Win32 BOOL 是 32 位，而非 Go bool。
	abortedErr := checkHRESULT(callCOM(operation, fileOpAborted, uintptr(unsafe.Pointer(&aborted))), "IFileOperation.GetAnyOperationsAborted")
	if aborted != 0 && abortedErr == nil {
		abortedErr = ErrAborted
	}
	return errors.Join(performErr, abortedErr, sink.failure())
}
