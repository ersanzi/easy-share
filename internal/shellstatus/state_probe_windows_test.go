//go:build windows

package shellstatus

import (
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// 文档公开提供方状态枚举的受控隔离验证，不写UIStatus BLOB或改用户文件。
func TestProviderStateOverlayProbe(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_MULTITHREADED); err != nil {
		t.Fatal(err)
	}
	defer windows.CoUninitialize()
	path := registeredStatusFixture(t)
	store, err := openVolatileStore(path, 0x1002)
	if err != nil {
		t.Fatal(err)
	}
	defer release(store)
	key := propertyKey{Format: windows.GUID{Data1: 0xe77e90df, Data2: 0x6271, Data3: 0x4f5b, Data4: [8]byte{0x83, 0x4f, 0x2d, 0xd1, 0xf2, 0x45, 0xdd, 0xa4}}, ID: 3}
	for _, state := range []uint32{6, 7, 1} {
		value := propertyVariant{Type: 19, Count: state}
		if err := hresult(comCall(store, 6, uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(&value))), "提供方状态写入"); err != nil {
			if err.Error() == "提供方状态写入 HRESULT=0x80004001" {
				t.Log("本系统StorageProviderState只读；保持未知/原生状态，不绕过写合成图标")
				return
			}
			t.Fatal(err)
		}
		if err := hresult(comCall(store, 7), "提供方状态提交"); err != nil {
			t.Fatal(err)
		}
		var read propertyVariant
		if err := hresult(comCall(store, 5, uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(&read))), "提供方状态回读"); err != nil {
			t.Fatal(err)
		}
		t.Logf("providerState=%d gotType=%x value=%d", state, read.Type, read.Count)
	}
}
